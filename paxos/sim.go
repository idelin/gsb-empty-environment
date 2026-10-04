package paxos

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Envelope is an in-flight message. IDs are assigned in send order and are
// referenced by explicit deliver/drop/dup events.
type Envelope struct {
	ID     int
	Msg    Message
	SentAt int
}

// Sim is the discrete-event environment: an explicit integer tick clock, a
// pending-message queue, and scripted events. Nothing is delivered,
// dropped, duplicated, crashed or restarted unless an event says so.
type Sim struct {
	nodes   [NumNodes]*Node
	disks   [NumNodes]*Disk
	pending []Envelope
	nextID  int
	tick    int
	checker *Checker
	log     []string
}

func NewSim() *Sim {
	s := &Sim{checker: NewChecker()}
	for i := 0; i < NumNodes; i++ {
		s.disks[i] = NewDisk()
		s.nodes[i] = newNode(i, s, s.disks[i])
	}
	return s
}

func (s *Sim) Checker() *Checker { return s.checker }

func (s *Sim) Node(i int) *Node { return s.nodes[i] }

func (s *Sim) Log() []string { return append([]string(nil), s.log...) }

func (s *Sim) logf(format string, args ...interface{}) {
	s.log = append(s.log, fmt.Sprintf(format, args...))
}

// Tick advances the clock; ticks must be non-decreasing.
func (s *Sim) Tick(t int) {
	if t < s.tick {
		panic(fmt.Sprintf("tick went backwards: %d < %d", t, s.tick))
	}
	s.tick = t
}

func (s *Sim) send(m Message) {
	s.nextID++
	s.pending = append(s.pending, Envelope{ID: s.nextID, Msg: m, SentAt: s.tick})
	s.logf("tick=%d send #%d %s", s.tick, s.nextID, m.String())
}

// Pending returns a snapshot of the in-flight messages.
func (s *Sim) Pending() []Envelope { return append([]Envelope(nil), s.pending...) }

// Inject enqueues a crafted message (used to inject stale-ballot traffic).
func (s *Sim) Inject(m Message) { s.send(m) }

// Deliver delivers one pending message by ID.
func (s *Sim) Deliver(id int) bool {
	for i, e := range s.pending {
		if e.ID == id {
			s.pending = append(s.pending[:i], s.pending[i+1:]...)
			s.logf("tick=%d deliver #%d %s", s.tick, id, e.Msg.String())
			s.nodes[e.Msg.To].Deliver(e.Msg)
			return true
		}
	}
	return false
}

// Drop discards one pending message by ID.
func (s *Sim) Drop(id int) bool {
	for i, e := range s.pending {
		if e.ID == id {
			s.pending = append(s.pending[:i], s.pending[i+1:]...)
			s.logf("tick=%d drop #%d %s", s.tick, id, e.Msg.String())
			return true
		}
	}
	return false
}

// Duplicate enqueues a copy of a pending message and returns its new ID.
func (s *Sim) Duplicate(id int) int {
	for _, e := range s.pending {
		if e.ID == id {
			s.nextID++
			s.pending = append(s.pending, Envelope{ID: s.nextID, Msg: e.Msg, SentAt: s.tick})
			s.logf("tick=%d dup #%d -> #%d %s", s.tick, id, s.nextID, e.Msg.String())
			return s.nextID
		}
	}
	return -1
}

// DeliverAll drains the queue FIFO, including messages generated while
// draining. It terminates because nodes never retransmit on their own.
func (s *Sim) DeliverAll() {
	for len(s.pending) > 0 {
		e := s.pending[0]
		s.pending = s.pending[1:]
		s.logf("tick=%d deliver #%d %s", s.tick, e.ID, e.Msg.String())
		s.nodes[e.Msg.To].Deliver(e.Msg)
	}
}

// DropAll discards every pending message.
func (s *Sim) DropAll() {
	for _, e := range s.pending {
		s.logf("tick=%d drop #%d %s", s.tick, e.ID, e.Msg.String())
	}
	s.pending = nil
}

func (s *Sim) Campaign(n int) { s.nodes[n].Campaign() }
func (s *Sim) Propose(n int, v string) {
	s.logf("tick=%d client proposes %s on node=%d", s.tick, printable(v), n)
	s.nodes[n].Propose(v)
}
func (s *Sim) Crash(n int)   { s.nodes[n].Crash() }
func (s *Sim) Restart(n int) { s.nodes[n].Restart() }

func (s *Sim) DiskFail(n int) {
	s.disks[n].SetFault(true)
	s.logf("tick=%d node=%d disk fault injected", s.tick, n)
}

func (s *Sim) DiskHeal(n int) {
	s.disks[n].SetFault(false)
	s.logf("tick=%d node=%d disk fault healed", s.tick, n)
}

// Report renders chosen evidence, per-node state and checker verdicts.
func (s *Sim) Report() string {
	var b strings.Builder
	b.WriteString("== chosen values (independent checker evidence) ==\n")
	slots := s.checker.ChosenSlots()
	if len(slots) == 0 {
		b.WriteString("(none)\n")
	}
	for _, slot := range slots {
		v, accs := s.checker.Evidence(slot)
		fmt.Fprintf(&b, "slot %d: %s chosen; accepted by majority %v\n", slot, printable(v), accs)
	}
	b.WriteString("== node states ==\n")
	for i := 0; i < NumNodes; i++ {
		b.WriteString(s.nodes[i].State())
	}
	fmt.Fprintf(&b, "== pending messages: %d ==\n", len(s.pending))
	if vs := s.checker.Violations(); len(vs) > 0 {
		b.WriteString("== SAFETY VIOLATIONS ==\n")
		for _, v := range vs {
			fmt.Fprintf(&b, "  %s\n", v)
		}
	} else {
		b.WriteString("== safety checker: no violations ==\n")
	}
	return b.String()
}

func printable(v string) string {
	if v == NoOp {
		return "no-op"
	}
	return strconv.Quote(v)
}

func printableSlice(vs []string) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = printable(v)
	}
	return strings.Join(parts, " ")
}

func sortedKeys[V any](m map[int]V) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}
