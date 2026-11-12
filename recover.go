package taskpkg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"math/big"
	"sort"
)

// Recovery-related error sentinels.
var (
	// ErrRecoveryPlan is the base error for a malformed recovery plan.
	ErrRecoveryPlan = errors.New("taskpkg: invalid recovery plan")
	// ErrDoubleUnmask is returned when a personal seed is requested for a
	// non-completed device: together with a cross-set pair it would solve a
	// surviving device's private value, the forbidden double unmask.
	ErrDoubleUnmask = errors.New("taskpkg: personal seed recovered outside the completion set")
	// ErrIntraSetPair is returned for a pairwise seed whose two endpoints both
	// completed; their masks already cancel and must never be reconstructed.
	ErrIntraSetPair = errors.New("taskpkg: pairwise seed requested for two completed devices")
	// ErrEndpointNotCompleted is returned when a cross-set pair does not have
	// exactly one completed endpoint.
	ErrEndpointNotCompleted = errors.New("taskpkg: cross-set pair needs exactly one completed endpoint")
	// ErrPairOrientation is returned when the pair endpoints are not ordered low/high.
	ErrPairOrientation = errors.New("taskpkg: pair endpoints must be ordered low, high")
	// ErrDigestMismatch is returned when the plan digest does not match the
	// canonical digest of its completed roster.
	ErrDigestMismatch = errors.New("taskpkg: recovery plan digest mismatch")
	// ErrRecoveryMismatch is returned when a callback returns an unexpected key.
	ErrRecoveryMismatch = errors.New("taskpkg: recovery callback returned an unexpected key")
)

// PairID identifies a pairwise seed by its two endpoints, ordered low to high.
type PairID struct {
	Low  DeviceID
	High DeviceID
}

// NewPairID canonicalizes an unordered pair of device identities.
func NewPairID(a, b DeviceID) PairID {
	if a < b {
		return PairID{Low: a, High: b}
	}
	return PairID{Low: b, High: a}
}

// PersonalRecovery requests the scalar personal mask of each completed device.
type PersonalRecovery map[DeviceID]*big.Int

// PairSeed carries the same pairwise scalar as seen from each endpoint, together
// with the canonical orientation so the mask sign is unambiguous.
type PairSeed struct {
	LowMask  *big.Int
	HighMask *big.Int
}

// PairwiseRecovery requests cross-set pairwise scalars for each surviving edge.
type PairwiseRecovery map[PairID]PairSeed

// Recoverer is the host callback. Personal seeds are recovered in one call and
// cross-set pairwise seeds in another; a device never receives both kinds.
type Recoverer interface {
	RecoverPersonal(ctx context.Context, devices []DeviceID) (PersonalRecovery, error)
	RecoverPairwise(ctx context.Context, pairs []PairID) (PairwiseRecovery, error)
}

// RecoveryPlan is the host's proposed set of seeds to reconstruct. ValidatePlan
// enforces every privacy invariant before any seed is handed to a callback.
type RecoveryPlan struct {
	// Round and Prime mirror the phase configuration.
	Round uint64
	Prime *big.Int
	// Digest is the canonical digest of Completed, as produced by RosterDigest.
	Digest []byte
	// Completed is the surviving completion set, sorted.
	Completed []DeviceID
	// Dropped is every roster device outside the completion set, sorted.
	Dropped []DeviceID
	// PersonalTargets holds every completed device exactly once.
	PersonalTargets []DeviceID
	// PairTargets holds each cross-set pair exactly once.
	PairTargets []PairID
}

// Result is the finalized aggregate for one completion set.
type Result struct {
	// Digest identifies the unique completion set represented by this result.
	Digest []byte
	// Completed is the sorted completion set.
	Completed []DeviceID
	// Dropped is the sorted non-completion set.
	Dropped []DeviceID
	// Confirmations is the number of distinct trusted devices signing Digest.
	Confirmations int
	// Aggregate is the unmasked sum of the completed devices, modulo Prime.
	Aggregate *big.Int
	// Shares records the accepted share type of each completed device.
	Shares map[DeviceID]ShareType
}

// ValidatePlan checks the plan against the phase privacy rules. It does not
// invoke any callback, so it is safe to use in tests and host-side auditing.
func (p *Phase) ValidatePlan(plan RecoveryPlan) error {
	if plan.Prime == nil || plan.Prime.Cmp(p.prime) != 0 || plan.Round != p.cfg.Round {
		return ErrRecoveryPlan
	}
	completed := append([]DeviceID(nil), plan.Completed...)
	sort.Strings(toStrings(completed))
	if !sortedUnique(completed) {
		return ErrRecoveryPlan
	}
	dropped := append([]DeviceID(nil), plan.Dropped...)
	if !sortedUnique(dropped) {
		return ErrRecoveryPlan
	}

	all := append(append([]DeviceID(nil), completed...), dropped...)
	if len(all) != len(p.cfg.Roster) {
		return ErrRecoveryPlan
	}
	roster := append([]DeviceID(nil), p.cfg.Roster...)
	sort.Slice(roster, func(i, j int) bool { return roster[i] < roster[j] })
	sort.Strings(toStrings(all))
	for i, id := range roster {
		if all[i] != id {
			return ErrRecoveryPlan
		}
	}

	wantDigest := RosterDigest(plan.Round, plan.Prime, completed)
	if !bytes.Equal(wantDigest, plan.Digest) {
		return ErrDigestMismatch
	}

	completedSet := make(map[DeviceID]struct{}, len(completed))
	for _, id := range completed {
		completedSet[id] = struct{}{}
	}

	personalSeen := make(map[DeviceID]struct{}, len(plan.PersonalTargets))
	for _, id := range plan.PersonalTargets {
		if _, ok := completedSet[id]; !ok {
			// A personal seed for a non-completed device, combined with any pair
			// it shares with a survivor, would solve that survivor's private value.
			return ErrDoubleUnmask
		}
		if _, dup := personalSeen[id]; dup {
			return ErrRecoveryPlan
		}
		personalSeen[id] = struct{}{}
	}
	if len(personalSeen) != len(completed) {
		return ErrRecoveryPlan
	}

	pairSeen := make(map[PairID]struct{}, len(plan.PairTargets))
	for _, pair := range plan.PairTargets {
		if pair.Low >= pair.High {
			return ErrPairOrientation
		}
		if _, dup := pairSeen[pair]; dup {
			return ErrRecoveryPlan
		}
		pairSeen[pair] = struct{}{}
		_, lowDone := completedSet[pair.Low]
		_, highDone := completedSet[pair.High]
		switch {
		case lowDone && highDone:
			return ErrIntraSetPair
		case !lowDone && !highDone:
			return ErrEndpointNotCompleted
		}
	}

	var expected []PairID
	for _, c := range completed {
		for _, d := range dropped {
			expected = append(expected, NewPairID(c, d))
		}
	}
	if len(expected) != len(plan.PairTargets) {
		return ErrEndpointNotCompleted
	}
	sort.Slice(expected, func(i, j int) bool {
		if expected[i].Low != expected[j].Low {
			return expected[i].Low < expected[j].Low
		}
		return expected[i].High < expected[j].High
	})
	got := append([]PairID(nil), plan.PairTargets...)
	sort.Slice(got, func(i, j int) bool {
		if got[i].Low != got[j].Low {
			return got[i].Low < got[j].Low
		}
		return got[i].High < got[j].High
	})
	for i := range expected {
		if expected[i] != got[i] {
			return ErrEndpointNotCompleted
		}
	}
	return nil
}

// Finalize selects the threshold-confirmed completion set, validates the
// recovery plan, invokes the two recovery callbacks, removes the completed
// devices' personal masks and every cross-set pairwise mask, and returns the
// aggregate modulo Prime.
func (p *Phase) Finalize(ctx context.Context, plan RecoveryPlan, recoverer Recoverer) (*Result, error) {
	if !p.sealed {
		return nil, ErrPhaseNotSealed
	}
	if recoverer == nil {
		return nil, ErrRecoveryPlan
	}
	winner, confirmations, err := p.winningDigest()
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(winner, plan.Digest) {
		return nil, ErrDigestMismatch
	}
	if err := p.ValidatePlan(plan); err != nil {
		return nil, err
	}
	for _, id := range plan.Completed {
		sub, ok := p.submissions[id]
		if !ok || !bytes.Equal(sub.Digest, plan.Digest) {
			return nil, ErrDigestMismatch
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	personal, err := recoverer.RecoverPersonal(ctx, append([]DeviceID(nil), plan.PersonalTargets...))
	if err != nil {
		return nil, err
	}
	if len(personal) != len(plan.PersonalTargets) {
		return nil, ErrRecoveryMismatch
	}
	for _, id := range plan.PersonalTargets {
		value, ok := personal[id]
		if !ok || value == nil {
			return nil, ErrRecoveryMismatch
		}
	}

	pairwise, err := recoverer.RecoverPairwise(ctx, append([]PairID(nil), plan.PairTargets...))
	if err != nil {
		return nil, err
	}
	if len(pairwise) != len(plan.PairTargets) {
		return nil, ErrRecoveryMismatch
	}
	for _, pair := range plan.PairTargets {
		seed, ok := pairwise[pair]
		if !ok || seed.LowMask == nil || seed.HighMask == nil {
			return nil, ErrRecoveryMismatch
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	completed := append([]DeviceID(nil), plan.Completed...)
	sort.Strings(toStrings(completed))
	completedSet := make(map[DeviceID]struct{}, len(completed))
	for _, id := range completed {
		completedSet[id] = struct{}{}
	}

	aggregate := big.NewInt(0)
	for _, id := range completed {
		aggregate.Add(aggregate, p.submissions[id].MaskedValue)
	}

	for _, id := range completed {
		aggregate.Sub(aggregate, modPrime(personal[id], p.prime))
	}

	for _, pair := range plan.PairTargets {
		seed := pairwise[pair]
		if _, ok := completedSet[pair.Low]; ok {
			// Completed endpoint is the low ID: its contribution was additive.
			aggregate.Sub(aggregate, modPrime(seed.LowMask, p.prime))
		} else {
			// Completed endpoint is the high ID: its contribution was subtractive.
			aggregate.Add(aggregate, modPrime(seed.HighMask, p.prime))
		}
	}
	aggregate.Mod(aggregate, p.prime)

	shares := make(map[DeviceID]ShareType, len(completed))
	for _, id := range completed {
		shares[id] = p.submissions[id].ShareType
	}

	return &Result{
		Digest:        append([]byte(nil), plan.Digest...),
		Completed:     completed,
		Dropped:       append([]DeviceID(nil), plan.Dropped...),
		Confirmations: confirmations,
		Aggregate:     aggregate,
		Shares:        shares,
	}, nil
}

// BuildRecoveryPlan constructs the only legal plan for a confirmed completion
// set: every completed device gets a personal recovery entry, and every
// completed/dropped pair gets exactly one cross-set pairwise entry.
func (p *Phase) BuildRecoveryPlan(digest []byte, completed []DeviceID) (RecoveryPlan, error) {
	completedSet := make(map[DeviceID]struct{}, len(completed))
	for _, id := range completed {
		completedSet[id] = struct{}{}
	}
	var done, dropped []DeviceID
	for _, id := range p.cfg.Roster {
		if _, ok := completedSet[id]; ok {
			done = append(done, id)
		} else {
			dropped = append(dropped, id)
		}
	}
	sort.Strings(toStrings(done))
	sort.Strings(toStrings(dropped))

	plan := RecoveryPlan{
		Round:           p.cfg.Round,
		Prime:           new(big.Int).Set(p.prime),
		Digest:          append([]byte(nil), digest...),
		Completed:       done,
		Dropped:         dropped,
		PersonalTargets: append([]DeviceID(nil), done...),
	}
	for _, c := range done {
		for _, d := range dropped {
			plan.PairTargets = append(plan.PairTargets, NewPairID(c, d))
		}
	}
	if len(plan.Digest) != sha256.Size {
		return plan, ErrDigestMismatch
	}
	return plan, p.ValidatePlan(plan)
}

func toStrings(ids []DeviceID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = string(id)
	}
	return out
}

func sortedUnique(ids []DeviceID) bool {
	for i := 1; i < len(ids); i++ {
		if ids[i-1] >= ids[i] {
			return false
		}
	}
	return true
}
