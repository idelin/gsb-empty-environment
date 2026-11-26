package doublesign

import (
	"crypto/ed25519"
	"errors"
	"sync"
)

// Verdict is the outcome of feeding one share into the Auditor.
type Verdict string

const (
	VerdictAccepted  Verdict = "accepted"
	VerdictStaged    Verdict = "staged"
	VerdictDuplicate Verdict = "duplicate"
	VerdictRejected  Verdict = "rejected"
	VerdictFrozen    Verdict = "frozen"
	VerdictConvicted Verdict = "convicted"
)

// Result reports what Observe did with a share.
type Result struct {
	Verdict  Verdict
	Evidence *Evidence
	Reason   string
}

type phaseEntry struct {
	block []byte
	msg   Message
}

type heightState struct {
	prepares    map[uint64]phaseEntry
	commits     map[uint64]phaseEntry
	views       map[uint64]phaseEntry
	maxLocked   uint64
	maxQCHeight uint64
	maxQCRound  uint64
}

func newHeightState() *heightState {
	return &heightState{
		prepares: map[uint64]phaseEntry{},
		commits:  map[uint64]phaseEntry{},
		views:    map[uint64]phaseEntry{},
	}
}

type memberState struct {
	frozen       bool
	heights      map[uint64]*heightState
	commitTipH   uint64
	commitTipB   []byte
	hasCommitTip bool
}

// Auditor enforces the double-signing safety rules. It is safe for concurrent
// use. When attached to a Store, every accepted/authentic share and every
// conviction is durably persisted for crash recovery.
type Auditor struct {
	mu      sync.Mutex
	chainID string
	epochs  map[uint64]EpochSet
	members map[string]*memberState
	seen    map[string]bool
	staged  []Message
	store   *Store
	anchors map[uint64]AuthorityAnchor
	authPub ed25519.PublicKey
}

// NewAuditor creates an in-memory auditor.
func NewAuditor(chainID string) *Auditor {
	if chainID == "" {
		panic("empty chain id")
	}
	return &Auditor{
		chainID: chainID,
		epochs:  map[uint64]EpochSet{},
		members: map[string]*memberState{},
		seen:    map[string]bool{},
		anchors: map[uint64]AuthorityAnchor{},
	}
}

// PinAuthority binds the governance authority public key before replaying a
// persistence log that contains anchored epoch sets.
func (a *Auditor) PinAuthority(authorityPub ed25519.PublicKey) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.authPub) == 0 {
		a.authPub = authorityPub
	}
}

// RegisterEpoch installs an epoch validator set with its signed root anchor.
// The authority public key is pinned on first registration.
func (a *Auditor) RegisterEpoch(set EpochSet, anchor AuthorityAnchor, authorityPub ed25519.PublicKey) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if set.ChainID != a.chainID {
		return errors.New("epoch chain id mismatch")
	}
	if err := set.Validate(); err != nil {
		return err
	}
	if _, exists := a.epochs[set.Epoch]; exists {
		return errors.New("epoch already registered")
	}
	if len(a.authPub) == 0 {
		a.authPub = authorityPub
	} else if !bytesEqual(a.authPub, authorityPub) {
		return errors.New("authority public key cannot change")
	}
	if err := anchor.Verify(set, authorityPub); err != nil {
		return err
	}
	a.epochs[set.Epoch] = set
	a.anchors[set.Epoch] = anchor
	for _, m := range set.Members {
		if _, ok := a.members[m.ID]; !ok {
			a.members[m.ID] = &memberState{heights: map[uint64]*heightState{}}
		}
	}
	if a.store != nil {
		if err := a.store.appendEpoch(set, anchor); err != nil {
			return err
		}
	}
	return nil
}

// IsFrozen reports the immutable freeze status of a member.
func (a *Auditor) IsFrozen(memberID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	ms, ok := a.members[memberID]
	return ok && ms.frozen
}

// FrozenMembers lists every convicted member.
func (a *Auditor) FrozenMembers() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for id, ms := range a.members {
		if ms.frozen {
			out = append(out, id)
		}
	}
	return out
}

// Observe validates and applies one signed share.
func (a *Auditor) Observe(m Message) Result {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.observeLocked(m, true)
}

func (a *Auditor) observeLocked(m Message, persist bool) Result {
	id := MessageID(m)
	if a.seen[id] {
		return Result{Verdict: VerdictDuplicate}
	}
	if err := m.WellFormed(a.chainID); err != nil {
		return Result{Verdict: VerdictRejected, Reason: err.Error()}
	}

	set, ok := a.epochs[m.Epoch]
	if !ok {
		return Result{Verdict: VerdictRejected, Reason: "unknown epoch: share must declare its epoch explicitly"}
	}
	member, ok := set.member(m.MemberID)
	if !ok {
		return Result{Verdict: VerdictRejected, Reason: "member not in epoch set"}
	}

	ms := a.members[m.MemberID]
	if ms.frozen {
		return Result{Verdict: VerdictFrozen, Reason: "member convicted and frozen"}
	}
	if !Verify(member.PubKey, a.chainID, m) {
		return Result{Verdict: VerdictRejected, Reason: "signature verification failed"}
	}

	a.seen[id] = true
	if persist && a.store != nil {
		if err := a.store.appendMessage(m); err != nil {
			delete(a.seen, id)
			return Result{Verdict: VerdictRejected, Reason: err.Error()}
		}
	}

	res := a.apply(m, ms, set)
	if res.Verdict == VerdictStaged {
		a.staged = append(a.staged, m)
	}
	a.pump(m.MemberID)
	return res
}

func (a *Auditor) height(ms *memberState, h uint64) *heightState {
	hs, ok := ms.heights[h]
	if !ok {
		hs = newHeightState()
		ms.heights[h] = hs
	}
	return hs
}

func (a *Auditor) stage(reason string) Result {
	return Result{Verdict: VerdictStaged, Reason: reason}
}

func (a *Auditor) evidence(set EpochSet, member string, v Violation, msgs ...Message) *Evidence {
	return &Evidence{
		ChainID:   a.chainID,
		Violation: v,
		MemberID:  member,
		EpochSet:  set,
		Anchor:    a.anchors[set.Epoch],
		Messages:  msgs,
	}
}
