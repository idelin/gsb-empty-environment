package router

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// model is a reference implementation: a complete, independent
// subscription snapshot model used to cross-check the router.
type model struct {
	seq   uint64
	snaps map[uint64]map[uint64]Subscription
}

func newModel() *model {
	return &model{snaps: map[uint64]map[uint64]Subscription{0: {}}}
}

func (m *model) apply(u Update) uint64 {
	next := make(map[uint64]Subscription, len(m.snaps[m.seq])+len(u.Add))
	for id, s := range m.snaps[m.seq] {
		next[id] = s
	}
	for _, id := range u.Remove {
		delete(next, id)
	}
	for _, s := range u.Add {
		next[s.ID] = s
	}
	m.seq++
	m.snaps[m.seq] = next
	return m.seq
}

// expect computes the deduplicated, sorted subscriber set for topic
// at the given commit sequence, straight from the model snapshot.
func (m *model) expect(seq uint64, topic string) []string {
	subs, ok := m.snaps[seq]
	if !ok {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	for _, s := range subs {
		if strings.HasPrefix(topic, s.Prefix) && !seen[s.Subscriber] {
			seen[s.Subscriber] = true
			out = append(out, s.Subscriber)
		}
	}
	sort.Strings(out)
	return out
}

func subscribers(ds []Delivery) []string {
	if len(ds) == 0 {
		return nil
	}
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.Subscriber
	}
	sort.Strings(out)
	return out
}

// check routes topic at every committed sequence and asserts the
// router agrees with the model snapshot for that sequence.
func check(t *testing.T, r *Router, m *model, topic string) {
	t.Helper()
	for seq := uint64(0); seq <= m.seq; seq++ {
		ds, err := r.Publish(seq, topic, nil)
		if err != nil {
			t.Fatalf("Publish(seq=%d, %q): %v", seq, topic, err)
		}
		for _, d := range ds {
			if d.Seq != seq {
				t.Fatalf("delivery stamped seq=%d, want %d", d.Seq, seq)
			}
		}
		got, want := subscribers(ds), m.expect(seq, topic)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("seq=%d topic=%q: got %v, want %v", seq, topic, got, want)
		}
	}
}

// TestSnapshotSwitch commits a batch that swaps the whole table and
// verifies messages pick old or new table purely by commit sequence,
// never a mix of both.
func TestSnapshotSwitch(t *testing.T) {
	r := New()
	m := newModel()

	u1 := Update{Add: []Subscription{
		{ID: 1, Subscriber: "alice", Prefix: "a/"},
		{ID: 2, Subscriber: "bob", Prefix: "a/"},
		{ID: 3, Subscriber: "carol", Prefix: "b/"},
	}}
	if seq := r.Apply(u1); seq != m.apply(u1) {
		t.Fatal("sequence mismatch")
	}

	// Atomic swap: everyone on "a/" out, a new crew in, in one batch.
	u2 := Update{
		Remove: []uint64{1, 2},
		Add: []Subscription{
			{ID: 4, Subscriber: "dave", Prefix: "a/"},
			{ID: 5, Subscriber: "erin", Prefix: "a/b/"},
		},
	}
	if seq := r.Apply(u2); seq != m.apply(u2) {
		t.Fatal("sequence mismatch")
	}

	check(t, r, m, "a/b/c")
	check(t, r, m, "b/x")

	// Explicit old-vs-new: seq 1 must not see dave/erin, seq 2 must
	// not see alice/bob on "a/".
	old, _ := r.Publish(1, "a/b/c", nil)
	if got := subscribers(old); !reflect.DeepEqual(got, []string{"alice", "bob"}) {
		t.Fatalf("old table: got %v", got)
	}
	newer, _ := r.Publish(2, "a/b/c", nil)
	if got := subscribers(newer); !reflect.DeepEqual(got, []string{"dave", "erin"}) {
		t.Fatalf("new table: got %v", got)
	}
}

// TestCancellation verifies unsubscribe takes effect atomically with
// the rest of its batch, and that in-flight messages addressed to the
// old commit sequence are still served by the old snapshot.
func TestCancellation(t *testing.T) {
	r := New()
	m := newModel()

	u1 := Update{Add: []Subscription{
		{ID: 1, Subscriber: "alice", Prefix: "a/"},
		{ID: 2, Subscriber: "bob", Prefix: "a/"},
	}}
	r.Apply(u1)
	m.apply(u1)

	// Cancel alice and add carol in the same commit.
	u2 := Update{Remove: []uint64{1}, Add: []Subscription{
		{ID: 3, Subscriber: "carol", Prefix: "a/"},
	}}
	r.Apply(u2)
	m.apply(u2)

	check(t, r, m, "a/x")

	// In-flight message committed before the cancel still reaches alice.
	ds, err := r.Publish(1, "a/x", "in-flight")
	if err != nil {
		t.Fatal(err)
	}
	if got := subscribers(ds); !reflect.DeepEqual(got, []string{"alice", "bob"}) {
		t.Fatalf("in-flight on old seq: got %v", got)
	}

	// Cancelling everything leaves an empty table.
	u3 := Update{Remove: []uint64{2, 3}}
	r.Apply(u3)
	m.apply(u3)
	check(t, r, m, "a/x")
	if got := r.PublishNow("a/x", nil); len(got) != 0 {
		t.Fatalf("expected no deliveries, got %v", got)
	}
}

// TestMultiPrefixAndDuplicate verifies that a message matching several
// prefixes of one subscriber, or a duplicate subscription, is still
// delivered exactly once per subscriber.
func TestMultiPrefixAndDuplicate(t *testing.T) {
	r := New()
	m := newModel()

	u := Update{Add: []Subscription{
		{ID: 1, Subscriber: "alice", Prefix: "a/"},
		{ID: 2, Subscriber: "alice", Prefix: "a/b/"},   // overlapping prefix
		{ID: 3, Subscriber: "alice", Prefix: "a/"},     // exact duplicate
		{ID: 4, Subscriber: "bob", Prefix: "a/b/c/"},   // nested, other user
		{ID: 5, Subscriber: "carol", Prefix: "b/"},     // non-matching
	}}
	r.Apply(u)
	m.apply(u)

	ds, err := r.Publish(1, "a/b/c/d", "msg")
	if err != nil {
		t.Fatal(err)
	}
	if got := subscribers(ds); !reflect.DeepEqual(got, []string{"alice", "bob"}) {
		t.Fatalf("got %v, want [alice bob]", got)
	}
	count := 0
	for _, d := range ds {
		if d.Subscriber == "alice" {
			count++
			if d.Message != "msg" || d.Topic != "a/b/c/d" {
				t.Fatalf("bad delivery: %+v", d)
			}
		}
	}
	if count != 1 {
		t.Fatalf("alice got %d copies, want 1", count)
	}

	// Remove only one of alice's duplicates: she must still receive
	// exactly one copy via the remaining subscriptions.
	u2 := Update{Remove: []uint64{3}}
	r.Apply(u2)
	m.apply(u2)
	check(t, r, m, "a/b/c/d")
}

// TestConcurrentCommitsAndPublishes hammers the router with concurrent
// batch commits and publishes pinned to known sequences, then verifies
// every observed recipient set against the model. Run with -race.
func TestConcurrentCommitsAndPublishes(t *testing.T) {
	r := New()
	m := newModel()

	const batches = 64
	updates := make([]Update, batches)
	for i := 0; i < batches; i++ {
		id := uint64(i%8 + 1)
		if i%3 == 2 {
			updates[i] = Update{Remove: []uint64{id}}
		} else {
			updates[i] = Update{Add: []Subscription{{
				ID:         id,
				Subscriber: fmt.Sprintf("sub-%d", id),
				Prefix:     fmt.Sprintf("t/%d/", i%4),
			}}}
		}
	}

	// Committer: applies batches one by one (the router serializes
	// commits, so the model can be updated sequentially afterwards).
	var commitWG sync.WaitGroup
	commitWG.Add(1)
	go func() {
		defer commitWG.Done()
		for _, u := range updates {
			r.Apply(u)
		}
	}()

	// Publishers: pin each message to a sequence that already exists.
	var pubWG sync.WaitGroup
	errs := make(chan string, batches*4)
	for p := 0; p < 4; p++ {
		pubWG.Add(1)
		go func(p int) {
			defer pubWG.Done()
			for i := 0; i < batches; i++ {
				seq := uint64(i)
				ds, err := r.Publish(seq, fmt.Sprintf("t/%d/x", i%4), nil)
				if err != nil {
					errs <- fmt.Sprintf("seq=%d: %v", seq, err)
					return
				}
				// Recipient sets from concurrent publishers pinned to
				// the same seq must be identical (snapshot immutable).
				ds2, _ := r.Publish(seq, fmt.Sprintf("t/%d/x", i%4), nil)
				if !reflect.DeepEqual(subscribers(ds), subscribers(ds2)) {
					errs <- fmt.Sprintf("seq=%d: unstable snapshot", seq)
					return
				}
			}
		}(p)
	}
	commitWG.Wait()
	pubWG.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}

	// Replay every batch into the model, then cross-check every
	// committed snapshot against the router.
	for _, u := range updates {
		m.apply(u)
	}
	if r.Seq() != m.seq {
		t.Fatalf("router seq=%d, model seq=%d", r.Seq(), m.seq)
	}
	for i := 0; i < 4; i++ {
		check(t, r, m, fmt.Sprintf("t/%d/x", i))
	}
}

// TestRetain verifies old snapshots can be discarded and that routing
// against a discarded sequence fails instead of guessing.
func TestRetain(t *testing.T) {
	r := New()
	r.Apply(Update{Add: []Subscription{{ID: 1, Subscriber: "alice", Prefix: "a/"}}})
	r.Apply(Update{Remove: []uint64{1}})

	r.Retain(2)
	if _, err := r.Publish(1, "a/x", nil); err != ErrUnknownSeq {
		t.Fatalf("got %v, want ErrUnknownSeq", err)
	}
	if got := r.PublishNow("a/x", nil); len(got) != 0 {
		t.Fatalf("latest snapshot lost: %v", got)
	}

	// Retaining beyond the latest sequence still keeps the latest.
	r.Retain(100)
	if got := r.PublishNow("a/x", nil); len(got) != 0 {
		t.Fatalf("latest snapshot discarded: %v", got)
	}
}
