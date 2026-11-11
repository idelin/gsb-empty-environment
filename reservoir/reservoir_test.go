package reservoir

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

var testKey = []byte("probe-window-key-001")

func testConfig() Config {
	return Config{
		Capacity: 5,
		MinQuota: map[string]int{"A": 2, "B": 1},
	}
}

func pri(key []byte, id string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id))
	return mac.Sum(nil)
}

type candidate struct {
	ev  Event
	pri []byte
}

// referenceSelect implements the global specification directly:
// de-duplicate by ID (first-seen wins), protect per-class quotas, then
// fill remaining slots by global (priority, ID) ordering.
func referenceSelect(events []Event, cfg Config, key []byte) []Record {
	first := map[string]Event{}
	var order []string
	for _, ev := range events {
		if _, ok := first[ev.ID]; !ok {
			first[ev.ID] = Event{Class: ev.Class, ID: ev.ID, Payload: append([]byte(nil), ev.Payload...)}
			order = append(order, ev.ID)
		}
	}
	cands := make([]candidate, 0, len(order))
	for _, id := range order {
		ev := first[id]
		cands = append(cands, candidate{ev: ev, pri: pri(key, id)})
	}

	selected := map[string]bool{}
	for class, q := range cfg.MinQuota {
		var members []candidate
		for _, c := range cands {
			if c.ev.Class == class {
				members = append(members, c)
			}
		}
		sort.Slice(members, func(i, j int) bool { return rankLess(members[i], members[j]) })
		if q > len(members) {
			q = len(members)
		}
		for i := 0; i < q; i++ {
			selected[members[i].ev.ID] = true
		}
	}

	var rest []candidate
	for _, c := range cands {
		if !selected[c.ev.ID] {
			rest = append(rest, c)
		}
	}
	sort.Slice(rest, func(i, j int) bool { return rankLess(rest[i], rest[j]) })
	slots := cfg.Capacity - len(selected)
	for i := 0; i < slots && i < len(rest); i++ {
		selected[rest[i].ev.ID] = true
	}

	var out []candidate
	for _, c := range cands {
		if selected[c.ev.ID] {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return rankLess(out[i], out[j]) })

	records := make([]Record, len(out))
	for i, c := range out {
		records[i] = Record{Event: c.ev, Priority: c.pri}
	}
	return records
}

// rankLess reports whether a outranks b.
func rankLess(a, b candidate) bool {
	if c := bytes.Compare(a.pri, b.pri); c != 0 {
		return c > 0
	}
	return a.ev.ID < b.ev.ID
}

func recordIDs(records []Record) []string {
	ids := make([]string, len(records))
	for i, r := range records {
		ids[i] = r.Event.ID
	}
	return ids
}

func assertRecords(t *testing.T, got, want []Record, label string) {
	t.Helper()
	if !reflect.DeepEqual(recordIDs(got), recordIDs(want)) {
		t.Fatalf("%s: retained IDs = %v, want %v", label, recordIDs(got), recordIDs(want))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: records differ\ngot  %#v\nwant %#v", label, got, want)
	}
	for i := 1; i < len(got); i++ {
		if rankLess(candidate{pri: got[i].Priority, ev: got[i].Event}, candidate{pri: got[i-1].Priority, ev: got[i-1].Event}) {
			t.Fatalf("%s: snapshot not ordered by descending priority", label)
		}
	}
}

func replay(t *testing.T, events []Event, cfg Config, key []byte) *Sampler {
	t.Helper()
	s, err := New(cfg, key)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, ev := range events {
		s.Add(ev)
	}
	return s
}

// universeEvents defines five distinct events across three classes:
// A owns ids 0/1/2 (quota 2), B owns id 3 (quota 1), C owns id 4.
func universeEvents() []Event {
	return []Event{
		{Class: "A", ID: "id-0", Payload: []byte("p0")},
		{Class: "A", ID: "id-1", Payload: []byte("p1")},
		{Class: "A", ID: "id-2", Payload: []byte("p2")},
		{Class: "B", ID: "id-3", Payload: []byte("p3")},
		{Class: "C", ID: "id-4", Payload: []byte("p4")},
	}
}

// allSequences returns every sequence of length 1..maxLen whose
// elements are drawn from the small event universe (repeats included).
func allSequences(universe []Event, maxLen int) [][]Event {
	var out [][]Event
	var cur []Event
	var rec func(int)
	rec = func(remaining int) {
		if remaining == 0 {
			out = append(out, append([]Event(nil), cur...))
			return
		}
		for _, ev := range universe {
			cur = append(cur, ev)
			rec(remaining - 1)
			cur = cur[:len(cur)-1]
		}
	}
	for n := 1; n <= maxLen; n++ {
		cur = cur[:0]
		rec(n)
	}
	return out
}

func TestExhaustiveSingleStream(t *testing.T) {
	cfg := testConfig()
	universe := universeEvents()
	for _, stream := range allSequences(universe, 6) {
		s := replay(t, stream, cfg, testKey)
		assertRecords(t, s.Snapshot(), referenceSelect(stream, cfg, testKey), "single stream")

		stats := s.Stats()
		if stats.DistinctIDs != len(s.Snapshot()) {
			t.Fatalf("DistinctIDs = %d, retained = %d", stats.DistinctIDs, len(s.Snapshot()))
		}
		if got := s.Capacity(); got != cfg.Capacity {
			t.Fatalf("Capacity = %d, want %d", got, cfg.Capacity)
		}
	}
}

func TestExhaustiveMerges(t *testing.T) {
	cfg := testConfig()
	universe := universeEvents()

	for _, stream := range allSequences(universe, 6) {
		want := referenceSelect(stream, cfg, testKey)
		n := len(stream)

		// Every partition into three non-empty segments and every
		// bracketing of the merge must yield the global result.
		for i := 1; i < n; i++ {
			for j := i + 1; j < n; j++ {
				left := replay(t, stream[:i], cfg, testKey)
				mid := replay(t, stream[i:j], cfg, testKey)
				right := replay(t, stream[j:], cfg, testKey)

				lr := replay(t, stream[:i], cfg, testKey)
				if err := lr.Merge(mid); err != nil {
					t.Fatalf("Merge: %v", err)
				}
				if err := lr.Merge(right); err != nil {
					t.Fatalf("Merge: %v", err)
				}
				assertRecords(t, lr.Snapshot(), want, "(s1+s2)+s3")

				rr := replay(t, stream[i:j], cfg, testKey)
				if err := rr.Merge(right); err != nil {
					t.Fatalf("Merge: %v", err)
				}
				if err := left.Merge(rr); err != nil {
					t.Fatalf("Merge: %v", err)
				}
				assertRecords(t, left.Snapshot(), want, "s1+(s2+s3)")

				// Round-robin shards exercise a non-contiguous split.
				shards := make([]*Sampler, 3)
				for k := range shards {
					shards[k], _ = New(cfg, testKey)
				}
				for k, ev := range stream {
					shards[k%3].Add(ev)
				}
				combined, _ := New(cfg, testKey)
				for _, sh := range shards {
					if err := combined.Merge(sh); err != nil {
						t.Fatalf("Merge: %v", err)
					}
				}
				assertRecords(t, combined.Snapshot(), want, "round-robin shards")
			}
		}

		// Two-way contiguous splits, including merge order reversal.
		for i := 1; i < n; i++ {
			a := replay(t, stream[:i], cfg, testKey)
			b := replay(t, stream[i:], cfg, testKey)
			if err := a.Merge(b); err != nil {
				t.Fatalf("Merge: %v", err)
			}
			assertRecords(t, a.Snapshot(), want, "split merge")

			c := replay(t, stream[:i], cfg, testKey)
			d := replay(t, stream[i:], cfg, testKey)
			if err := d.Merge(c); err != nil {
				t.Fatalf("Merge: %v", err)
			}
			assertRecords(t, d.Snapshot(), want, "reversed split merge")
		}
	}
}

func TestExhaustiveShardUnionEquivalence(t *testing.T) {
	cfg := Config{Capacity: 4, MinQuota: map[string]int{"A": 1, "B": 1}}
	universe := universeEvents()
	rng := rand.New(rand.NewSource(42))

	for trial := 0; trial < 300; trial++ {
		n := rng.Intn(18) + 1
		stream := make([]Event, n)
		for i := range stream {
			stream[i] = universe[rng.Intn(len(universe))]
		}

		k := rng.Intn(3) + 1
		shards := make([]*Sampler, k)
		for i := range shards {
			shards[i], _ = New(cfg, testKey)
		}
		for _, ev := range stream {
			shards[rng.Intn(k)].Add(ev)
		}

		// Merge in a random order.
		rng.Shuffle(k, func(i, j int) { shards[i], shards[j] = shards[j], shards[i] })
		combined, _ := New(cfg, testKey)
		for _, sh := range shards {
			if err := combined.Merge(sh); err != nil {
				t.Fatalf("Merge: %v", err)
			}
		}
		assertRecords(t, combined.Snapshot(), referenceSelect(stream, cfg, testKey), "random shard union")
	}
}

func TestSameIDDifferentContent(t *testing.T) {
	cfg := testConfig()
	stream := []Event{
		{Class: "A", ID: "dup", Payload: []byte("first")},
		{Class: "A", ID: "dup", Payload: []byte("second")},
		{Class: "B", ID: "dup", Payload: []byte("third")},
	}
	s := replay(t, stream, cfg, testKey)
	stats := s.Stats()
	if stats.EventsSeen != 3 || stats.Duplicates != 2 || stats.ContentConflicts != 2 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	got := s.Snapshot()
	if len(got) != 1 || got[0].Event.ID != "dup" {
		t.Fatalf("expected one retained dup event, got %#v", got)
	}
	if !bytes.Equal(got[0].Event.Payload, []byte("first")) || got[0].Event.Class != "A" {
		t.Fatalf("first-seen content not preserved: %#v", got[0].Event)
	}

	// The conflict must propagate through merges and never duplicate.
	other, _ := New(cfg, testKey)
	other.Add(Event{Class: "C", ID: "dup", Payload: []byte("remote")})
	other.Add(Event{Class: "A", ID: "solo", Payload: []byte("x")})
	if err := s.Merge(other); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	ids := map[string]bool{}
	for _, r := range s.Snapshot() {
		if ids[r.Event.ID] {
			t.Fatalf("duplicate ID %s retained after merge", r.Event.ID)
		}
		ids[r.Event.ID] = true
	}
	if !ids["dup"] {
		t.Fatalf("dup event lost during merge")
	}
	if stats := s.Stats(); stats.ContentConflicts != 3 {
		t.Fatalf("ContentConflicts = %d, want 3", stats.ContentConflicts)
	}

	// Reference semantics: priority depends only on ID, first-seen wins.
	full := append(append([]Event(nil), stream...),
		Event{Class: "C", ID: "dup", Payload: []byte("remote")},
		Event{Class: "A", ID: "solo", Payload: []byte("x")})
	assertRecords(t, s.Snapshot(), referenceSelect(full, cfg, testKey), "conflict merge")
}

func TestHighFrequencyClassCannotStarveQuota(t *testing.T) {
	cfg := Config{Capacity: 5, MinQuota: map[string]int{"A": 1, "B": 2}}
	s, _ := New(cfg, testKey)

	// Forty noisy A events versus rare B/C alerts. B's two protected
	// slots must survive regardless of A's volume.
	for i := 0; i < 40; i++ {
		s.Add(Event{Class: "A", ID: "noise-" + string(rune('a'+i)), Payload: []byte("n")})
	}
	s.Add(Event{Class: "B", ID: "rare-b1", Payload: []byte("b1")})
	s.Add(Event{Class: "B", ID: "rare-b2", Payload: []byte("b2")})
	s.Add(Event{Class: "C", ID: "rare-c1", Payload: []byte("c1")})
	s.Add(Event{Class: "B", ID: "rare-b3", Payload: []byte("b3")})

	classCount := map[string]int{}
	ids := map[string]bool{}
	for _, r := range s.Snapshot() {
		if ids[r.Event.ID] {
			t.Fatalf("duplicate retained ID %s", r.Event.ID)
		}
		ids[r.Event.ID] = true
		classCount[r.Event.Class]++
	}
	if classCount["B"] < 2 {
		t.Fatalf("B quota starved: class counts = %v", classCount)
	}
	if classCount["A"] < 1 {
		t.Fatalf("A quota starved: class counts = %v", classCount)
	}
	if len(s.Snapshot()) != cfg.Capacity {
		t.Fatalf("retained = %d, want full capacity %d", len(s.Snapshot()), cfg.Capacity)
	}
	if !ids["rare-b1"] || !ids["rare-b2"] {
		t.Fatalf("protected rare B events evicted: %v", ids)
	}

	// The guarantee must also hold after shard merges.
	shardA, _ := New(cfg, testKey)
	shardB, _ := New(cfg, testKey)
	for i := 0; i < 40; i++ {
		shardA.Add(Event{Class: "A", ID: "noise-" + string(rune('a'+i)), Payload: []byte("n")})
	}
	shardB.Add(Event{Class: "B", ID: "rare-b1", Payload: []byte("b1")})
	shardB.Add(Event{Class: "B", ID: "rare-b2", Payload: []byte("b2")})
	shardB.Add(Event{Class: "C", ID: "rare-c1", Payload: []byte("c1")})
	shardB.Add(Event{Class: "B", ID: "rare-b3", Payload: []byte("b3")})
	if err := shardA.Merge(shardB); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	mergedCount := map[string]int{}
	for _, r := range shardA.Snapshot() {
		mergedCount[r.Event.Class]++
	}
	if mergedCount["B"] < 2 {
		t.Fatalf("B quota starved after merge: %v", mergedCount)
	}
}

func TestConfigRejectsInsufficientCapacity(t *testing.T) {
	cases := []Config{
		{Capacity: 2, MinQuota: map[string]int{"A": 1, "B": 2}},
		{Capacity: 1, MinQuota: map[string]int{"A": 2}},
		{Capacity: 0, MinQuota: map[string]int{"A": 1}},
		{Capacity: 5, MinQuota: map[string]int{"A": -1}},
		{Capacity: -1, MinQuota: nil},
	}
	for i, cfg := range cases {
		if _, err := New(cfg, testKey); err != ErrInsufficientCapacity {
			t.Fatalf("case %d: err = %v, want ErrInsufficientCapacity", i, err)
		}
	}

	// Zero capacity with no quotas is valid: everything is rejected.
	s, err := New(Config{Capacity: 0}, testKey)
	if err != nil {
		t.Fatalf("zero-capacity New: %v", err)
	}
	s.Add(Event{Class: "A", ID: "x"})
	if len(s.Snapshot()) != 0 || s.Stats().Rejections != 1 {
		t.Fatalf("zero-capacity sampler retained an event: %#v", s.Stats())
	}

	// Quotas fitting exactly within capacity are accepted.
	ok, err := New(Config{Capacity: 3, MinQuota: map[string]int{"A": 1, "B": 2}}, testKey)
	if err != nil || ok == nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestRotateKeyOpensNewWindow(t *testing.T) {
	cfg := testConfig()
	s, _ := New(cfg, testKey)
	s.Add(Event{Class: "A", ID: "id-0", Payload: []byte("p0")})
	if s.WindowID() != 0 {
		t.Fatalf("initial WindowID = %d, want 0", s.WindowID())
	}

	newKey := []byte("rotated-window-key")
	prev := s.RotateKey(newKey)
	if prev != 0 || s.WindowID() != 1 {
		t.Fatalf("window ids = %d -> %d, want 0 -> 1", prev, s.WindowID())
	}
	if !bytes.Equal(s.Key(), newKey) {
		t.Fatalf("key not rotated")
	}
	if len(s.Snapshot()) != 0 {
		t.Fatalf("records survived rotation")
	}
	if s.Stats() != (Stats{}) {
		t.Fatalf("counters survived rotation: %+v", s.Stats())
	}

	// Priority under the new key differs from the old one.
	s.Add(Event{Class: "A", ID: "id-0", Payload: []byte("p0")})
	if bytes.Equal(s.Snapshot()[0].Priority, pri(testKey, "id-0")) {
		t.Fatalf("priority did not change after key rotation")
	}

	// Windows cannot be merged, and neither can differing configs.
	old, _ := New(cfg, testKey)
	if err := s.Merge(old); err != ErrWindowMismatch {
		t.Fatalf("cross-window merge err = %v, want ErrWindowMismatch", err)
	}
	diff, _ := New(Config{Capacity: 9, MinQuota: cfg.MinQuota}, newKey)
	if err := s.Merge(diff); err != ErrWindowMismatch {
		t.Fatalf("cross-config merge err = %v, want ErrWindowMismatch", err)
	}
	sameWindow := newSampler(cfg, newKey, s.WindowID())
	if err := s.Merge(sameWindow); err != nil {
		t.Fatalf("same-window merge failed: %v", err)
	}
}

func TestDedupAndHeapInvariants(t *testing.T) {
	cfg := Config{Capacity: 3, MinQuota: map[string]int{"A": 1}}
	s, _ := New(cfg, testKey)

	// Seven distinct IDs over four repeats exceed the bounded buffers
	// (class A may buffer at most 3, the other classes share the rest),
	// so evictions/rejections actually occur.
	base := []Event{
		{Class: "A", ID: "a1", Payload: []byte("p")},
		{Class: "A", ID: "a2", Payload: []byte("p")},
		{Class: "A", ID: "a3", Payload: []byte("p")},
		{Class: "A", ID: "a4", Payload: []byte("p")},
		{Class: "B", ID: "b1", Payload: []byte("p")},
		{Class: "B", ID: "b2", Payload: []byte("p")},
		{Class: "C", ID: "c1", Payload: []byte("p")},
	}
	for repeat := 0; repeat < 4; repeat++ {
		for _, ev := range base {
			s.Add(ev)
		}
	}
	stats := s.Stats()
	if stats.EventsSeen != len(base)*4 ||
		stats.Duplicates+stats.DistinctIDs+stats.Evictions+stats.Rejections != stats.EventsSeen {
		t.Fatalf("unexpected dedup stats: %+v", stats)
	}
	if stats.Duplicates == 0 {
		t.Fatalf("expected repeated IDs to be counted as duplicates, stats: %+v", stats)
	}
	if stats.Evictions+stats.Rejections == 0 {
		t.Fatalf("overflow stream produced neither evictions nor rejections: %+v", stats)
	}

	// Every per-class candidate buffer stays sorted and within its
	// bounded limit, and the total buffered set never exceeds capacity.
	buffered := 0
	for class, buf := range s.candidates {
		buffered += len(buf)
		if len(buf) > s.classLimit(class) {
			t.Fatalf("class %s buffered %d, limit %d", class, len(buf), s.classLimit(class))
		}
		for i := 1; i < len(buf); i++ {
			if higher(buf[i], buf[i-1]) {
				t.Fatalf("class %s candidate buffer not priority-sorted", class)
			}
		}
	}
	if buffered != s.Stats().DistinctIDs {
		t.Fatalf("buffered = %d, distinct = %d", buffered, s.Stats().DistinctIDs)
	}

	// Snapshot copies are independent of sampler state.
	before := s.Snapshot()
	before[0].Event.Payload[0] = 0xFF
	before[0].Priority[0] = 0xFF
	again := s.Snapshot()
	if !reflect.DeepEqual(again, s.Snapshot()) || bytes.Equal(again[0].Event.Payload, before[0].Event.Payload) {
		t.Fatalf("Snapshot did not return independent copies")
	}

	// Repeated IDs never change the retained content or priority.
	want := referenceSelect(base, cfg, testKey)
	assertRecords(t, s.Snapshot(), want, "repeated-stream result")
}
