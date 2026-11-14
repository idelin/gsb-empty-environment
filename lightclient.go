package netsec

import (
	"fmt"
	"sync"
)

// TrustedState is an out-of-band trusted checkpoint for a source chain.
// The checkpoint header is treated as final.
type TrustedState struct {
	Header     Header       `json:"header"`
	Validators ValidatorSet `json:"validators"`
}

// LightClient verifies consensus-signed headers, tracks the canonical chain,
// applies a finality depth, and fails closed on a finality violation.
type LightClient struct {
	mu sync.RWMutex

	chainID       ChainID
	validators    ValidatorSet
	validatorHash Hash
	trustedHeight uint64
	trustedHash   Hash
	finalityDepth uint64

	headers   map[uint64]map[Hash]Header
	canonical map[uint64]Header
	head      Header
	finalized uint64
	violation error
}

// NewLightClient creates a light client from a trusted checkpoint. finalityDepth
// is the number of headers that must be built on top of a header before it is
// considered final. A depth of zero means the head is final immediately.
func NewLightClient(trusted TrustedState, finalityDepth uint64) (*LightClient, error) {
	if err := trusted.Header.Validate(); err != nil {
		return nil, fmt.Errorf("netsec: trusted header: %w", err)
	}
	validators, err := NewValidatorSet(trusted.Validators.Validators)
	if err != nil {
		return nil, err
	}
	if trusted.Header.ValidatorsHash != validators.Hash() {
		return nil, fmt.Errorf("%w: trusted header does not commit to the trusted validator set", ErrUnknownValidatorSet)
	}
	header := trusted.Header
	lc := &LightClient{
		chainID:       header.ChainID,
		validators:    validators,
		validatorHash: validators.Hash(),
		trustedHeight: header.Height,
		trustedHash:   header.BlockHash,
		finalityDepth: finalityDepth,
		headers:       make(map[uint64]map[Hash]Header),
		canonical:     make(map[uint64]Header),
		head:          header,
		finalized:     header.Height,
	}
	lc.headers[header.Height] = map[Hash]Header{header.BlockHash: header}
	lc.canonical[header.Height] = header
	return lc, nil
}

// ChainID returns the source chain this client validates.
func (lc *LightClient) ChainID() ChainID {
	lc.mu.RLock()
	defer lc.mu.RUnlock()
	return lc.chainID
}

// Validators returns a copy of the active validator set.
func (lc *LightClient) Validators() ValidatorSet {
	lc.mu.RLock()
	defer lc.mu.RUnlock()
	return copyValidatorSet(lc.validators)
}

// FinalityDepth returns the configured confirmation depth.
func (lc *LightClient) FinalityDepth() uint64 {
	lc.mu.RLock()
	defer lc.mu.RUnlock()
	return lc.finalityDepth
}

// Head returns the current canonical head.
func (lc *LightClient) Head() Header {
	lc.mu.RLock()
	defer lc.mu.RUnlock()
	return lc.head
}

// FinalizedHeight returns the highest height considered final.
func (lc *LightClient) FinalizedHeight() uint64 {
	lc.mu.RLock()
	defer lc.mu.RUnlock()
	return lc.finalized
}

// Violation returns a non-nil error after a finality violation. Once set,
// verification fails closed until Reset installs a new trusted checkpoint.
func (lc *LightClient) Violation() error {
	lc.mu.RLock()
	defer lc.mu.RUnlock()
	return lc.violation
}

// CanonicalHeader returns the canonical header at height.
func (lc *LightClient) CanonicalHeader(height uint64) (Header, bool) {
	lc.mu.RLock()
	defer lc.mu.RUnlock()
	header, ok := lc.canonical[height]
	return header, ok
}

// CanonicalView is an atomic snapshot used for message verification.
type CanonicalView struct {
	Header          Header
	Head            Header
	FinalizedHeight uint64
	Violation       error
}

// View returns the canonical header at height together with the current head,
// finalized height, and health state under one lock.
func (lc *LightClient) View(height uint64) (CanonicalView, bool) {
	lc.mu.RLock()
	defer lc.mu.RUnlock()
	header, ok := lc.canonical[height]
	if !ok {
		return CanonicalView{Violation: lc.violation}, false
	}
	return CanonicalView{
		Header:          header,
		Head:            lc.head,
		FinalizedHeight: lc.finalized,
		Violation:       lc.violation,
	}, true
}

// Confirmations returns head-height+1 for a canonical header.
func (lc *LightClient) Confirmations(height uint64) (uint64, error) {
	lc.mu.RLock()
	defer lc.mu.RUnlock()
	if _, ok := lc.canonical[height]; !ok {
		return 0, ErrBlockNotCanonical
	}
	if height > lc.head.Height {
		return 0, ErrBlockNotCanonical
	}
	return lc.head.Height - height + 1, nil
}

// AddHeader verifies signatures and inserts a header. Headers must connect to
// a known parent; the relayer cannot skip heights. An alternative branch
// becomes canonical only when it is strictly longer than the current head.
//
// A reorg that would replace a finalized header freezes the client and
// returns an error wrapping both ErrFrozen and ErrFinalityViolation.
func (lc *LightClient) AddHeader(h Header, signatures []HeaderSignature) error {
	if err := h.Validate(); err != nil {
		return err
	}

	lc.mu.Lock()
	defer lc.mu.Unlock()

	if lc.violation != nil {
		return fmt.Errorf("%w: %w", ErrFrozen, lc.violation)
	}
	if h.ChainID != lc.chainID {
		return fmt.Errorf("%w: header chain %q, expected %q", ErrInvalidHeader, h.ChainID, lc.chainID)
	}
	if h.ValidatorsHash != lc.validatorHash {
		return fmt.Errorf("%w: header validator set hash does not match trusted set", ErrUnknownValidatorSet)
	}
	if err := VerifyHeaderSignatureSet(h, signatures, lc.validators); err != nil {
		return err
	}

	// The checkpoint is final. A conflicting header at exactly the checkpoint
	// height is direct evidence of a finality violation.
	if h.Height < lc.trustedHeight {
		return fmt.Errorf("%w: height %d below checkpoint %d", ErrBelowCheckpoint, h.Height, lc.trustedHeight)
	}
	if h.Height == lc.trustedHeight {
		if h.BlockHash == lc.trustedHash {
			return nil
		}
		return lc.recordViolation(fmt.Sprintf("header at checkpoint height %d conflicts with trusted hash", h.Height))
	}

	if existing, ok := lc.headers[h.Height][h.BlockHash]; ok {
		if existing == h {
			return nil
		}
		return fmt.Errorf("%w: conflicting header for the same height and hash", ErrInvalidHeader)
	}
	if !lc.hasHeader(h.Height-1, h.ParentHash) {
		return fmt.Errorf("%w: height %d parent %s is unknown", ErrMissingParent, h.Height, h.ParentHash)
	}

	if lc.headers[h.Height] == nil {
		lc.headers[h.Height] = make(map[Hash]Header)
	}
	lc.headers[h.Height][h.BlockHash] = h

	// A non-canonical fork that is not longer than the current head cannot
	// affect the canonical chain yet.
	if h.Height <= lc.head.Height {
		return nil
	}

	// Normal operation extends the canonical head by one block.
	if h.Height == lc.head.Height+1 && h.ParentHash == lc.head.BlockHash {
		lc.canonical[h.Height] = h
		lc.head = h
		lc.advanceFinalized()
		return nil
	}

	path, err := lc.pathToTrusted(h)
	if err != nil {
		return err
	}

	ancestorHeight := lc.trustedHeight
	for _, branchHeader := range path {
		canonical, ok := lc.canonical[branchHeader.Height]
		if !ok || canonical.BlockHash != branchHeader.BlockHash {
			break
		}
		ancestorHeight = branchHeader.Height
	}

	if ancestorHeight < lc.finalized {
		return lc.recordViolation(fmt.Sprintf(
			"branch from height %d replaces finalized height %d (new head %d)",
			ancestorHeight, lc.finalized, h.Height))
	}

	for height := ancestorHeight + 1; height <= lc.head.Height; height++ {
		delete(lc.canonical, height)
	}
	for _, branchHeader := range path {
		if branchHeader.Height > ancestorHeight {
			lc.canonical[branchHeader.Height] = branchHeader
		}
	}
	lc.head = h
	lc.advanceFinalized()
	return nil
}

func (lc *LightClient) advanceFinalized() {
	if lc.head.Height > lc.finalityDepth {
		candidate := lc.head.Height - lc.finalityDepth
		if candidate > lc.finalized {
			lc.finalized = candidate
		}
	}
}

// Reset installs a new trusted checkpoint for the same chain. It is intended
// for an explicit administrative recovery after a finality violation or for
// validator-set rotation. It is not part of normal message verification.
func (lc *LightClient) Reset(trusted TrustedState) error {
	if err := trusted.Header.Validate(); err != nil {
		return fmt.Errorf("netsec: trusted header: %w", err)
	}
	validators, err := NewValidatorSet(trusted.Validators.Validators)
	if err != nil {
		return err
	}
	if trusted.Header.ValidatorsHash != validators.Hash() {
		return fmt.Errorf("%w: trusted header does not commit to the new validator set", ErrUnknownValidatorSet)
	}

	lc.mu.Lock()
	defer lc.mu.Unlock()
	if trusted.Header.ChainID != lc.chainID {
		return fmt.Errorf("%w: reset chain %q, expected %q", ErrInvalidHeader, trusted.Header.ChainID, lc.chainID)
	}
	header := trusted.Header
	lc.validators = validators
	lc.validatorHash = validators.Hash()
	lc.trustedHeight = header.Height
	lc.trustedHash = header.BlockHash
	lc.headers = map[uint64]map[Hash]Header{
		header.Height: {header.BlockHash: header},
	}
	lc.canonical = map[uint64]Header{header.Height: header}
	lc.head = header
	lc.finalized = header.Height
	lc.violation = nil
	return nil
}

func (lc *LightClient) hasHeader(height uint64, hash Hash) bool {
	branch, ok := lc.headers[height]
	if !ok {
		return false
	}
	_, ok = branch[hash]
	return ok
}

// pathToTrusted returns headers from checkpoint+1 through h in ascending order.
func (lc *LightClient) pathToTrusted(h Header) ([]Header, error) {
	var reversed []Header
	current := h
	for current.Height > lc.trustedHeight {
		reversed = append(reversed, current)
		parent, ok := lc.headers[current.Height-1][current.ParentHash]
		if !ok {
			return nil, fmt.Errorf("%w: height %d parent %s is unknown", ErrMissingParent, current.Height, current.ParentHash)
		}
		current = parent
	}
	if current.BlockHash != lc.trustedHash {
		return nil, fmt.Errorf("%w: branch does not connect to trusted checkpoint", ErrFinalityViolation)
	}
	path := make([]Header, len(reversed))
	for i := range reversed {
		path[len(reversed)-1-i] = reversed[i]
	}
	return path, nil
}

func (lc *LightClient) recordViolation(detail string) error {
	if lc.violation == nil {
		lc.violation = fmt.Errorf("%w: %s", ErrFinalityViolation, detail)
	}
	return fmt.Errorf("%w: %w", ErrFrozen, lc.violation)
}

func copyValidatorSet(set ValidatorSet) ValidatorSet {
	validators := make([]Validator, len(set.Validators))
	for i, v := range set.Validators {
		validators[i] = Validator{
			PublicKey: append([]byte(nil), v.PublicKey...),
			Power:     v.Power,
		}
	}
	return ValidatorSet{Validators: validators}
}
