package router

import (
	"fmt"
	"math/rand"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// model is a reference implementation: a complete subscription
// snapshot kept as plain maps, updated wholesale per batch.
type model struct {
	seq  uint64
	subs map[string]map[string]bool // subscriber -> set of prefixes
}

func newModel() *model { return &model{subs: make(map[string]map[string]bool)} }

func (m *model) apply(ops []Op) {
	if len(ops) == 0 {
		return
	}
	for _, op := range ops {
		ps, ok := m.subs[op.Sub]
		if op.Subscribe {
			if !ok {
				ps = make(map[string]bool)
				m.subs[op.Sub] = ps
			}
			ps[op.Prefix] = true
		} else if ok {
			delete(ps, op.Prefix)
			if len(ps) == 0 {
				delete(m.subs, op.Sub)
			}
		}
	}
	m.seq++
}

// match computes the full recipient set for topic under the current
// snapshot, deduplicated per subscriber.
func (m *model) match(topic string) []string {
	var ids []string
	for sub, prefixes := range m.subs {
		for p := range prefixes {
			if strings.HasPrefix(topic, p) {
				ids = append(ids, sub)
				break
			}
		}
	}
	sort.Strings(ids)
	return ids
}

func drainAll(subs map[string]*Subscriber) map[string][]Delivery {
	out := make(map[string][]Delivery)
	for id, s := range subs {
		out[id] = drain(s)
	}
	return out
}

func checkDeliveries(t *testing.T, step int, topic string, seq uint64, want []string, got map[string][]Delivery) {
	t.Helper()
	wantSet := make(map[string]bool, len(want))
	for _, id := range want {
		wantSet[id] = true
	}
	for id, ds := range got {
		if !wantSet[id] {
			if len(ds) != 0 {
				t.Fatalf("step %d: %s got %d unexpected deliveries for topic %q", step, id, len(ds), topic)
			}
			continue
		}
		if len(ds) != 1 {
			t.Fatalf("step %d: %s got %d deliveries for topic %q, want exactly 1", step, id, len(ds), topic)
		}
		if ds[0].Seq != seq {
			t.Fatalf("step %d: %s delivery seq = %d, want %d", step, id, ds[0].Seq, seq)
		}
		if ds[0].Message.Topic != topic {
			t.Fatalf("step %d: %s delivery topic = %q, want %q", step, id, ds[0].Message.Topic, topic)
		}
	}
}

// TestModelRandomOps drives the router with random batches of
// subscribe/unsubscribe ops interleaved with publishes, and checks
// every publish against the full-snapshot reference model: exactly the
// modeled recipients get exactly one delivery each, stamped with the
// snapshot sequence the publish reported.
func TestModelRandomOps(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	r := New()
	m := newModel()

	subIDs := []string{"s0", "s1", "s2", "s3"}
	prefixes := []string{"", "a/", "b/", "a/x/", "a/y/", "b/x/", "ab"}
	topics := []string{"a/x/1", "a/y/2", "b/x/3", "ab/4", "abc", "a/", "b/"}

	subs := make(map[string]*Subscriber)
	for _, id := range subIDs {
		subs[id] = r.AddSubscriber(id, 4096)
	}

	for step := 0; step < 400; step++ {
		if rng.Intn(2) == 0 {
			// Random batch of 1..4 ops, applied atomically.
			n := 1 + rng.Intn(4)
			ops := make([]Op, n)
			for i := range ops {
				ops[i] = Op{
					Sub:       subIDs[rng.Intn(len(subIDs))],
					Prefix:    prefixes[rng.Intn(len(prefixes))],
					Subscribe: rng.Intn(3) != 0, // bias towards subscribe
				}
			}
			m.apply(ops)
			if got := r.Apply(ops...); got != m.seq {
				t.Fatalf("step %d: apply seq = %d, model seq = %d", step, got, m.seq)
			}
			continue
		}

		topic := topics[rng.Intn(len(topics))]
		want := m.match(topic)
		pubSeq := r.Publish(topic, []byte(topic))
		if pubSeq != m.seq {
			t.Fatalf("step %d: publish seq = %d, model seq = %d", step, pubSeq, m.seq)
		}
		checkDeliveries(t, step, topic, m.seq, want, drainAll(subs))
	}
}

// TestAtomicSnapshotSwitch hammers Publish while a batch update swaps
// the routing of one topic from subscriber A to subscriber B. Every
// single message must be routed wholly by the old snapshot (only A) or
// wholly by the new one (only B) — never half-and-half — and once the
// swap has committed, only B may receive.
func TestAtomicSnapshotSwitch(t *testing.T) {
	r := New()
	a := r.AddSubscriber("A", 1<<16)
	b := r.AddSubscriber("B", 1<<16)

	seq1 := r.Apply(SubscribeOp("A", "t/"))

	var stop, published int32
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; atomic.LoadInt32(&stop) == 0; i++ {
			r.Publish("t/x", []byte(fmt.Sprint(i)))
			atomic.AddInt32(&published, 1)
		}
	}()

	// Wait until the old snapshot is provably in use, then swap
	// A -> B in one atomic batch and wait until the new one is.
	for len(a.C) == 0 {
		runtime.Gosched()
	}
	seq2 := r.Apply(UnsubscribeOp("A", "t/"), SubscribeOp("B", "t/"))
	for len(b.C) == 0 {
		runtime.Gosched()
	}
	atomic.StoreInt32(&stop, 1)
	wg.Wait()
	n := int(atomic.LoadInt32(&published))

	aGot := drain(a)
	bGot := drain(b)

	if len(aGot)+len(bGot) != n {
		t.Fatalf("total deliveries = %d, want %d (each message exactly once)", len(aGot)+len(bGot), n)
	}
	seen := make(map[string]int, n)
	for _, d := range aGot {
		if d.Seq != seq1 {
			t.Fatalf("A delivery stamped seq %d, want old snapshot seq %d", d.Seq, seq1)
		}
		seen[string(d.Message.Body)]++
	}
	for _, d := range bGot {
		if d.Seq != seq2 {
			t.Fatalf("B delivery stamped seq %d, want new snapshot seq %d", d.Seq, seq2)
		}
		seen[string(d.Message.Body)]++
	}
	for body, c := range seen {
		if c != 1 {
			t.Fatalf("message %q delivered %d times, want exactly 1", body, c)
		}
	}
	if len(aGot) == 0 || len(bGot) == 0 {
		t.Fatalf("expected deliveries on both sides of the switch, got A=%d B=%d", len(aGot), len(bGot))
	}

	// After the swap, only B receives.
	r.Publish("t/y", nil)
	if got := drain(a); len(got) != 0 {
		t.Fatalf("A received %d deliveries after swap", len(got))
	}
	if got := drain(b); len(got) != 1 {
		t.Fatalf("B received %d deliveries after swap, want 1", len(got))
	}
}

// TestConcurrentModelConsistency applies random batches and publishes
// concurrently. Applies are serialized with a reference model so the
// expected recipient set of every committed snapshot is recorded.
// Every publish (tagged with a unique body) must then be explained by
// exactly one snapshot: all its deliveries carry that snapshot's seq
// and its recipient set equals the model's set for that seq.
func TestConcurrentModelConsistency(t *testing.T) {
	r := New()

	subIDs := []string{"s0", "s1", "s2"}
	prefixes := []string{"a/", "a/x/", "b/"}
	const topic = "a/x/1"
	const publishes = 3000

	subs := make(map[string]*Subscriber)
	for _, id := range subIDs {
		subs[id] = r.AddSubscriber(id, publishes)
	}

	var mu sync.Mutex // serializes model+router applies and history writes
	m := newModel()
	history := map[uint64][]string{0: m.match(topic)}

	var wg sync.WaitGroup
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			lrng := rand.New(rand.NewSource(seed))
			for i := 0; i < 200; i++ {
				n := 1 + lrng.Intn(3)
				ops := make([]Op, n)
				for j := range ops {
					ops[j] = Op{
						Sub:       subIDs[lrng.Intn(len(subIDs))],
						Prefix:    prefixes[lrng.Intn(len(prefixes))],
						Subscribe: lrng.Intn(2) == 0,
					}
				}
				mu.Lock()
				m.apply(ops)
				seq := r.Apply(ops...)
				if seq != m.seq {
					t.Errorf("apply seq = %d, model seq = %d", seq, m.seq)
				}
				history[seq] = m.match(topic)
				mu.Unlock()
			}
		}(int64(w + 10))
	}

	// Concurrent publisher; bodies tag each publish uniquely.
	pubSeqs := make([]uint64, publishes)
	var pwg sync.WaitGroup
	pwg.Add(1)
	go func() {
		defer pwg.Done()
		for i := 0; i < publishes; i++ {
			pubSeqs[i] = r.Publish(topic, []byte(fmt.Sprint(i)))
		}
	}()

	wg.Wait()
	pwg.Wait()

	// Group deliveries by publish body.
	perPublish := make(map[string]map[string]uint64) // body -> sub -> seq
	for id, s := range subs {
		for _, d := range drain(s) {
			body := string(d.Message.Body)
			pm, ok := perPublish[body]
			if !ok {
				pm = make(map[string]uint64)
				perPublish[body] = pm
			}
			if _, dup := pm[id]; dup {
				t.Fatalf("subscriber %s got duplicate delivery for publish %q", id, body)
			}
			pm[id] = d.Seq
		}
	}

	mu.Lock()
	defer mu.Unlock()
	for i := 0; i < publishes; i++ {
		body := fmt.Sprint(i)
		pm := perPublish[body] // nil when the snapshot routed to nobody
		// One seq per publish, equal to what Publish returned.
		for sub, seq := range pm {
			if seq != pubSeqs[i] {
				t.Fatalf("publish %d: %s delivery seq = %d, Publish returned %d", i, sub, seq, pubSeqs[i])
			}
		}
		// Recipient set must equal the model's set for that snapshot.
		want, ok := history[pubSeqs[i]]
		if !ok {
			t.Fatalf("publish %d: unknown snapshot seq %d", i, pubSeqs[i])
		}
		var gotIDs []string
		for sub := range pm {
			gotIDs = append(gotIDs, sub)
		}
		sort.Strings(gotIDs)
		if fmt.Sprint(gotIDs) != fmt.Sprint(want) {
			t.Fatalf("publish %d: recipients %v, model snapshot %d routes to %v", i, gotIDs, pubSeqs[i], want)
		}
	}
}
