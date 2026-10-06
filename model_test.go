package bcast

import (
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// model is a reference receive-set ledger. Every queue operation is mirrored
// here and the full ledger state is compared after each step.
type model struct {
	capacity  int
	used      int
	nextSeq   uint64
	subs      map[uint64]bool
	reserved  map[uint64]bool
	live      map[uint64]map[uint64]bool // seq -> outstanding ack set
	reclaimed []uint64
}

func newModel(capacity int) *model {
	return &model{
		capacity: capacity,
		subs:     make(map[uint64]bool),
		reserved: make(map[uint64]bool),
		live:     make(map[uint64]map[uint64]bool),
	}
}

func (m *model) begin() (uint64, bool) {
	if m.used == m.capacity {
		return 0, false
	}
	seq := m.nextSeq
	m.nextSeq++
	m.used++
	m.reserved[seq] = true
	return seq, true
}

func (m *model) commit(seq uint64) {
	if !m.reserved[seq] {
		return
	}
	delete(m.reserved, seq)
	pending := make(map[uint64]bool)
	for id := range m.subs {
		pending[id] = true
	}
	if len(pending) == 0 {
		m.reclaim(seq)
	} else {
		m.live[seq] = pending
	}
}

func (m *model) cancel(seq uint64) (uint64, bool) {
	if m.reserved[seq] {
		delete(m.reserved, seq)
		m.used--
		return 0, false
	}
	if _, live := m.live[seq]; live {
		return seq, true
	}
	// Committed with empty receive set: already reclaimed, still committed.
	for _, r := range m.reclaimed {
		if r == seq {
			return seq, true
		}
	}
	return 0, false
}

func (m *model) subscribe(id uint64) { m.subs[id] = true }

func (m *model) ack(id, seq uint64) {
	pending, ok := m.live[seq]
	if !ok || !m.subs[id] || !pending[id] {
		return
	}
	delete(pending, id)
	if len(pending) == 0 {
		m.reclaim(seq)
	}
}

func (m *model) unsubscribe(id uint64) {
	if !m.subs[id] {
		return
	}
	delete(m.subs, id)
	var drained []uint64
	for seq, pending := range m.live {
		if pending[id] {
			delete(pending, id)
			if len(pending) == 0 {
				drained = append(drained, seq)
			}
		}
	}
	sort.Slice(drained, func(i, j int) bool { return drained[i] < drained[j] })
	for _, seq := range drained {
		m.reclaim(seq)
	}
}

func (m *model) reclaim(seq uint64) {
	delete(m.live, seq)
	m.used--
	m.reclaimed = append(m.reclaimed, seq)
}

// expectRecv computes the oldest seq still owed by id.
func (m *model) expectRecv(id uint64) (uint64, bool) {
	if !m.subs[id] {
		return 0, false
	}
	var best uint64
	found := false
	for seq, pending := range m.live {
		if pending[id] && (!found || seq < best) {
			best, found = seq, true
		}
	}
	return best, found
}

type pubHandle struct {
	pub *Publication
	seq uint64
}

func checkLedger(t *testing.T, q *Queue, m *model, step int) {
	t.Helper()
	if q.Used() != m.used {
		t.Fatalf("step %d: used = %d, model %d", step, q.Used(), m.used)
	}
	live := q.Live()
	if len(live) != len(m.live) {
		t.Fatalf("step %d: live = %v, model %v", step, live, sortedKeys(m.live))
	}
	for _, seq := range live {
		if _, ok := m.live[seq]; !ok {
			t.Fatalf("step %d: live seq %d not in model", step, seq)
		}
		ids, ok := q.Pending(seq)
		if !ok {
			t.Fatalf("step %d: Pending(%d) not live", step, seq)
		}
		want := make([]uint64, 0, len(m.live[seq]))
		for id := range m.live[seq] {
			want = append(want, id)
		}
		sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
		if !reflect.DeepEqual(ids, want) {
			t.Fatalf("step %d: pending(%d) = %v, model %v", step, seq, ids, want)
		}
	}
	if got := q.Reclaimed(); !equalSeqs(got, m.reclaimed) {
		t.Fatalf("step %d: reclaimed = %v, model %v", step, got, m.reclaimed)
	}
}

func equalSeqs(a, b []uint64) bool {
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

func sortedKeys(m map[uint64]map[uint64]bool) []uint64 {
	out := make([]uint64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// TestModelInterleavings drives random interleavings of publish/commit/cancel,
// subscribe, recv/ack (including slow consumers that rarely ack) and
// unsubscribe/rejoin, verifying the receive-set ledger after every step.
func TestModelInterleavings(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		capacity := 2 + rng.Intn(4)
		q := New(capacity)
		m := newModel(capacity)

		var pubs []*pubHandle
		var subs []*Subscriber // active subscribers
		slow := make(map[uint64]bool)

		for step := 0; step < 2000; step++ {
			switch op := rng.Intn(100); {
			case op < 25: // begin
				p, err := q.Begin(step)
				seq, ok := m.begin()
				if (err == ErrFull) == ok {
					t.Fatalf("seed %d step %d: begin err=%v, model ok=%v", seed, step, err, ok)
				}
				if err == nil {
					if p.seq != seq {
						t.Fatalf("seed %d step %d: seq %d, model %d", seed, step, p.seq, seq)
					}
					pubs = append(pubs, &pubHandle{pub: p, seq: seq})
				}
			case op < 40 && len(pubs) > 0: // commit a random open publication
				i := rng.Intn(len(pubs))
				h := pubs[i]
				pubs = append(pubs[:i], pubs[i+1:]...)
				if got := h.pub.Commit(); got != h.seq {
					t.Fatalf("seed %d step %d: commit = %d, want %d", seed, step, got, h.seq)
				}
				m.commit(h.seq)
			case op < 50 && len(pubs) > 0: // cancel a random open publication
				i := rng.Intn(len(pubs))
				h := pubs[i]
				pubs = append(pubs[:i], pubs[i+1:]...)
				gotSeq, gotOK := h.pub.Cancel()
				wantSeq, wantOK := m.cancel(h.seq)
				if gotSeq != wantSeq || gotOK != wantOK {
					t.Fatalf("seed %d step %d: cancel = %d,%v, model %d,%v",
						seed, step, gotSeq, gotOK, wantSeq, wantOK)
				}
			case op < 65: // subscribe (sometimes a slow consumer)
				s := q.Subscribe()
				m.subscribe(s.id)
				slow[s.id] = rng.Intn(4) == 0
				subs = append(subs, s)
			case op < 80 && len(subs) > 0: // recv, then maybe ack
				s := subs[rng.Intn(len(subs))]
				seq, _, ok := s.Recv()
				wantSeq, wantOK := m.expectRecv(s.id)
				if ok != wantOK || (ok && seq != wantSeq) {
					t.Fatalf("seed %d step %d: recv = %d,%v, model %d,%v",
						seed, step, seq, ok, wantSeq, wantOK)
				}
				if ok && (!slow[s.id] || rng.Intn(5) == 0) {
					s.Ack(seq)
					m.ack(s.id, seq)
				}
			case len(subs) > 0: // unsubscribe (leaving acks unread items)
				i := rng.Intn(len(subs))
				s := subs[i]
				subs = append(subs[:i], subs[i+1:]...)
				s.Unsubscribe()
				m.unsubscribe(s.id)
				delete(slow, s.id)
			}
			checkLedger(t, q, m, step)
		}

		// Drain: everyone still registered leaves; everything must reclaim.
		for _, s := range subs {
			s.Unsubscribe()
			m.unsubscribe(s.id)
		}
		checkLedger(t, q, m, 2000)
		if len(m.live) != 0 || q.Used() != len(pubs) {
			t.Fatalf("seed %d: after drain live=%v used=%d open pubs=%d",
				seed, sortedKeys(m.live), q.Used(), len(pubs))
		}
	}
}

// TestConcurrentSmoke hammers the queue from goroutines; run with -race.
func TestConcurrentSmoke(t *testing.T) {
	q := New(8)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				p, err := q.Begin(i)
				if err == ErrFull {
					continue
				}
				if i%7 == 0 {
					p.Cancel()
				} else {
					seq := p.Commit()
					if i%11 == 0 {
						p.Cancel() // committed: no-op, still returns seq
						_ = seq
					}
				}
			}
		}()
	}
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				s := q.Subscribe()
				for j := 0; j < 10; j++ {
					if seq, _, ok := s.Recv(); ok {
						s.Ack(seq)
					}
				}
				s.Unsubscribe()
			}
		}()
	}
	wg.Wait()
	if got := q.Used(); got < 0 || got > 8 {
		t.Fatalf("used = %d out of bounds", got)
	}
}
