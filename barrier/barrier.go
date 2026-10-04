// Package barrier implements a distributed barrier simulator.
//
// At the start of every round the current membership is frozen: members that
// join afterwards wait for the next round, and members that leave a frozen
// round must hold an exemption record. A round completes when every frozen
// member has either arrived (with an arrival proof) or been exempted. The
// completion certificate lists the final disposition of every frozen member.
// Duplicate and stale-round messages are idempotent no-ops.
package barrier

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Disposition is the final state of a frozen member within a round.
type Disposition int

const (
	// Pending means the member has neither arrived nor been exempted yet.
	Pending Disposition = iota
	// Arrived means the member checked in and holds an arrival proof.
	Arrived
	// Exempted means the member was released via an exemption record.
	Exempted
)

func (d Disposition) String() string {
	switch d {
	case Arrived:
		return "arrived"
	case Exempted:
		return "exempted"
	default:
		return "pending"
	}
}

var (
	// ErrStaleRound is returned for messages targeting an older round.
	ErrStaleRound = errors.New("barrier: message targets an old round")
	// ErrFutureRound is returned for messages targeting a round that has
	// not started yet.
	ErrFutureRound = errors.New("barrier: message targets a future round")
	// ErrNotFrozen is returned when the member is not part of the frozen
	// set of the current round (e.g. joined mid-round).
	ErrNotFrozen = errors.New("barrier: member is not frozen in this round")
	// ErrAlreadyArrived is returned when an exemption loses the race
	// against an earlier arrival.
	ErrAlreadyArrived = errors.New("barrier: member already arrived")
	// ErrAlreadyExempted is returned when an arrival loses the race
	// against an earlier exemption.
	ErrAlreadyExempted = errors.New("barrier: member already exempted")
	// ErrRoundInProgress is returned when starting a new round while the
	// current one is not complete.
	ErrRoundInProgress = errors.New("barrier: current round is not complete")
	// ErrUnknownMember is returned for members that never joined.
	ErrUnknownMember = errors.New("barrier: unknown member")
	// ErrExemptionRequired is returned when leaving a frozen, incomplete
	// round without an exemption record.
	ErrExemptionRequired = errors.New("barrier: frozen member needs an exemption record to leave")
)

// ArrivalProof is the receipt issued when a frozen member arrives.
type ArrivalProof struct {
	Round  uint64
	Member string
	Seq    uint64 // arrival order within the round, 1-based
	Digest string
}

// ExemptionRecord is the receipt issued when a frozen member is exempted.
type ExemptionRecord struct {
	Round  uint64
	Member string
	Reason string
	Seq    uint64 // exemption order within the round, 1-based
	Digest string
}

// MemberOutcome is one frozen member's entry in a completion certificate.
type MemberOutcome struct {
	Member      string
	Disposition Disposition
	Seq         uint64 // arrival/exemption order; 0 if pending (never in a cert)
	Detail      string // arrival digest or exemption reason
}

// Certificate is the completion proof of a round. It lists the disposition
// of every frozen member, sorted by member name.
type Certificate struct {
	Round   uint64
	Entries []MemberOutcome
	Digest  string
}

// Arrived counts entries with disposition Arrived.
func (c Certificate) Arrived() int {
	n := 0
	for _, e := range c.Entries {
		if e.Disposition == Arrived {
			n++
		}
	}
	return n
}

// Exempted counts entries with disposition Exempted.
func (c Certificate) Exempted() int {
	n := 0
	for _, e := range c.Entries {
		if e.Disposition == Exempted {
			n++
		}
	}
	return n
}

type memberState struct {
	disposition Disposition
	seq         uint64
	detail      string // arrival digest or exemption reason
}

// Barrier is the simulator state. It is safe for concurrent use.
type Barrier struct {
	mu sync.Mutex

	round     uint64 // current round; 0 means no round started
	active    bool   // a round is frozen and not yet complete
	members   map[string]bool
	frozen    map[string]*memberState
	arriveSeq uint64
	exemptSeq uint64
	certs     map[uint64]Certificate
}

// New returns an empty barrier with no round started.
func New() *Barrier {
	return &Barrier{
		members: make(map[string]bool),
		certs:   make(map[uint64]Certificate),
	}
}

// Round reports the current round number and whether it is still open.
func (b *Barrier) Round() (round uint64, inProgress bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.round, b.active
}

// Join registers a member. If a round is currently frozen, the member waits
// and only becomes part of the frozen set from the next round on. The
// returned round is the first round the member can participate in.
func (b *Barrier) Join(id string) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.members[id] = true
	if b.active {
		return b.round + 1
	}
	if b.round == 0 {
		return 1
	}
	return b.round
}

// Leave unregisters a member. A member frozen in the current incomplete
// round may only leave if it already holds an exemption record.
func (b *Barrier) Leave(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.members[id] {
		return ErrUnknownMember
	}
	if b.active {
		if st, ok := b.frozen[id]; ok && st.disposition != Exempted {
			return ErrExemptionRequired
		}
	}
	delete(b.members, id)
	return nil
}

// BeginRound freezes the current membership and starts the next round. A
// round with no members completes immediately with an empty certificate.
func (b *Barrier) BeginRound() (uint64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active {
		return b.round, ErrRoundInProgress
	}
	b.round++
	b.frozen = make(map[string]*memberState, len(b.members))
	for id := range b.members {
		b.frozen[id] = &memberState{disposition: Pending}
	}
	b.arriveSeq = 0
	b.exemptSeq = 0
	b.active = true
	b.completeLocked()
	return b.round, nil
}

// Frozen returns the sorted frozen member set of the current round.
func (b *Barrier) Frozen() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.frozen))
	for id := range b.frozen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func digest(parts ...interface{}) string {
	h := sha256.New()
	for _, p := range parts {
		fmt.Fprintf(h, "%v|", p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Arrive records a frozen member's arrival and returns its proof.
//
// A repeated arrival for the same member and round returns the originally
// issued proof without any side effect. Arrivals for old or future rounds,
// and arrivals from members outside the frozen set, are rejected without
// side effects.
func (b *Barrier) Arrive(id string, round uint64) (ArrivalProof, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkRoundLocked(round); err != nil {
		return ArrivalProof{}, err
	}
	st, ok := b.frozen[id]
	if !ok {
		return ArrivalProof{}, ErrNotFrozen
	}
	switch st.disposition {
	case Arrived:
		return ArrivalProof{Round: round, Member: id, Seq: st.seq, Digest: st.detail}, nil
	case Exempted:
		return ArrivalProof{}, ErrAlreadyExempted
	}
	b.arriveSeq++
	st.disposition = Arrived
	st.seq = b.arriveSeq
	st.detail = digest("arrive", round, id, st.seq)
	b.completeLocked()
	return ArrivalProof{Round: round, Member: id, Seq: st.seq, Digest: st.detail}, nil
}

// Exempt records an exemption for a frozen member that exits the round.
//
// A repeated exemption for the same member and round returns the original
// record without any side effect. Exemptions for old or future rounds, for
// members outside the frozen set, or for members that already arrived, are
// rejected without side effects.
func (b *Barrier) Exempt(id string, round uint64, reason string) (ExemptionRecord, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkRoundLocked(round); err != nil {
		return ExemptionRecord{}, err
	}
	st, ok := b.frozen[id]
	if !ok {
		return ExemptionRecord{}, ErrNotFrozen
	}
	switch st.disposition {
	case Exempted:
		return ExemptionRecord{Round: round, Member: id, Reason: st.detail, Seq: st.seq,
			Digest: digest("exempt", round, id, st.seq)}, nil
	case Arrived:
		return ExemptionRecord{}, ErrAlreadyArrived
	}
	b.exemptSeq++
	st.disposition = Exempted
	st.seq = b.exemptSeq
	st.detail = reason
	b.completeLocked()
	return ExemptionRecord{Round: round, Member: id, Reason: reason, Seq: st.seq,
		Digest: digest("exempt", round, id, st.seq)}, nil
}

func (b *Barrier) checkRoundLocked(round uint64) error {
	if round < b.round {
		return ErrStaleRound
	}
	if round > b.round || b.round == 0 {
		return ErrFutureRound
	}
	return nil
}

// completeLocked seals the round once every frozen member has a final
// disposition, and issues the completion certificate.
func (b *Barrier) completeLocked() {
	if !b.active {
		return
	}
	for _, st := range b.frozen {
		if st.disposition == Pending {
			return
		}
	}
	cert := Certificate{Round: b.round, Entries: make([]MemberOutcome, 0, len(b.frozen))}
	for id, st := range b.frozen {
		cert.Entries = append(cert.Entries, MemberOutcome{
			Member:      id,
			Disposition: st.disposition,
			Seq:         st.seq,
			Detail:      st.detail,
		})
	}
	sort.Slice(cert.Entries, func(i, j int) bool {
		return cert.Entries[i].Member < cert.Entries[j].Member
	})
	parts := make([]interface{}, 0, len(cert.Entries)*4+1)
	parts = append(parts, "cert", cert.Round)
	for _, e := range cert.Entries {
		parts = append(parts, e.Member, e.Disposition.String(), e.Seq, e.Detail)
	}
	cert.Digest = digest(parts...)
	b.certs[b.round] = cert
	b.active = false
}

// Certificate returns the completion certificate of a finished round.
func (b *Barrier) Certificate(round uint64) (Certificate, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cert, ok := b.certs[round]
	return cert, ok
}
