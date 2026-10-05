// Package barrier implements a distributed barrier simulator.
//
// Members are frozen into the barrier at the start of each round. Members
// that join mid-round wait for the next round; members that leave mid-round
// must have an exemption record. A round completes when every frozen member
// has a disposition (arrived or exempted), at which point a completion
// certificate listing the disposition of every frozen member is issued and
// the next round begins. Duplicate and stale-round messages have no side
// effects.
package barrier

import "sync"

// Disposition records how a frozen member was accounted for in a round.
type Disposition string

const (
	DispositionArrived  Disposition = "arrived"
	DispositionExempted Disposition = "exempted"
)

// ArrivalProof is issued when a frozen member arrives at the barrier.
type ArrivalProof struct {
	Round  int
	Member string
	Seq    uint64
}

// Exemption records that a frozen member is excused from a round.
type Exemption struct {
	Round  int
	Member string
	Reason string
	Seq    uint64
}

// Certificate is issued exactly once per completed round. Dispositions
// lists the fate of every member frozen at the start of the round.
type Certificate struct {
	Round        int
	Dispositions map[string]Disposition
	Arrivals     map[string]ArrivalProof
	Exemptions   map[string]Exemption
}

// Barrier is a round-based distributed barrier simulator. It is safe for
// concurrent use; races between arrival and exemption for the same member
// are resolved first-one-wins.
type Barrier struct {
	mu         sync.Mutex
	round      int
	frozen     map[string]struct{}
	arrivals   map[string]ArrivalProof
	exemptions map[string]Exemption
	joinQueue  []string
	joinSet    map[string]struct{}
	leaves     map[string]struct{}
	seq        uint64
	completed  bool
	certs      []Certificate
}

// NewBarrier creates a barrier whose round 1 freezes the given members.
func NewBarrier(members ...string) *Barrier {
	b := &Barrier{
		round:      1,
		frozen:     make(map[string]struct{}),
		arrivals:   make(map[string]ArrivalProof),
		exemptions: make(map[string]Exemption),
		joinSet:    make(map[string]struct{}),
		leaves:     make(map[string]struct{}),
	}
	for _, m := range members {
		b.frozen[m] = struct{}{}
	}
	return b
}

// Round returns the current round number.
func (b *Barrier) Round() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.round
}

// Frozen reports whether member is frozen into the current round.
func (b *Barrier) Frozen(member string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.frozen[member]
	return ok
}

// Certificates returns a copy of all completion certificates issued so far.
func (b *Barrier) Certificates() []Certificate {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Certificate, len(b.certs))
	copy(out, b.certs)
	return out
}

// Join registers a new member. The member is not frozen into the current
// round; it takes effect at the start of the next round. Returns the round
// in which the member will first be frozen.
func (b *Barrier) Join(member string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.frozen[member]; ok {
		return b.round
	}
	if _, ok := b.joinSet[member]; ok {
		return b.round + 1
	}
	b.joinQueue = append(b.joinQueue, member)
	b.joinSet[member] = struct{}{}
	effective := b.round + 1
	b.maybeCompleteLocked()
	return effective
}

// Arrive records member's arrival for the given round. It returns the
// arrival proof and whether this call created it. Messages for a stale or
// future round, for non-frozen members, and duplicate arrivals have no
// side effects; a duplicate returns the existing proof with created=false.
// If an exemption for this member already won the race, arrival is
// rejected.
func (b *Barrier) Arrive(round int, member string) (*ArrivalProof, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if round != b.round || b.completed {
		return nil, false
	}
	if _, ok := b.frozen[member]; !ok {
		return nil, false
	}
	if _, ok := b.exemptions[member]; ok {
		return nil, false
	}
	if p, ok := b.arrivals[member]; ok {
		dup := p
		return &dup, false
	}
	b.seq++
	p := ArrivalProof{Round: round, Member: member, Seq: b.seq}
	b.arrivals[member] = p
	b.maybeCompleteLocked()
	return &p, true
}

// Exempt records an exemption for member in the given round. It returns
// the exemption record and whether this call created it. Stale or future
// rounds, non-frozen members, and duplicate exemptions have no side
// effects. If the member already arrived, the exemption is rejected:
// first one wins.
func (b *Barrier) Exempt(round int, member, reason string) (*Exemption, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if round != b.round || b.completed {
		return nil, false
	}
	if _, ok := b.frozen[member]; !ok {
		return nil, false
	}
	if _, ok := b.arrivals[member]; ok {
		return nil, false
	}
	if e, ok := b.exemptions[member]; ok {
		dup := e
		return &dup, false
	}
	b.seq++
	e := Exemption{Round: round, Member: member, Reason: reason, Seq: b.seq}
	b.exemptions[member] = e
	b.maybeCompleteLocked()
	return &e, true
}

// Leave removes member from the barrier at the start of the next round.
// If the member is frozen into the current round and has not yet arrived,
// an exemption record is created for it: exiting members must have an
// exemption record. If the member is only queued to join, it is removed
// from the join queue instead.
func (b *Barrier) Leave(member string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.joinSet[member]; ok {
		delete(b.joinSet, member)
		for i, m := range b.joinQueue {
			if m == member {
				b.joinQueue = append(b.joinQueue[:i], b.joinQueue[i+1:]...)
				break
			}
		}
	}
	if _, ok := b.frozen[member]; ok {
		if _, arrived := b.arrivals[member]; !arrived && !b.completed {
			if _, exempted := b.exemptions[member]; !exempted {
				b.seq++
				b.exemptions[member] = Exemption{
					Round:  b.round,
					Member: member,
					Reason: "leave: member exit",
					Seq:    b.seq,
				}
			}
		}
		b.leaves[member] = struct{}{}
	}
	b.maybeCompleteLocked()
}

// maybeCompleteLocked issues the completion certificate and advances the
// round when every frozen member has a disposition. The caller must hold
// b.mu.
func (b *Barrier) maybeCompleteLocked() {
	if b.completed {
		return
	}
	for m := range b.frozen {
		if _, ok := b.arrivals[m]; ok {
			continue
		}
		if _, ok := b.exemptions[m]; ok {
			continue
		}
		return
	}
	cert := Certificate{
		Round:        b.round,
		Dispositions: make(map[string]Disposition, len(b.frozen)),
		Arrivals:     make(map[string]ArrivalProof, len(b.arrivals)),
		Exemptions:   make(map[string]Exemption, len(b.exemptions)),
	}
	for m := range b.frozen {
		if p, ok := b.arrivals[m]; ok {
			cert.Dispositions[m] = DispositionArrived
			cert.Arrivals[m] = p
		} else {
			cert.Dispositions[m] = DispositionExempted
			cert.Exemptions[m] = b.exemptions[m]
		}
	}
	b.certs = append(b.certs, cert)
	b.completed = true
	b.advanceLocked()
}

// advanceLocked starts the next round: queued joins and recorded leaves
// take effect and a fresh member set is frozen. The caller must hold b.mu.
func (b *Barrier) advanceLocked() {
	next := make(map[string]struct{}, len(b.frozen)+len(b.joinQueue))
	for m := range b.frozen {
		if _, gone := b.leaves[m]; !gone {
			next[m] = struct{}{}
		}
	}
	for _, m := range b.joinQueue {
		if _, gone := b.leaves[m]; !gone {
			next[m] = struct{}{}
		}
	}
	b.round++
	b.frozen = next
	b.arrivals = make(map[string]ArrivalProof)
	b.exemptions = make(map[string]Exemption)
	b.joinQueue = nil
	b.joinSet = make(map[string]struct{})
	b.leaves = make(map[string]struct{})
	b.completed = false
}
