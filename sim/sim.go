// Package sim is a single-process, deterministic event simulator used to
// drive OR-Set replicas. Virtual time, message delays, loss, duplication,
// reordering and network partitions are all controlled explicitly by the
// scheduler, so distributed executions are reproducible in `go test`.
package sim

import (
	"container/heap"
	"fmt"
	"math/rand"
	"sort"

	"orsetlab/orset"
)

type event struct {
	time int64
	seq  int
	run  func()
}

// evHeap is a min-heap of events ordered by (time, seq): deterministic.
type evHeap []event

func (h evHeap) Len() int { return len(h) }
func (h evHeap) Less(i, j int) bool {
	return h[i].time < h[j].time || (h[i].time == h[j].time && h[i].seq < h[j].seq)
}
func (h evHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *evHeap) Push(x interface{}) { *h = append(*h, x.(event)) }
func (h *evHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Simulator owns a cluster of OR-Set replicas and an event queue.
type Simulator struct {
	Now      int64
	Replicas map[string]*orset.ORSet

	ids   []string
	queue evHeap
	seq   int
	rng   *rand.Rand

	links map[[2]string]bool // directed link state; absent means up
	Loss  float64            // probability of dropping a message on delivery
	Dup   float64            // probability of duplicating a message

	Log       []string
	Delivered int
	Dropped   int
}

// New creates a simulator with the given replica ids and a seeded RNG.
func New(seed int64, ids ...string) *Simulator {
	s := &Simulator{
		Replicas: map[string]*orset.ORSet{},
		ids:      append([]string(nil), ids...),
		rng:      rand.New(rand.NewSource(seed)),
		links:    map[[2]string]bool{},
	}
	for _, id := range ids {
		s.Replicas[id] = orset.New(id, ids)
	}
	return s
}

// IDs returns the sorted replica ids.
func (s *Simulator) IDs() []string { return append([]string(nil), s.ids...) }

func (s *Simulator) logf(format string, args ...interface{}) {
	s.Log = append(s.Log, fmt.Sprintf("t=%-4d %s", s.Now, fmt.Sprintf(format, args...)))
}

// Notef appends a narrated note to the event log at the current time.
// Use inside At() callbacks for demo narration.
func (s *Simulator) Notef(format string, args ...interface{}) {
	s.logf(format, args...)
}

// At schedules fn to run at virtual time t.
func (s *Simulator) At(t int64, fn func()) {
	s.seq++
	heap.Push(&s.queue, event{time: t, seq: s.seq, run: fn})
}

// Add schedules replica r to add element e at time t.
func (s *Simulator) Add(t int64, r, e string) {
	s.At(t, func() {
		d := s.Replicas[r].Add(e)
		s.logf("%s.add(%q)  -> tag %s:%d", r, e, d.Replica, d.Seq)
	})
}

// Remove schedules replica r to remove element e at time t.
func (s *Simulator) Remove(t int64, r, e string) {
	s.At(t, func() {
		rd, n := s.Replicas[r].Remove(e)
		s.logf("%s.remove(%q) -> tombstoned %d observed tag(s), remove event %s:%d", r, e, n, rd.Replica, rd.Seq)
	})
}

// GC schedules a garbage-collection pass on replica r at time t.
func (s *Simulator) GC(t int64, r string) {
	s.At(t, func() {
		n := s.Replicas[r].GC()
		s.logf("%s.gc()       -> collected %d causally-stable tombstone(s)", r, n)
	})
}

// GCAll schedules a GC pass on every replica at time t.
func (s *Simulator) GCAll(t int64) {
	for _, id := range s.ids {
		s.GC(t, id)
	}
}

// Send schedules replica `from` to snapshot its state at time t and
// deliver it to `to` after `delay` (subject to link state, loss, dup).
func (s *Simulator) Send(t int64, from, to string, delay int64) {
	s.At(t, func() {
		snap := s.Replicas[from].Snapshot()
		s.logf("%s -> %s : send state (adds=%d tombs=%d)", from, to, snapAdds(snap), snapTombs(snap))
		s.deliver(s.Now+delay, from, to, snap, false)
	})
}

func (s *Simulator) deliver(at int64, from, to string, snap orset.Snapshot, isDup bool) {
	s.At(at, func() {
		if !s.linkUp(from, to) {
			s.Dropped++
			s.logf("%s -> %s : DROPPED (link down)", from, to)
			return
		}
		if s.rng.Float64() < s.Loss {
			s.Dropped++
			s.logf("%s -> %s : DROPPED (lossy channel)", from, to)
			return
		}
		s.Replicas[to].Merge(snap)
		s.Delivered++
		s.logf("%s -> %s : delivered, %s now has %v", from, to, to, s.Replicas[to].Elements())
		if !isDup && s.rng.Float64() < s.Dup {
			s.logf("%s -> %s : duplicated by channel", from, to)
			s.deliver(s.Now+1, from, to, snap, true)
		}
	})
}

// GossipAll schedules every replica to send its state to every other
// replica at time t, with per-message random delay in [1, maxDelay].
func (s *Simulator) GossipAll(t int64, maxDelay int64) {
	for _, from := range s.ids {
		for _, to := range s.ids {
			if from == to {
				continue
			}
			delay := int64(1)
			if maxDelay > 1 {
				delay += s.rng.Int63n(maxDelay)
			}
			s.Send(t, from, to, delay)
		}
	}
}

// GossipEvery schedules rounds of GossipAll: at start, start+period, ...
// for the given number of rounds.
func (s *Simulator) GossipEvery(start, period int64, rounds int, maxDelay int64) {
	for i := 0; i < rounds; i++ {
		s.GossipAll(start+int64(i)*period, maxDelay)
	}
}

// SetLink sets both directions of the link between a and b.
func (s *Simulator) SetLink(a, b string, up bool) {
	s.links[[2]string{a, b}] = !up
	s.links[[2]string{b, a}] = !up
}

// Partition schedules a partition between group `a` and group `b` at time t.
func (s *Simulator) Partition(t int64, a, b []string) {
	s.At(t, func() {
		for _, x := range a {
			for _, y := range b {
				s.SetLink(x, y, false)
			}
		}
		s.logf("PARTITION: %v isolated from %v", a, b)
	})
}

// Heal restores all links at time t.
func (s *Simulator) Heal(t int64) {
	s.At(t, func() {
		s.links = map[[2]string]bool{}
		s.logf("HEAL: all links restored")
	})
}

func (s *Simulator) linkUp(from, to string) bool {
	return !s.links[[2]string{from, to}]
}

// Run processes queued events in deterministic (time, seq) order until the
// queue is empty. Events scheduled by events are processed too.
func (s *Simulator) Run() {
	for len(s.queue) > 0 {
		e := heap.Pop(&s.queue).(event)
		s.Now = e.time
		e.run()
	}
}

// Converged reports whether all replicas hold identical core state
// (adds, tombstones, version vectors).
func (s *Simulator) Converged() bool {
	var prev *orset.Snapshot
	for _, id := range s.ids {
		snap := s.Replicas[id].Snapshot()
		if prev != nil && !prev.CoreEqual(snap) {
			return false
		}
		prev = &snap
	}
	return true
}

// SameElements reports whether all replicas observe the same live elements.
func (s *Simulator) SameElements() bool {
	var prev []string
	for i, id := range s.ids {
		el := s.Replicas[id].Elements()
		if i > 0 && !eqStrs(prev, el) {
			return false
		}
		prev = el
	}
	return true
}

// Elements returns the live elements observed by replica r.
func (s *Simulator) Elements(r string) []string { return s.Replicas[r].Elements() }

func eqStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func snapAdds(s orset.Snapshot) int {
	n := 0
	for _, m := range s.Adds {
		n += len(m)
	}
	return n
}

func snapTombs(s orset.Snapshot) int {
	n := 0
	for _, m := range s.Tombs {
		n += len(m)
	}
	return n
}

// SortedElements is a small helper for demos/tests.
func SortedElements(s *Simulator, r string) []string {
	el := s.Elements(r)
	sort.Strings(el)
	return el
}
