package agg

import (
	"fmt"
	"hash/fnv"
	"sort"
)

// State is a pure function of the event log.
type State struct {
	CurrentGen uint64
	Members    map[string]bool
	ResetOpen  bool            // a reset barrier is in flight
	Crossed    map[string]bool // partitions cached as arrived at the barrier
	OldAcc     int64           // accumulator for the current (closing) generation
	NewAcc     int64           // buffered events already tagged with the next generation
	Finals     map[uint64]int64
}

func newState() State {
	return State{
		Members: make(map[string]bool),
		Crossed: make(map[string]bool),
		Finals:  make(map[uint64]int64),
	}
}

// applyEvent folds one event into the state. It is the single place
// where state changes happen, used both by the live aggregator and by
// Replay, so recovery is correct by construction.
func applyEvent(s *State, ev Event) {
	switch ev.Kind {
	case KindPartitionJoined:
		s.Members[ev.Partition] = true
	case KindPartitionExited:
		delete(s.Members, ev.Partition)
		delete(s.Crossed, ev.Partition)
	case KindResetIssued:
		s.ResetOpen = true
		s.Crossed = make(map[string]bool)
	case KindDataAccepted:
		if ev.Gen == s.CurrentGen {
			s.OldAcc += ev.Value
		} else {
			s.NewAcc += ev.Value
		}
	case KindBarrierCrossed:
		s.Crossed[ev.Partition] = true
	case KindGenFinalized:
		s.Finals[ev.Gen] = ev.Value
	case KindGenRolled:
		s.CurrentGen = ev.Gen
		s.OldAcc = ev.Value // carried from the buffered new-generation events
		s.NewAcc = 0
		s.Crossed = make(map[string]bool)
		s.ResetOpen = false
	}
	// Ignored / dropped kinds are audit-only: no state change.
}

// Replay rebuilds the state from a log prefix. This is recovery.
func Replay(log []Event) State {
	s := newState()
	for _, ev := range log {
		applyEvent(&s, ev)
	}
	return s
}

// Aggregator is the live state machine. It appends every decision to
// its log and folds it into its state via applyEvent.
type Aggregator struct {
	state State
	log   []Event
}

func New() *Aggregator {
	return &Aggregator{state: newState()}
}

// Restore recovers an aggregator from a persisted log (crash recovery).
func Restore(log []Event) *Aggregator {
	cp := make([]Event, len(log))
	copy(cp, log)
	return &Aggregator{state: Replay(cp), log: cp}
}

func (a *Aggregator) State() State { return a.state }
func (a *Aggregator) Log() []Event { return a.log }

func (a *Aggregator) emit(tag Tag, kind Kind, partition string, gen uint64, value int64, note string, members []string) Event {
	ev := Event{
		Seq:       uint64(len(a.log)),
		Tag:       tag,
		Kind:      kind,
		Partition: partition,
		Gen:       gen,
		Value:     value,
		Note:      note,
		Members:   members,
	}
	a.log = append(a.log, ev)
	applyEvent(&a.state, ev)
	return ev
}

// Join registers a partition.
func (a *Aggregator) Join(p string) bool {
	if a.state.Members[p] {
		return false
	}
	a.emit(TagMembership, KindPartitionJoined, p, a.state.CurrentGen, 0, "", nil)
	return true
}

// Exit deregisters a partition. If a reset is in flight the partition
// is dropped from the pending barrier set, which may complete it.
func (a *Aggregator) Exit(p string) bool {
	if !a.state.Members[p] {
		return false
	}
	a.emit(TagMembership, KindPartitionExited, p, a.state.CurrentGen, 0, "", nil)
	a.maybeFinalize()
	return true
}

// IssueReset broadcasts a reset barrier for the current generation.
func (a *Aggregator) IssueReset() bool {
	s := &a.state
	if s.ResetOpen {
		a.emit(TagControl, KindResetIgnored, "", s.CurrentGen, 0, "reset-in-flight", nil)
		return false
	}
	if len(s.Members) == 0 {
		a.emit(TagControl, KindResetIgnored, "", s.CurrentGen, 0, "no-members", nil)
		return false
	}
	a.emit(TagControl, KindResetIssued, "", s.CurrentGen, 0, "", sortedKeys(s.Members))
	return true
}

// SubmitData ingests one partition event tagged with the generation the
// partition believes it belongs to. Before the partition crosses the
// barrier its events count toward the old generation; after crossing,
// toward the new one.
func (a *Aggregator) SubmitData(p string, gen uint64, value int64) {
	s := &a.state
	switch {
	case !s.Members[p]:
		a.emit(TagData, KindDataDropped, p, gen, value, "not-a-member", nil)
	case gen == s.CurrentGen && !s.Crossed[p]:
		a.emit(TagData, KindDataAccepted, p, gen, value, "old-generation", nil)
	case gen == s.CurrentGen+1 && s.Crossed[p]:
		a.emit(TagData, KindDataAccepted, p, gen, value, "new-generation", nil)
	default:
		a.emit(TagData, KindDataDropped, p, gen, value, "generation-mismatch", nil)
	}
}

// BarrierAck records that a partition arrived at the reset barrier.
// Arrivals are cached per partition; duplicates and stale arrivals are
// ignored. When every live member has arrived, the old generation's
// final value is published and the generation rolls over.
func (a *Aggregator) BarrierAck(p string, gen uint64) {
	s := &a.state
	switch {
	case !s.ResetOpen || gen != s.CurrentGen:
		a.emit(TagBarrier, KindBarrierIgnored, p, gen, 0, "stale-or-no-reset", nil)
	case !s.Members[p]:
		a.emit(TagBarrier, KindBarrierIgnored, p, gen, 0, "not-a-member", nil)
	case s.Crossed[p]:
		a.emit(TagBarrier, KindBarrierIgnored, p, gen, 0, "duplicate", nil)
	default:
		a.emit(TagBarrier, KindBarrierCrossed, p, gen, 0, "", nil)
		a.maybeFinalize()
	}
}

// maybeFinalize publishes the old generation's final value once every
// live member has crossed the barrier.
func (a *Aggregator) maybeFinalize() {
	s := &a.state
	if !s.ResetOpen || len(s.Members) == 0 {
		return
	}
	for m := range s.Members {
		if !s.Crossed[m] {
			return
		}
	}
	g := s.CurrentGen
	a.emit(TagPublish, KindGenFinalized, "", g, s.OldAcc, "", nil)
	a.emit(TagControl, KindGenRolled, "", g+1, s.NewAcc, "", nil)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Hash is a deterministic fingerprint of the state, used to assert
// that a recovered state equals the live one.
func (s State) Hash() uint64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "gen=%d old=%d new=%d open=%v\n", s.CurrentGen, s.OldAcc, s.NewAcc, s.ResetOpen)
	for _, m := range sortedKeys(s.Members) {
		fmt.Fprintf(h, "member %s\n", m)
	}
	for _, c := range sortedKeys(s.Crossed) {
		fmt.Fprintf(h, "crossed %s\n", c)
	}
	gens := make([]uint64, 0, len(s.Finals))
	for g := range s.Finals {
		gens = append(gens, g)
	}
	sort.Slice(gens, func(i, j int) bool { return gens[i] < gens[j] })
	for _, g := range gens {
		fmt.Fprintf(h, "final %d=%d\n", g, s.Finals[g])
	}
	return h.Sum64()
}
