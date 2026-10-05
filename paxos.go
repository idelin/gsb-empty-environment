// Package paxos implements the core of a fixed-membership Multi-Paxos
// simulation (5 nodes, majority 3) using only the Go standard library.
//
// Ballots are (round, nodeID) pairs under total order. Acceptors durably
// persist the highest promised ballot and, per slot, the highest accepted
// ballot/value. A new leader finishes phase 1 after 3 promises, adopting
// per slot the value of the highest accepted ballot seen and filling slots
// without any accepted value with no-op. A slot is chosen once a majority
// has accepted the same (ballot, value); the application only advances
// over the contiguous chosen prefix.
package paxos

import (
	"fmt"
	"sort"
	"strings"
)

// Ballot is a totally ordered (round, nodeID) pair.
type Ballot struct {
	Round int
	ID    int
}

// Compare returns -1, 0 or +1 as b is less than, equal to or greater than o.
func (b Ballot) Compare(o Ballot) int {
	if b.Round != o.Round {
		if b.Round < o.Round {
			return -1
		}
		return 1
	}
	switch {
	case b.ID < o.ID:
		return -1
	case b.ID > o.ID:
		return 1
	}
	return 0
}

func (b Ballot) String() string { return fmt.Sprintf("(%d,%d)", b.Round, b.ID) }

// Value is a proposed command. The empty value is the no-op.
type Value string

// Noop fills slots that no acceptor has accepted a value for when a new
// leader finishes phase 1.
const Noop Value = ""

func formatValue(v Value) string {
	if v == Noop {
		return "noop"
	}
	return fmt.Sprintf("%q", string(v))
}

// Accepted records the ballot at which an acceptor accepted a slot's value.
type Accepted struct {
	Ballot Ballot
	Value  Value
}

// MsgKind enumerates the four Paxos message types.
type MsgKind int

const (
	MsgPrepare MsgKind = iota
	MsgPromise
	MsgAccept
	MsgAccepted
)

func (k MsgKind) String() string {
	switch k {
	case MsgPrepare:
		return "Prepare"
	case MsgPromise:
		return "Promise"
	case MsgAccept:
		return "Accept"
	case MsgAccepted:
		return "Accepted"
	}
	return "?"
}

// Message is a single Paxos protocol message. Accepted is only set for
// MsgPromise and carries the acceptor's full accepted table.
type Message struct {
	Kind     MsgKind
	Ballot   Ballot
	Slot     int
	Value    Value
	Accepted map[int]Accepted
}

func (m Message) String() string {
	switch m.Kind {
	case MsgPrepare:
		return fmt.Sprintf("Prepare(b=%s)", m.Ballot)
	case MsgPromise:
		return fmt.Sprintf("Promise(b=%s, accepted=%s)", m.Ballot, formatAccepted(m.Accepted))
	case MsgAccept:
		return fmt.Sprintf("Accept(b=%s, slot=%d, v=%s)", m.Ballot, m.Slot, formatValue(m.Value))
	case MsgAccepted:
		return fmt.Sprintf("Accepted(b=%s, slot=%d, v=%s)", m.Ballot, m.Slot, formatValue(m.Value))
	}
	return "?"
}

func formatAccepted(m map[int]Accepted) string {
	if len(m) == 0 {
		return "{}"
	}
	slots := make([]int, 0, len(m))
	for s := range m {
		slots = append(slots, s)
	}
	sort.Ints(slots)
	var sb strings.Builder
	sb.WriteString("{")
	for i, s := range slots {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "%d:%s/%s", s, m[s].Ballot, formatValue(m[s].Value))
	}
	sb.WriteString("}")
	return sb.String()
}

func formatChosen(m map[int]Value) string {
	if len(m) == 0 {
		return "{}"
	}
	slots := make([]int, 0, len(m))
	for s := range m {
		slots = append(slots, s)
	}
	sort.Ints(slots)
	var sb strings.Builder
	sb.WriteString("{")
	for i, s := range slots {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "%d:%s", s, formatValue(m[s]))
	}
	sb.WriteString("}")
	return sb.String()
}

func formatApplied(a []Value) string {
	parts := make([]string, len(a))
	for i, v := range a {
		parts[i] = formatValue(v)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// PersistentState is the acceptor's durable state. Promised and Accepted
// are required for safety; Chosen is the decided log kept so a restarted
// node can rebuild its applied prefix.
type PersistentState struct {
	Promised Ballot
	Accepted map[int]Accepted
	Chosen   map[int]Value
}

func cloneAccepted(m map[int]Accepted) map[int]Accepted {
	out := make(map[int]Accepted, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneChosen(m map[int]Value) map[int]Value {
	out := make(map[int]Value, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (s PersistentState) clone() PersistentState {
	return PersistentState{
		Promised: s.Promised,
		Accepted: cloneAccepted(s.Accepted),
		Chosen:   cloneChosen(s.Chosen),
	}
}

// Store durably persists acceptor state across crashes.
type Store interface {
	Load() (PersistentState, error)
	Save(PersistentState) error
}

// MemStore is an in-memory Store. SetFail simulates persistence failures:
// Save starts returning an error, and a correct node must then refuse to
// answer protocol messages rather than risk forgetting a promise.
type MemStore struct {
	state PersistentState
	fail  bool
}

func NewMemStore() *MemStore {
	return &MemStore{state: PersistentState{
		Accepted: map[int]Accepted{},
		Chosen:   map[int]Value{},
	}}
}

func (m *MemStore) Load() (PersistentState, error) { return m.state.clone(), nil }

func (m *MemStore) Save(s PersistentState) error {
	if m.fail {
		return fmt.Errorf("simulated persistence failure")
	}
	m.state = s.clone()
	return nil
}

// SetFail toggles simulated persistence failure.
func (m *MemStore) SetFail(f bool) { m.fail = f }

// Outbound is a message a node wants to send. To == -1 means broadcast.
type Outbound struct {
	To  int
	Msg Message
}

// Node is one cluster member: a durable acceptor plus a volatile leader.
type Node struct {
	ID int
	N  int
	Up bool

	store Store

	// Durable acceptor state (mirrored in store).
	promised Ballot
	accepted map[int]Accepted
	chosen   map[int]Value

	// Volatile state, lost on crash.
	maxRound   int
	leader     bool
	ballot     Ballot
	promises   map[int]Message
	phase1Done bool
	pending    []Value // client values waiting for phase 1
	nextSlot   int
	accepts    map[int]map[Value]map[int]bool // slot -> value -> acceptors
	applied    []Value
}

// NewNode creates a node, loading any durable state from the store.
func NewNode(id, n int, store Store) *Node {
	nd := &Node{ID: id, N: n, Up: true, store: store}
	nd.loadFromStore()
	return nd
}

func (n *Node) majority() int { return n.N/2 + 1 }

func (n *Node) loadFromStore() {
	st, err := n.store.Load()
	if err != nil {
		st = PersistentState{Accepted: map[int]Accepted{}, Chosen: map[int]Value{}}
	}
	n.promised = st.Promised
	n.accepted = st.Accepted
	n.chosen = st.Chosen
	n.leader = false
	n.phase1Done = false
	n.promises = map[int]Message{}
	n.pending = nil
	n.nextSlot = 0
	n.accepts = map[int]map[Value]map[int]bool{}
	n.applied = nil
	n.extendApplied()
	n.maxRound = n.promised.Round
	for _, a := range n.accepted {
		if a.Ballot.Round > n.maxRound {
			n.maxRound = a.Ballot.Round
		}
	}
}

// save persists the durable state. On failure the caller must roll back
// its in-memory change and stay silent.
func (n *Node) save() bool {
	return n.store.Save(PersistentState{
		Promised: n.promised,
		Accepted: n.accepted,
		Chosen:   n.chosen,
	}) == nil
}

func (n *Node) trackBallot(b Ballot) {
	if b.Round > n.maxRound {
		n.maxRound = b.Round
	}
}

// StartLeader begins phase 1 with a fresh, strictly higher ballot.
func (n *Node) StartLeader() []Outbound {
	if !n.Up {
		return nil
	}
	n.maxRound++
	n.ballot = Ballot{Round: n.maxRound, ID: n.ID}
	n.leader = true
	n.phase1Done = false
	n.promises = map[int]Message{}
	n.accepts = map[int]map[Value]map[int]bool{}
	n.pending = nil
	return []Outbound{{To: -1, Msg: Message{Kind: MsgPrepare, Ballot: n.ballot}}}
}

// Propose asks the leader to propose v at the next free slot. Before phase
// 1 completes the value is buffered; afterwards it is proposed directly.
func (n *Node) Propose(v Value) []Outbound {
	if !n.Up || !n.leader {
		return nil
	}
	if !n.phase1Done {
		n.pending = append(n.pending, v)
		return nil
	}
	return n.proposeLocked(v)
}

func (n *Node) proposeLocked(v Value) []Outbound {
	slot := n.nextSlot
	n.nextSlot++
	return []Outbound{{To: -1, Msg: Message{Kind: MsgAccept, Ballot: n.ballot, Slot: slot, Value: v}}}
}

// Handle processes one inbound message and returns messages to send.
func (n *Node) Handle(from int, m Message) []Outbound {
	if !n.Up {
		return nil
	}
	switch m.Kind {
	case MsgPrepare:
		return n.onPrepare(from, m)
	case MsgPromise:
		return n.onPromise(from, m)
	case MsgAccept:
		return n.onAccept(from, m)
	case MsgAccepted:
		return n.onAccepted(from, m)
	}
	return nil
}

func (n *Node) onPrepare(from int, m Message) []Outbound {
	n.trackBallot(m.Ballot)
	if m.Ballot.Compare(n.promised) < 0 {
		return nil // stale ballot: reject
	}
	old := n.promised
	n.promised = m.Ballot
	if !n.save() {
		n.promised = old
		return nil
	}
	return []Outbound{{To: from, Msg: Message{
		Kind:     MsgPromise,
		Ballot:   m.Ballot,
		Accepted: cloneAccepted(n.accepted),
	}}}
}

func (n *Node) onPromise(from int, m Message) []Outbound {
	if !n.leader || n.phase1Done || m.Ballot != n.ballot {
		return nil
	}
	n.promises[from] = m
	if len(n.promises) < n.majority() {
		return nil
	}
	// Phase 1 complete for this ballot: adopt, per slot, the value of the
	// highest accepted ballot reported by the quorum; fill slots without
	// any accepted value with no-op.
	n.phase1Done = true
	merged := map[int]Accepted{}
	maxSlot := -1
	for _, p := range n.promises {
		for slot, acc := range p.Accepted {
			if cur, ok := merged[slot]; !ok || acc.Ballot.Compare(cur.Ballot) > 0 {
				merged[slot] = acc
			}
			if slot > maxSlot {
				maxSlot = slot
			}
		}
	}
	var out []Outbound
	for slot := 0; slot <= maxSlot; slot++ {
		v := Noop
		if acc, ok := merged[slot]; ok {
			v = acc.Value
		}
		out = append(out, Outbound{To: -1, Msg: Message{Kind: MsgAccept, Ballot: n.ballot, Slot: slot, Value: v}})
	}
	n.nextSlot = maxSlot + 1
	for _, v := range n.pending {
		out = append(out, n.proposeLocked(v)...)
	}
	n.pending = nil
	return out
}

func (n *Node) onAccept(from int, m Message) []Outbound {
	n.trackBallot(m.Ballot)
	if m.Ballot.Compare(n.promised) < 0 {
		return nil // stale ballot: reject
	}
	oldPromised := n.promised
	oldAccepted, had := n.accepted[m.Slot]
	n.promised = m.Ballot
	n.accepted[m.Slot] = Accepted{Ballot: m.Ballot, Value: m.Value}
	if !n.save() {
		n.promised = oldPromised
		if had {
			n.accepted[m.Slot] = oldAccepted
		} else {
			delete(n.accepted, m.Slot)
		}
		return nil
	}
	return []Outbound{{To: from, Msg: Message{Kind: MsgAccepted, Ballot: m.Ballot, Slot: m.Slot, Value: m.Value}}}
}

func (n *Node) onAccepted(from int, m Message) []Outbound {
	if !n.leader || m.Ballot != n.ballot {
		return nil
	}
	if _, done := n.chosen[m.Slot]; done {
		return nil
	}
	byValue, ok := n.accepts[m.Slot]
	if !ok {
		byValue = map[Value]map[int]bool{}
		n.accepts[m.Slot] = byValue
	}
	set, ok := byValue[m.Value]
	if !ok {
		set = map[int]bool{}
		byValue[m.Value] = set
	}
	set[from] = true
	if len(set) >= n.majority() {
		n.chosen[m.Slot] = m.Value
		n.save() // best effort: chosen is also re-learnable
		n.extendApplied()
	}
	return nil
}

// extendApplied advances the application over the contiguous chosen prefix.
func (n *Node) extendApplied() {
	for {
		v, ok := n.chosen[len(n.applied)]
		if !ok {
			return
		}
		n.applied = append(n.applied, v)
	}
}

// Crash halts the node; all volatile state is lost.
func (n *Node) Crash() { n.Up = false }

// Restart brings the node back, reloading durable state from the store.
func (n *Node) Restart() {
	n.Up = true
	n.loadFromStore()
}

// Accessors for simulation output and tests.

func (n *Node) Promised() Ballot              { return n.promised }
func (n *Node) AcceptedMap() map[int]Accepted { return cloneAccepted(n.accepted) }
func (n *Node) ChosenMap() map[int]Value      { return cloneChosen(n.chosen) }
func (n *Node) Applied() []Value              { return append([]Value(nil), n.applied...) }
func (n *Node) IsLeader() bool                { return n.leader }
func (n *Node) Phase1Done() bool              { return n.phase1Done }
func (n *Node) Ballot() Ballot                { return n.ballot }

// StateString renders the node's state for simulation output.
func (n *Node) StateString() string {
	state := "up"
	if !n.Up {
		state = "down"
	}
	return fmt.Sprintf("node %d %-4s promised=%s accepted=%s chosen=%s applied=%s leader=%v phase1Done=%v",
		n.ID, state, n.promised, formatAccepted(n.accepted), formatChosen(n.chosen),
		formatApplied(n.applied), n.leader, n.phase1Done)
}
