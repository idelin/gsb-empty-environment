package bqueue

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
	"time"
)

func recv(t *testing.T, s *Subscription) (uint64, interface{}) {
	t.Helper()
	seq, v, err := s.Receive(context.Background())
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	return seq, v
}

// pendingSeqs returns, for each committed slot, the sorted sequence numbers
// of the subscribers still expected to receive it. Subscriptions are
// identified by their next field for readability of failures.
func (q *Queue) account() (headSeqs []uint64, pendings []map[*Subscription]bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := 0; i < q.count; i++ {
		sl := q.slots[(q.head+i)%len(q.slots)]
		headSeqs = append(headSeqs, sl.seq)
		m := make(map[*Subscription]bool, len(sl.pending))
		for s := range sl.pending {
			m[s] = true
		}
		pendings = append(pendings, m)
	}
	return headSeqs, pendings
}

func in(p map[*Subscription]bool, subs ...*Subscription) bool {
	if len(p) != len(subs) {
		return false
	}
	for _, s := range subs {
		if !p[s] {
			return false
		}
	}
	return true
}

// A slow consumer pins the oldest slot: capacity is not released until it
// receives, even when every other subscriber has acknowledged.
func TestSlowConsumerBlocksReclaim(t *testing.T) {
	q := New(2)
	fast, slow := q.Subscribe(), q.Subscribe()

	ctx := context.Background()
	if _, err := q.Publish(ctx, "m0"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Publish(ctx, "m1"); err != nil {
		t.Fatal(err)
	}

	// Fast consumer acknowledges both messages.
	if seq, _ := recv(t, fast); seq != 0 {
		t.Fatalf("fast got seq %d, want 0", seq)
	}
	if seq, _ := recv(t, fast); seq != 1 {
		t.Fatalf("fast got seq %d, want 1", seq)
	}

	// Slow consumer still holds m0: no slot is reclaimed, publish times out.
	toCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, err := q.Publish(toCtx, "m2"); err != context.DeadlineExceeded {
		t.Fatalf("publish on full queue: err = %v, want DeadlineExceeded", err)
	}
	if n := q.Len(); n != 2 {
		t.Fatalf("Len = %d, want 2 (slow consumer pins capacity)", n)
	}

	// Slow consumer acknowledges m0: exactly one slot is released.
	if seq, v := recv(t, slow); seq != 0 || v != "m0" {
		t.Fatalf("slow got (%d,%v), want (0,m0)", seq, v)
	}
	if n := q.Len(); n != 1 {
		t.Fatalf("Len = %d, want 1 after slow consumer acked m0", n)
	}
	seq, err := q.Publish(ctx, "m2")
	if err != nil || seq != 2 {
		t.Fatalf("publish after release: seq=%d err=%v, want (2,nil)", seq, err)
	}

	_, pendings := q.account()
	if !in(pendings[0], slow) {
		t.Fatal("m1 should still be pending on slow only")
	}
	if !in(pendings[1], fast, slow) {
		t.Fatal("m2 should be pending on fast and slow")
	}
}

// Leaving acknowledges the leaver's unread messages, releasing capacity
// that only the leaver was pinning.
func TestLeaveAcksUnread(t *testing.T) {
	q := New(2)
	a, b := q.Subscribe(), q.Subscribe()
	ctx := context.Background()
	q.Publish(ctx, "m0")
	q.Publish(ctx, "m1")

	if seq, _ := recv(t, a); seq != 0 {
		t.Fatalf("a got seq %d, want 0", seq)
	}
	// m0 is now pending only on b; b leaves and thereby acknowledges it.
	b.Close()
	if n := q.Len(); n != 1 {
		t.Fatalf("Len = %d, want 1: leaver's unread m0 must be acked", n)
	}
	seq, err := q.Publish(ctx, "m2")
	if err != nil || seq != 2 {
		t.Fatalf("publish after leave: seq=%d err=%v, want (2,nil)", seq, err)
	}

	// A closed subscription stays closed and Close is idempotent.
	b.Close()
	if _, _, err := b.Receive(ctx); err != ErrSubClosed {
		t.Fatalf("Receive on closed sub: err = %v, want ErrSubClosed", err)
	}
}

// A subscriber that joins later never receives older messages; after
// leaving and rejoining it resumes from the publish frontier.
func TestRejoinSkipsOldMessages(t *testing.T) {
	q := New(4)
	ctx := context.Background()
	a := q.Subscribe()
	q.Publish(ctx, "m0")

	b := q.Subscribe() // joins after m0
	q.Publish(ctx, "m1")

	if seq, v := recv(t, b); seq != 1 || v != "m1" {
		t.Fatalf("late joiner got (%d,%v), want (1,m1)", seq, v)
	}
	if seq, _ := recv(t, a); seq != 0 {
		t.Fatalf("a got seq %d, want 0", seq)
	}
	if seq, _ := recv(t, a); seq != 1 {
		t.Fatalf("a got seq %d, want 1", seq)
	}

	// b leaves (acking nothing new) and rejoins; m2 published while b is
	// gone must not be delivered to the rejoined subscription.
	b.Close()
	q.Publish(ctx, "m2")
	b2 := q.Subscribe()
	q.Publish(ctx, "m3")
	if seq, v := recv(t, b2); seq != 3 || v != "m3" {
		t.Fatalf("rejoined sub got (%d,%v), want (3,m3)", seq, v)
	}
}

// A cancelled publish returns no sequence number only if it never
// committed; once committed, the sequence number is always returned.
func TestCancelPublish(t *testing.T) {
	ctx := context.Background()

	// Committed despite an already-cancelled context (capacity was free).
	q := New(1)
	sub := q.Subscribe()
	dead, cancel := context.WithCancel(ctx)
	cancel()
	seq, err := q.Publish(dead, "m0")
	if err != nil || seq != 0 {
		t.Fatalf("committed publish: seq=%d err=%v, want (0,nil)", seq, err)
	}

	// Queue is now full and pinned by a subscriber: cancellation wins.
	seq, err = q.Publish(dead, "m1")
	if err != context.Canceled || seq != 0 {
		t.Fatalf("uncommitted publish: seq=%d err=%v, want (0,Canceled)", seq, err)
	}
	if n := q.Len(); n != 1 {
		t.Fatalf("Len = %d, want 1: cancelled publish must not commit", n)
	}

	// Draining releases capacity again.
	if got, _ := recv(t, sub); got != 0 {
		t.Fatalf("sub got seq %d, want 0", got)
	}
	if n := q.Len(); n != 0 {
		t.Fatalf("Len = %d, want 0 after drain", n)
	}
}

// A publisher blocked on a full queue is unblocked when a leaver releases
// capacity.
func TestBlockedPublisherWokenByLeave(t *testing.T) {
	q := New(1)
	ctx := context.Background()
	sub := q.Subscribe()
	q.Publish(ctx, "m0")

	done := make(chan uint64, 1)
	go func() {
		seq, err := q.Publish(ctx, "m1")
		if err != nil {
			t.Errorf("blocked publish: %v", err)
		}
		done <- seq
	}()

	// Give the publisher a moment to block, then free the slot by leaving.
	time.Sleep(20 * time.Millisecond)
	sub.Close()
	select {
	case seq := <-done:
		if seq != 1 {
			t.Fatalf("seq = %d, want 1", seq)
		}
	case <-time.After(time.Second):
		t.Fatal("publisher still blocked after leaver released capacity")
	}
}

// --- Model-based interleaving test -------------------------------------

// model is an independent receive-set ledger used to cross-check the
// queue under randomized interleavings of subscribe / publish / receive /
// leave.
type model struct {
	capacity int
	nextSeq  uint64
	// committed[i] is the i-th oldest unreclaimed message.
	committed []uint64
	// pending[seq] is the set of subscriber ids still expected to ack seq.
	pending map[uint64]map[int]bool
	// subs[id] is the next seq the subscriber expects; absent id = left.
	subs   map[int]uint64
	nextID int
}

func newModel(capacity int) *model {
	return &model{
		capacity: capacity,
		pending:  make(map[uint64]map[int]bool),
		subs:     make(map[int]uint64),
	}
}

func (m *model) subscribe() int {
	id := m.nextID
	m.nextID++
	m.subs[id] = m.nextSeq // join frontier: no old messages
	return id
}

func (m *model) canPublish() bool { return len(m.committed) < m.capacity }

func (m *model) publish() uint64 {
	seq := m.nextSeq
	m.nextSeq++
	m.committed = append(m.committed, seq)
	p := make(map[int]bool, len(m.subs))
	for id := range m.subs {
		p[id] = true
	}
	m.pending[seq] = p
	m.release()
	return seq
}

func (m *model) canReceive(id int) bool {
	next, ok := m.subs[id]
	return ok && next < m.nextSeq
}

func (m *model) receive(id int) uint64 {
	seq := m.subs[id]
	delete(m.pending[seq], id)
	m.subs[id] = seq + 1
	m.release()
	return seq
}

func (m *model) leave(id int) {
	delete(m.subs, id)
	for _, p := range m.pending {
		delete(p, id) // leaving acknowledges all unread messages
	}
	m.release()
}

// release drops the fully acknowledged prefix, mirroring ring reclamation.
func (m *model) release() {
	for len(m.committed) > 0 && len(m.pending[m.committed[0]]) == 0 {
		delete(m.pending, m.committed[0])
		m.committed = m.committed[1:]
	}
}

// checkQueue reconciles the queue's internal receive-set accounting with
// the model ledger.
func checkQueue(t *testing.T, q *Queue, m *model, subs map[int]*Subscription) {
	t.Helper()
	seqs, pendings := q.account()
	if len(seqs) != len(m.committed) {
		t.Fatalf("committed seqs = %v, model wants %v", seqs, m.committed)
	}
	for i, seq := range seqs {
		if seq != m.committed[i] {
			t.Fatalf("committed seqs = %v, model wants %v", seqs, m.committed)
		}
		want := make(map[*Subscription]bool)
		for id := range m.pending[seq] {
			want[subs[id]] = true
		}
		if !reflect.DeepEqual(pendings[i], want) {
			var gotIDs, wantIDs []int
			for s := range pendings[i] {
				gotIDs = append(gotIDs, subID(subs, s))
			}
			for id := range m.pending[seq] {
				wantIDs = append(wantIDs, id)
			}
			sort.Ints(gotIDs)
			sort.Ints(wantIDs)
			t.Fatalf("seq %d: pending subs %v, model wants %v", seq, gotIDs, wantIDs)
		}
	}
	if n := q.Len(); n != len(m.committed) {
		t.Fatalf("Len = %d, model wants %d", n, len(m.committed))
	}
	if n := q.Len(); n > m.capacity {
		t.Fatalf("Len = %d exceeds capacity %d", n, m.capacity)
	}
}

func subID(subs map[int]*Subscription, s *Subscription) int {
	for id, sub := range subs {
		if sub == s {
			return id
		}
	}
	return -1
}

// TestInterleavings drives randomized interleavings of slow consumers,
// leavers, rejoiners and capacity pressure, verifying receive-set
// accounting after every step.
func TestInterleavings(t *testing.T) {
	for trial := 0; trial < 20; trial++ {
		name := fmt.Sprintf("trial-%d", trial)
		t.Run(name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(trial)))
			q := New(3)
			m := newModel(3)
			subs := make(map[int]*Subscription)
			ctx := context.Background()

			for step := 0; step < 400; step++ {
				// Collect live subscriber ids.
				var ids []int
				for id := range m.subs {
					ids = append(ids, id)
				}
				sort.Ints(ids)

				op := rng.Intn(100)
				switch {
				case op < 20 || len(ids) == 0:
					// Subscribe (possibly rejoin after leaving).
					id := m.subscribe()
					s := q.Subscribe()
					if s == nil {
						t.Fatal("Subscribe returned nil on open queue")
					}
					subs[id] = s

				case op < 55:
					// Publish; occasionally with a pre-cancelled context to
					// exercise commit-vs-cancel accounting.
					pctx := ctx
					if rng.Intn(10) == 0 {
						c, cancel := context.WithCancel(ctx)
						cancel()
						pctx = c
					}
					if m.canPublish() {
						want := m.publish()
						got, err := q.Publish(pctx, int(want))
						if err != nil {
							t.Fatalf("step %d: Publish: %v", step, err)
						}
						if got != want {
							t.Fatalf("step %d: seq = %d, model wants %d", step, got, want)
						}
					} else {
						// Full: a cancelled publish must not commit.
						c, cancel := context.WithCancel(ctx)
						cancel()
						if _, err := q.Publish(c, -1); err != context.Canceled {
							t.Fatalf("step %d: full-queue cancelled publish err = %v", step, err)
						}
					}

				case op < 85:
					// Receive on a random subscriber that has data (slow
					// consumers emerge from not being picked).
					var ready []int
					for _, id := range ids {
						if m.canReceive(id) {
							ready = append(ready, id)
						}
					}
					if len(ready) == 0 {
						continue
					}
					id := ready[rng.Intn(len(ready))]
					want := m.receive(id)
					got, v, err := subs[id].Receive(ctx)
					if err != nil {
						t.Fatalf("step %d: Receive: %v", step, err)
					}
					if got != want {
						t.Fatalf("step %d: sub %d got seq %d, model wants %d", step, id, got, want)
					}
					if v != int(want) {
						t.Fatalf("step %d: seq %d carried value %v, want %d", step, got, v, want)
					}

				default:
					// Leave: acknowledges the leaver's unread messages.
					id := ids[rng.Intn(len(ids))]
					m.leave(id)
					subs[id].Close()
					delete(subs, id)
				}

				checkQueue(t, q, m, subs)
			}
		})
	}
}
