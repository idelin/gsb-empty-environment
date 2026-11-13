package netsec

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
)

const (
	// DefaultMaxSubsets is larger than the maximum possible C(16, 8) = 12870.
	DefaultMaxSubsets uint64 = 20000
)

var (
	// ErrInsufficientShares indicates that fewer than t distinct valid shares
	// were supplied.
	ErrInsufficientShares = errors.New("netsec: insufficient valid shares")
	// ErrBudgetExhausted indicates that proving consistency would exceed the
	// caller's subset budget. No plaintext is returned in this case.
	ErrBudgetExhausted = errors.New("netsec: consistency budget exhausted")
	// ErrConflict indicates that valid threshold subsets recovered different
	// plaintexts.
	ErrConflict = errors.New("netsec: conflicting participant shares")
)

// Budget limits the number of threshold subsets examined by Decrypt.
// MaxSubsets == 0 selects DefaultMaxSubsets.
type Budget struct {
	MaxSubsets uint64
}

// SubsetRecovery records one complete t-subset reconstruction.
type SubsetRecovery struct {
	IDs []uint32
	D   Point
	M   Point
}

// ConflictEvidence identifies two valid t-subsets that recovered different
// plaintext points.
type ConflictEvidence struct {
	First  SubsetRecovery
	Second SubsetRecovery
}

// ConflictError is returned instead of choosing one of two inconsistent
// plaintexts.
type ConflictError struct {
	Evidence ConflictEvidence
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s: subsets %v and %v disagree", ErrConflict, e.Evidence.First.IDs, e.Evidence.Second.IDs)
}

// Unwrap allows errors.Is(err, ErrConflict).
func (e *ConflictError) Unwrap() error {
	return ErrConflict
}

// DecryptionResult is returned only after every valid t-subset has produced
// the same plaintext.
type DecryptionResult struct {
	Plaintext      Point
	ValidShares    int
	RejectedShares int
	SubsetsChecked uint64
}

// Decrypt validates the manifest and all available shares, reconstructs the
// plaintext from every valid t-subset, and publishes it only if all subsets
// agree. Invalid shares are ignored; duplicate identifiers contribute at most
// one share.
func Decrypt(manifest Manifest, ciphertext Ciphertext, shares []DecryptionShare, budget Budget) (DecryptionResult, error) {
	if err := ValidateManifest(manifest); err != nil {
		return DecryptionResult{}, err
	}
	if err := validateCiphertext(ciphertext); err != nil {
		return DecryptionResult{}, err
	}

	valid, rejected := collectValidShares(manifest, ciphertext, shares)
	if len(valid) < manifest.Threshold {
		return DecryptionResult{}, fmt.Errorf(
			"%w: have %d distinct valid shares, need %d",
			ErrInsufficientShares,
			len(valid),
			manifest.Threshold,
		)
	}

	subsetCount := binomial(uint64(len(valid)), uint64(manifest.Threshold))
	limit := budget.MaxSubsets
	if limit == 0 {
		limit = DefaultMaxSubsets
	}
	if subsetCount > limit {
		return DecryptionResult{}, fmt.Errorf(
			"%w: %d threshold subsets exceed the limit of %d",
			ErrBudgetExhausted,
			subsetCount,
			limit,
		)
	}

	var first *SubsetRecovery
	var second *SubsetRecovery
	var reconstructionErr error
	var checked uint64
	forEachCombination(len(valid), manifest.Threshold, func(indices []int) bool {
		checked++
		if checked > limit {
			reconstructionErr = ErrBudgetExhausted
			return false
		}
		subset := make([]DecryptionShare, len(indices))
		for i, index := range indices {
			subset[i] = valid[index]
		}
		recovery, err := recoverSubset(subset, ciphertext)
		if err != nil {
			reconstructionErr = err
			return false
		}
		if first == nil {
			current := recovery
			first = &current
			return true
		}
		if recovery.M != first.M {
			current := recovery
			second = &current
			return false
		}
		return true
	})

	if reconstructionErr != nil {
		return DecryptionResult{}, fmt.Errorf("%w: subset reconstruction failed", reconstructionErr)
	}
	if first == nil {
		return DecryptionResult{}, fmt.Errorf("%w: no threshold subset could be reconstructed", ErrInvalidInput)
	}
	if second != nil {
		return DecryptionResult{}, &ConflictError{
			Evidence: ConflictEvidence{
				First:  SubsetRecovery{IDs: append([]uint32(nil), first.IDs...), D: first.D, M: first.M},
				Second: SubsetRecovery{IDs: append([]uint32(nil), second.IDs...), D: second.D, M: second.M},
			},
		}
	}
	if checked > limit {
		return DecryptionResult{}, fmt.Errorf("%w: checked %d subsets", ErrBudgetExhausted, checked)
	}

	return DecryptionResult{
		Plaintext:      first.M,
		ValidShares:    len(valid),
		RejectedShares: rejected,
		SubsetsChecked: checked,
	}, nil
}

// Recover is an alias for Decrypt.
func Recover(manifest Manifest, ciphertext Ciphertext, shares []DecryptionShare, budget Budget) (DecryptionResult, error) {
	return Decrypt(manifest, ciphertext, shares, budget)
}

// Combine is an alias for Decrypt.
func Combine(manifest Manifest, ciphertext Ciphertext, shares []DecryptionShare, budget Budget) (DecryptionResult, error) {
	return Decrypt(manifest, ciphertext, shares, budget)
}

func collectValidShares(
	manifest Manifest,
	ciphertext Ciphertext,
	shares []DecryptionShare,
) ([]DecryptionShare, int) {
	byID := make(map[uint32]DecryptionShare)
	rejected := 0
	for _, share := range shares {
		if err := verifyShareUnchecked(manifest, ciphertext, share); err != nil {
			rejected++
			continue
		}
		if _, exists := byID[share.ID]; exists {
			continue
		}
		byID[share.ID] = share
	}
	valid := make([]DecryptionShare, 0, len(byID))
	for _, share := range byID {
		valid = append(valid, share)
	}
	sort.Slice(valid, func(i, j int) bool { return valid[i].ID < valid[j].ID })
	return valid, rejected
}

func recoverSubset(shares []DecryptionShare, ciphertext Ciphertext) (SubsetRecovery, error) {
	ids := make([]uint32, len(shares))
	points := make([]Point, len(shares))
	for i, share := range shares {
		ids[i] = share.ID
		points[i] = share.D
	}
	d, err := lagrangeCombination(ids, points)
	if err != nil {
		return SubsetRecovery{}, err
	}
	dPoint, err := pointFromCanonical(d)
	if err != nil {
		return SubsetRecovery{}, err
	}
	c2, err := pointFromCanonical(ciphertext.C2)
	if err != nil {
		return SubsetRecovery{}, err
	}
	m, err := pointToCanonical(addCurvePoints(c2, scalarMultCurve(bigMinusOne(), dPoint)))
	if err != nil {
		return SubsetRecovery{}, fmt.Errorf("%w: recovered plaintext is the identity", ErrInvalidInput)
	}
	return SubsetRecovery{
		IDs: append([]uint32(nil), ids...),
		D:   d,
		M:   m,
	}, nil
}

func bigMinusOne() *big.Int {
	return big.NewInt(-1)
}

func binomial(n, k uint64) uint64 {
	if k > n {
		return 0
	}
	if k > n-k {
		k = n - k
	}
	result := uint64(1)
	for i := uint64(1); i <= k; i++ {
		result = result * (n - k + i) / i
	}
	return result
}

func forEachCombination(n, k int, visit func([]int) bool) {
	if k <= 0 || k > n {
		return
	}
	indices := make([]int, k)
	for i := range indices {
		indices[i] = i
	}
	for {
		if !visit(indices) {
			return
		}
		i := k - 1
		for i >= 0 && indices[i] == n-k+i {
			i--
		}
		if i < 0 {
			return
		}
		indices[i]++
		for j := i + 1; j < k; j++ {
			indices[j] = indices[j-1] + 1
		}
	}
}
