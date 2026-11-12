package netsec

import (
	"crypto/elliptic"
	"fmt"
	"math/big"
	"sort"
	"sync"
)

// DisqualifyReason explains why a dealer was removed from a run.
type DisqualifyReason string

const (
	// ReasonDoubleBroadcast: the dealer's commitment hash differs
	// between two receivers.
	ReasonDoubleBroadcast DisqualifyReason = "double_broadcast"
	// ReasonUnresolvedComplaint: a valid complaint was not answered by
	// an in-window disclosure that verifies.
	ReasonUnresolvedComplaint DisqualifyReason = "unresolved_complaint"
	// ReasonInvalidDisclosure: the dealer answered a complaint but the
	// disclosed share fails the commitment check.
	ReasonInvalidDisclosure DisqualifyReason = "invalid_disclosure"
	// ReasonConstantClash: the dealer's constant-term commitment equals
	// another dealer's.
	ReasonConstantClash DisqualifyReason = "constant_commitment_clash"
)

// DealerStatus is the immutable snapshot of one dealer at finalization.
type DealerStatus struct {
	DealerID     string
	Commit       Commitment
	Disqualified bool
	Reason       DisqualifyReason
}

// Result is fixed once when the complaint window closes. Late complaints
// or disclosures never alter it.
type Result struct {
	// Round is the logical round at which the result was finalized.
	Round int
	// Qualified lists dealer IDs whose shares enter the aggregates.
	Qualified []string
	// Disqualified maps a dealer ID to its removal reason.
	Disqualified map[string]DisqualifyReason
	// FalseAccusers lists receiver IDs whose complaints were disproved by
	// a verifying in-window disclosure.
	FalseAccusers []string
	// AggregatedPublicKey is the product of the qualified dealers'
	// constant-term commitments.
	AggregatedPublicKey Point
	// AggregatedShares maps a receiver ID to the sum of the shares
	// issued by qualified dealers, reduced modulo the P-256 order.
	AggregatedShares map[string]*big.Int
}

type dealerState struct {
	id     string
	commit Commitment
	// canonicalHash is the hash seen by the first receiver registering a
	// receipt for this dealer.
	canonicalHash [32]byte
	hashSeen      bool
	disqualified  bool
	reason        DisqualifyReason
}

type receiverState struct {
	id string
	x  *big.Int
	// shares[dealerID] is the share this receiver registered.
	shares map[string]*big.Int
}

type complaint struct {
	dealerID   string
	receiverID string
	x          *big.Int
	share      *big.Int
	commitHash [32]byte
	round      int
}

// Engine runs the complaint window for one DKG instance.
//
// The window is measured in logical rounds advanced by the caller with
// Advance: with windowRounds = w the window spans rounds 1..w and is
// closed at round w+1. All state is guarded by an RWMutex, so the type
// is safe for concurrent use by many receiver goroutines.
type Engine struct {
	mu           sync.RWMutex
	windowRounds int
	round        int
	finalized    bool
	result       *Result

	dealers   map[string]*dealerState
	receivers map[string]*receiverState
	// receiverByX rejects duplicate receiver indices.
	receiverByX map[string]string
	// complaints[dealerID][receiverID] keeps the first valid complaint.
	complaints map[string]map[string]*complaint
	// disclosures[dealerID][receiverID] stores the first admitted
	// disclosure.
	disclosures map[string]map[string]*complaint
}

// NewEngine creates an engine whose complaint window stays open for
// windowRounds logical rounds. windowRounds must be positive.
func NewEngine(windowRounds int) (*Engine, error) {
	if windowRounds <= 0 {
		return nil, ErrWindowRounds
	}
	return &Engine{
		windowRounds: windowRounds,
		round:        1,
		dealers:      map[string]*dealerState{},
		receivers:    map[string]*receiverState{},
		receiverByX:  map[string]string{},
		complaints:   map[string]map[string]*complaint{},
		disclosures:  map[string]map[string]*complaint{},
	}, nil
}

// Round returns the current logical round (1-based).
func (e *Engine) Round() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.round
}

// WindowOpen reports whether complaints and disclosures are still
// accepted.
func (e *Engine) WindowOpen() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return !e.finalized && e.round <= e.windowRounds
}

// Advance moves the engine to the next logical round. It is a no-op once
// the result has been finalized.
func (e *Engine) Advance() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.finalized {
		return e.round
	}
	e.round++
	return e.round
}

func (e *Engine) checkOpenLocked() error {
	if e.finalized {
		return ErrFinalized
	}
	if e.round > e.windowRounds {
		return ErrWindowClosed
	}
	return nil
}

// RegisterReceiver enrolls a receiver with its public index x. Indices
// must be non-zero, in scalar range and unique across the run.
func (e *Engine) RegisterReceiver(receiverID string, x *big.Int) error {
	if receiverID == "" {
		return ErrEmptyID
	}
	if x == nil || x.Sign() == 0 {
		return ErrZeroIndex
	}
	if x.Cmp(Curve.Params().N) >= 0 {
		return ErrScalarOutOfRange
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.finalized {
		return ErrFinalized
	}
	if _, exists := e.receivers[receiverID]; exists {
		return ErrDuplicateReceiver
	}
	key := x.String()
	if other, taken := e.receiverByX[key]; taken {
		return fmt.Errorf("%w: index %s already used by %q", ErrDuplicateIndex, x.String(), other)
	}
	e.receivers[receiverID] = &receiverState{
		id:     receiverID,
		x:      new(big.Int).Set(x),
		shares: map[string]*big.Int{},
	}
	e.receiverByX[key] = receiverID
	return nil
}

// RegisterCommitment records a dealer's Feldman commitment. The
// commitment must pass Commitment.Validate and its constant term must
// not be the identity element.
func (e *Engine) RegisterCommitment(dealerID string, c Commitment) error {
	if dealerID == "" {
		return ErrEmptyID
	}
	if err := c.Validate(); err != nil {
		return err
	}
	if c.Constant().IsIdentity() {
		return ErrIdentityConstant
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.finalized {
		return ErrFinalized
	}
	if _, exists := e.dealers[dealerID]; exists {
		return ErrDuplicateDealer
	}
	e.dealers[dealerID] = &dealerState{id: dealerID, commit: c}
	return nil
}

// RegisterReceipt records the commitment hash a receiver observed for a
// dealer during the commitment broadcast. If two receivers observe
// different hashes for the same dealer the dealer is disqualified
// immediately for double broadcast and ErrDoubleBroadcast is returned.
func (e *Engine) RegisterReceipt(dealerID, receiverID string, commitHash [32]byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.finalized {
		return ErrFinalized
	}
	dealer, ok := e.dealers[dealerID]
	if !ok {
		return ErrUnknownDealer
	}
	if _, ok := e.receivers[receiverID]; !ok {
		return ErrUnknownReceiver
	}
	if !dealer.hashSeen {
		dealer.canonicalHash = commitHash
		dealer.hashSeen = true
		return nil
	}
	if dealer.canonicalHash != commitHash {
		dealer.disqualified = true
		dealer.reason = ReasonDoubleBroadcast
		return ErrDoubleBroadcast
	}
	return nil
}

// RegisterShare stores the share s issued by dealerID to receiverID at
// the receiver's registered index. Only in-range scalars are stored; the
// share itself is not verified here — a mismatch is raised through
// FileComplaint.
func (e *Engine) RegisterShare(dealerID, receiverID string, s *big.Int) error {
	if !inScalarRange(s) {
		return ErrScalarOutOfRange
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.finalized {
		return ErrFinalized
	}
	dealer, ok := e.dealers[dealerID]
	if !ok {
		return ErrUnknownDealer
	}
	receiver, ok := e.receivers[receiverID]
	if !ok {
		return ErrUnknownReceiver
	}
	if _, exists := receiver.shares[dealer.id]; exists {
		return ErrDuplicateShare
	}
	receiver.shares[dealer.id] = new(big.Int).Set(s)
	return nil
}

// FileComplaint submits a complaint during the open window. The
// complaint carries the challenged share and the exact commitment hash
// the receiver observed. A complaint whose own evidence verifies against
// the pinned commitment is rejected up front with ErrShareMismatch
// instead of being filed.
func (e *Engine) FileComplaint(dealerID, receiverID string, share *big.Int, commitHash [32]byte) error {
	if !inScalarRange(share) {
		return ErrScalarOutOfRange
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkOpenLocked(); err != nil {
		return err
	}
	dealer, ok := e.dealers[dealerID]
	if !ok {
		return ErrUnknownDealer
	}
	receiver, ok := e.receivers[receiverID]
	if !ok {
		return ErrUnknownReceiver
	}
	if !dealer.hashSeen || dealer.canonicalHash != commitHash {
		return ErrCommitmentHashMismatch
	}
	if err := VerifyShare(dealer.commit, receiver.x, share); err == nil {
		return ErrShareMismatch
	}
	if e.complaints[dealerID] == nil {
		e.complaints[dealerID] = map[string]*complaint{}
	}
	if _, exists := e.complaints[dealerID][receiverID]; exists {
		return ErrDuplicateComplaint
	}
	e.complaints[dealerID][receiverID] = &complaint{
		dealerID:   dealerID,
		receiverID: receiverID,
		x:          new(big.Int).Set(receiver.x),
		share:      new(big.Int).Set(share),
		commitHash: commitHash,
		round:      e.round,
	}
	return nil
}

// DiscloseShare lets a dealer answer a complaint within the window by
// publishing the challenged share. Admission only checks that a matching
// complaint exists and that the disclosure quotes the same commitment
// hash; whether the disclosed share actually verifies is adjudicated at
// finalization.
func (e *Engine) DiscloseShare(dealerID, receiverID string, share *big.Int, commitHash [32]byte) error {
	if !inScalarRange(share) {
		return ErrScalarOutOfRange
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkOpenLocked(); err != nil {
		return err
	}
	if _, ok := e.dealers[dealerID]; !ok {
		return ErrUnknownDealer
	}
	if _, ok := e.receivers[receiverID]; !ok {
		return ErrUnknownReceiver
	}
	byReceiver, ok := e.complaints[dealerID]
	if !ok {
		return ErrNoComplaint
	}
	if _, exists := byReceiver[receiverID]; !exists {
		return ErrNoComplaint
	}
	dealer := e.dealers[dealerID]
	if !dealer.hashSeen || dealer.canonicalHash != commitHash {
		return ErrCommitmentHashMismatch
	}
	if e.disclosures[dealerID] == nil {
		e.disclosures[dealerID] = map[string]*complaint{}
	}
	if _, exists := e.disclosures[dealerID][receiverID]; exists {
		return ErrDuplicateDisclosure
	}
	e.disclosures[dealerID][receiverID] = &complaint{
		dealerID:   dealerID,
		receiverID: receiverID,
		x:          new(big.Int).Set(e.receivers[receiverID].x),
		share:      new(big.Int).Set(share),
		commitHash: commitHash,
		round:      e.round,
	}
	return nil
}

// DealerStatus reports the current adjudication state of one dealer. The
// second return value is false for unknown dealers.
func (e *Engine) DealerStatus(dealerID string) (DealerStatus, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	dealer, ok := e.dealers[dealerID]
	if !ok {
		return DealerStatus{}, false
	}
	return DealerStatus{
		DealerID:     dealer.id,
		Commit:       dealer.commit,
		Disqualified: dealer.disqualified,
		Reason:       dealer.reason,
	}, true
}

// Finalize closes the run and computes the result. It may be called at
// most once and only after the window has closed (Advance was called
// windowRounds times). The returned result is immutable: subsequent
// complaints and disclosures are rejected and never change it.
func (e *Engine) Finalize() (*Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.finalized {
		return nil, ErrFinalized
	}
	if e.round <= e.windowRounds {
		return nil, ErrWindowStillOpen
	}

	result := &Result{
		Round:            e.round,
		Disqualified:     map[string]DisqualifyReason{},
		AggregatedShares: map[string]*big.Int{},
	}

	// Adjudicate every filed complaint against in-window disclosures.
	for dealerID, byReceiver := range e.complaints {
		dealer := e.dealers[dealerID]
		for receiverID, c := range byReceiver {
			d, answered := e.disclosures[dealerID][receiverID]
			switch {
			case !answered:
				dealer.disqualified = true
				dealer.reason = ReasonUnresolvedComplaint
			case VerifyShare(dealer.commit, c.x, d.share) == nil:
				result.FalseAccusers = append(result.FalseAccusers, receiverID)
			default:
				dealer.disqualified = true
				dealer.reason = ReasonInvalidDisclosure
			}
		}
	}

	// Constant-term commitments must be pairwise distinct across
	// dealers; every member of a collision is removed.
	byConstant := map[string][]string{}
	for id, dealer := range e.dealers {
		c0 := dealer.commit.Constant()
		key := string(elliptic.MarshalCompressed(Curve, c0.X, c0.Y))
		byConstant[key] = append(byConstant[key], id)
	}
	for _, ids := range byConstant {
		if len(ids) < 2 {
			continue
		}
		for _, id := range ids {
			dealer := e.dealers[id]
			dealer.disqualified = true
			dealer.reason = ReasonConstantClash
		}
	}

	qualified := make([]*dealerState, 0, len(e.dealers))
	for id, dealer := range e.dealers {
		if dealer.disqualified {
			result.Disqualified[id] = dealer.reason
			continue
		}
		qualified = append(qualified, dealer)
	}
	if len(qualified) == 0 {
		return nil, ErrNoQualifiedDealers
	}
	sort.Slice(qualified, func(i, j int) bool { return qualified[i].id < qualified[j].id })

	// Aggregated public key: product of qualified constant commitments.
	aggX, aggY := new(big.Int), new(big.Int)
	for _, dealer := range qualified {
		c0 := dealer.commit.Constant()
		aggX, aggY = Curve.Add(aggX, aggY, c0.X, c0.Y)
		result.Qualified = append(result.Qualified, dealer.id)
	}
	result.AggregatedPublicKey = Point{X: aggX, Y: aggY}

	// Aggregated shares: sum of qualified-dealer shares per receiver.
	receiverIDs := make([]string, 0, len(e.receivers))
	for id := range e.receivers {
		receiverIDs = append(receiverIDs, id)
	}
	sort.Strings(receiverIDs)
	n := Curve.Params().N
	for _, rid := range receiverIDs {
		receiver := e.receivers[rid]
		sum := new(big.Int)
		for _, dealer := range qualified {
			s, ok := receiver.shares[dealer.id]
			if !ok {
				return nil, fmt.Errorf("%w: receiver %q lacks share of dealer %q",
					ErrMissingShare, rid, dealer.id)
			}
			sum.Add(sum, s)
		}
		result.AggregatedShares[rid] = sum.Mod(sum, n)
	}

	sort.Strings(result.Qualified)
	sort.Strings(result.FalseAccusers)

	// The result is fixed exactly once on the success path.
	e.finalized = true
	e.result = result
	return result, nil
}
