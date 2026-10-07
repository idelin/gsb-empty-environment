package kvindex

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func mustGet(t *testing.T, ix *Index, key string, seq uint64, want string, wantOK bool) {
	t.Helper()
	got, ok := ix.Get(key, seq)
	if ok != wantOK || (wantOK && got != want) {
		t.Fatalf("Get(%q, %d) = (%q, %v), want (%q, %v)", key, seq, got, ok, want, wantOK)
	}
}

func mustErr(t *testing.T, err, want error) {
	t.Helper()
	if err != want {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

// TestResurrection: a newer point write revives a key covered by an older
// range tombstone, and historical reads still see the tombstone.
func TestResurrection(t *testing.T) {
	ix := New()
	if err := ix.Put("b", "v1", 1); err != nil {
		t.Fatal(err)
	}
	if err := ix.DeleteRange("a", "z", 2); err != nil {
		t.Fatal(err)
	}
	if err := ix.Put("b", "v2", 3); err != nil {
		t.Fatal(err)
	}
	mustGet(t, ix, "b", 1, "v1", true)
	mustGet(t, ix, "b", 2, "", false)
	mustGet(t, ix, "b", 3, "v2", true)
	mustGet(t, ix, "b", 100, "v2", true)
}

// TestNestedTombstones: deleting an already-deleted range nests tombstones;
// visibility is still decided by the newest event at or before the query seq.
func TestNestedTombstones(t *testing.T) {
	ix := New()
	if err := ix.Put("a", "v1", 1); err != nil {
		t.Fatal(err)
	}
	if err := ix.DeleteRange("a", "z", 2); err != nil {
		t.Fatal(err)
	}
	if err := ix.DeleteRange("a", "b", 3); err != nil { // nests on "a"
		t.Fatal(err)
	}
	if err := ix.Put("a", "v2", 4); err != nil {
		t.Fatal(err)
	}
	if err := ix.DeleteRange("", "c", 5); err != nil { // unbounded start nests again
		t.Fatal(err)
	}
	mustGet(t, ix, "a", 1, "v1", true)
	mustGet(t, ix, "a", 2, "", false)
	mustGet(t, ix, "a", 3, "", false)
	mustGet(t, ix, "a", 4, "v2", true)
	mustGet(t, ix, "a", 5, "", false)
	mustGet(t, ix, "a", 6, "", false)
}

// TestHalfOpenEndpoints: [start, end) removes start but not end.
func TestHalfOpenEndpoints(t *testing.T) {
	ix := New()
	for i, k := range []string{"a", "b", "c", "d"} {
		if err := ix.Put(k, "v"+k, uint64(i+1)); err != nil {
			t.Fatal(err)
		}
	}
	if err := ix.DeleteRange("b", "d", 5); err != nil {
		t.Fatal(err)
	}
	mustGet(t, ix, "a", 5, "va", true) // before start: untouched
	mustGet(t, ix, "b", 5, "", false)  // start: inclusive
	mustGet(t, ix, "c", 5, "", false)  // interior
	mustGet(t, ix, "d", 5, "vd", true) // end: exclusive
	// Historical reads are unaffected.
	mustGet(t, ix, "b", 4, "vb", true)
	mustGet(t, ix, "c", 4, "vc", true)
}

// TestInvalidRange rejects empty and inverted ranges.
func TestInvalidRange(t *testing.T) {
	ix := New()
	mustErr(t, ix.DeleteRange("b", "b", 1), ErrInvalidRange)
	mustErr(t, ix.DeleteRange("d", "a", 1), ErrInvalidRange)
	// A failed delete must not consume the sequence number.
	if err := ix.Put("a", "v", 1); err != nil {
		t.Fatal(err)
	}
}

// TestDuplicateSeq: identical replay is an idempotent no-op, a different
// operation with the same seq is rejected, and so is any older seq.
func TestDuplicateSeq(t *testing.T) {
	ix := New()
	if err := ix.Put("a", "v1", 5); err != nil {
		t.Fatal(err)
	}
	// Identical replay: accepted, state unchanged.
	if err := ix.Put("a", "v1", 5); err != nil {
		t.Fatalf("idempotent replay rejected: %v", err)
	}
	mustGet(t, ix, "a", 5, "v1", true)
	// Same seq, different op: rejected.
	mustErr(t, ix.Put("a", "v2", 5), ErrConflictingOp)
	mustErr(t, ix.Put("b", "v1", 5), ErrConflictingOp)
	mustErr(t, ix.DeleteRange("a", "z", 5), ErrConflictingOp)
	// Older seq: rejected.
	mustErr(t, ix.Put("a", "v3", 4), ErrNonMonotonicSeq)
	mustErr(t, ix.DeleteRange("a", "z", 3), ErrNonMonotonicSeq)
	// Identical replay of a range delete is also a no-op.
	if err := ix.DeleteRange("a", "z", 6); err != nil {
		t.Fatal(err)
	}
	if err := ix.DeleteRange("a", "z", 6); err != nil {
		t.Fatalf("idempotent range-delete replay rejected: %v", err)
	}
	mustGet(t, ix, "a", 6, "", false)
	// History must not contain duplicated events from replays.
	if n := len(ix.hist["a"].events); n != 2 {
		t.Fatalf("history length = %d, want 2 (put@5, tombstone@6)", n)
	}
	// Newer seq resumes normal operation.
	if err := ix.Put("a", "v3", 7); err != nil {
		t.Fatal(err)
	}
	mustGet(t, ix, "a", 7, "v3", true)
}

// TestHistoricalGet reads at sequence numbers between events.
func TestHistoricalGet(t *testing.T) {
	ix := New()
	if err := ix.Put("k", "v1", 10); err != nil {
		t.Fatal(err)
	}
	if err := ix.Put("k", "v2", 20); err != nil {
		t.Fatal(err)
	}
	mustGet(t, ix, "k", 0, "", false)
	mustGet(t, ix, "k", 9, "", false)
	mustGet(t, ix, "k", 10, "v1", true)
	mustGet(t, ix, "k", 19, "v1", true)
	mustGet(t, ix, "k", 20, "v2", true)
	mustGet(t, ix, "k", 1000, "v2", true)
	mustGet(t, ix, "absent", 1000, "", false)
}

// scanAll drains all pages and returns the concatenated entries.
func scanAll(t *testing.T, ix *Index, o ScanOptions) []Entry {
	t.Helper()
	var all []Entry
	for {
		entries, next, hasMore := ix.Scan(o)
		all = append(all, entries...)
		if !hasMore {
			return all
		}
		if next == "" {
			t.Fatal("hasMore with empty continuation token")
		}
		o.After = next
	}
}

func keysOf(entries []Entry) []string {
	keys := make([]string, len(entries))
	for i, e := range entries {
		keys[i] = e.Key
	}
	return keys
}

// TestScanPagination: pages concatenate to the full scan, continuation
// tokens resume exactly after the last returned key, and tombstoned keys
// are skipped without disturbing the page boundaries.
func TestScanPagination(t *testing.T) {
	ix := New()
	var seq uint64
	for i := 0; i < 10; i++ {
		seq++
		if err := ix.Put(fmt.Sprintf("k%02d", i), fmt.Sprintf("v%02d", i), seq); err != nil {
			t.Fatal(err)
		}
	}
	// Delete k02..k04 (i.e. k02, k03) so pages straddle tombstones.
	seq++
	if err := ix.DeleteRange("k02", "k04", seq); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"k00", "k01", "k04", "k05", "k06", "k07", "k08", "k09"}

	full, _, hasMore := ix.Scan(ScanOptions{Start: "k00", End: "k99", Seq: seq})
	if hasMore {
		t.Fatal("unlimited scan reports hasMore")
	}
	if got := keysOf(full); !reflect.DeepEqual(got, wantKeys) {
		t.Fatalf("full scan = %v, want %v", got, wantKeys)
	}

	for limit := 1; limit <= len(wantKeys)+1; limit++ {
		got := scanAll(t, ix, ScanOptions{Start: "k00", End: "k99", Seq: seq, Limit: limit})
		if !reflect.DeepEqual(keysOf(got), wantKeys) {
			t.Fatalf("limit %d: paged scan = %v, want %v", limit, keysOf(got), wantKeys)
		}
	}

	// Page boundaries land exactly on the continuation token.
	p1, next, hasMore := ix.Scan(ScanOptions{Start: "k00", End: "k99", Seq: seq, Limit: 3})
	if !hasMore || next != "k04" || !reflect.DeepEqual(keysOf(p1), wantKeys[:3]) {
		t.Fatalf("page 1 = %v, next %q, hasMore %v", keysOf(p1), next, hasMore)
	}
	p2, next, hasMore := ix.Scan(ScanOptions{Start: "k00", End: "k99", Seq: seq, Limit: 3, After: next})
	if !hasMore || next != "k07" || !reflect.DeepEqual(keysOf(p2), wantKeys[3:6]) {
		t.Fatalf("page 2 = %v, next %q, hasMore %v", keysOf(p2), next, hasMore)
	}
	p3, _, hasMore := ix.Scan(ScanOptions{Start: "k00", End: "k99", Seq: seq, Limit: 3, After: next})
	if hasMore || !reflect.DeepEqual(keysOf(p3), wantKeys[6:]) {
		t.Fatalf("page 3 = %v, hasMore %v", keysOf(p3), hasMore)
	}

	// Historical scan: before the delete, all ten keys are visible.
	hist := scanAll(t, ix, ScanOptions{Start: "k00", End: "k99", Seq: seq - 1, Limit: 4})
	if len(hist) != 10 {
		t.Fatalf("historical scan returned %d entries, want 10", len(hist))
	}
}

// TestScanBounds covers empty scans, exclusive ends and After past the end.
func TestScanBounds(t *testing.T) {
	ix := New()
	for i, k := range []string{"a", "b", "c"} {
		if err := ix.Put(k, k, uint64(i+1)); err != nil {
			t.Fatal(err)
		}
	}
	entries, _, hasMore := ix.Scan(ScanOptions{Start: "x", End: "z", Seq: 3})
	if len(entries) != 0 || hasMore {
		t.Fatalf("scan of empty range = %v, hasMore %v", entries, hasMore)
	}
	entries, _, _ = ix.Scan(ScanOptions{Start: "a", End: "c", Seq: 3})
	if got := keysOf(entries); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("scan [a,c) = %v, want [a b]", got)
	}
	entries, _, hasMore = ix.Scan(ScanOptions{Start: "a", End: "c", Seq: 3, After: "c"})
	if len(entries) != 0 || hasMore {
		t.Fatalf("scan with After past end = %v, hasMore %v", entries, hasMore)
	}
	// Unbounded end.
	entries, _, _ = ix.Scan(ScanOptions{Start: "b", Seq: 3})
	if got := keysOf(entries); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("unbounded scan = %v, want [b c]", got)
	}
}

// model is an independent reference implementation: it replays the
// operation log from scratch for every query.
type model struct {
	ops []Operation
}

func (m *model) get(key string, seq uint64) (string, bool) {
	val := ""
	ok := false
	for _, op := range m.ops {
		if op.Seq > seq {
			continue
		}
		switch op.Kind {
		case OpPut:
			if op.Key == key {
				val, ok = op.Value, true
			}
		case OpDeleteRange:
			if key >= op.Start && key < op.End {
				ok = false
			}
		}
	}
	return val, ok
}

func (m *model) scan(start, end string, seq uint64) []Entry {
	seen := map[string]bool{}
	for _, op := range m.ops {
		if op.Kind == OpPut {
			seen[op.Key] = true
		}
	}
	var keys []string
	for k := range seen {
		if k >= start && (end == "" || k < end) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var out []Entry
	for _, k := range keys {
		if v, ok := m.get(k, seq); ok {
			out = append(out, Entry{Key: k, Value: v})
		}
	}
	return out
}

// TestDifferentialRandom drives the index with random monotonic operations
// and cross-checks Get and Scan against the replay model at many sequence
// numbers, including historical ones.
func TestDifferentialRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	ix := New()
	m := &model{}

	key := func() string { return fmt.Sprintf("k%02d", rng.Intn(12)) }
	var seq uint64

	for round := 0; round < 400; round++ {
		seq += 1 + uint64(rng.Intn(3)) // strictly increasing, with occasional gaps
		if rng.Intn(2) == 0 {
			k, v := key(), fmt.Sprintf("v%d", round)
			if err := ix.Put(k, v, seq); err != nil {
				t.Fatalf("round %d: Put: %v", round, err)
			}
			m.ops = append(m.ops, Operation{Kind: OpPut, Seq: seq, Key: k, Value: v})
		} else {
			a, b := key(), key()
			if a == b {
				continue
			}
			if a > b {
				a, b = b, a
			}
			if err := ix.DeleteRange(a, b, seq); err != nil {
				t.Fatalf("round %d: DeleteRange: %v", round, err)
			}
			m.ops = append(m.ops, Operation{Kind: OpDeleteRange, Seq: seq, Start: a, End: b})
		}
	}

	// Point queries at every key and a spread of sequence numbers.
	for i := 0; i < 12; i++ {
		k := fmt.Sprintf("k%02d", i)
		for q := uint64(0); q <= seq+1; q++ {
			if q > 0 && rng.Intn(4) != 0 {
				continue
			}
			gv, gok := ix.Get(k, q)
			mv, mok := m.get(k, q)
			if gok != mok || (mok && gv != mv) {
				t.Fatalf("Get(%q, %d) = (%q, %v), model says (%q, %v)", k, q, gv, gok, mv, mok)
			}
		}
	}

	// Full and paged scans at several sequence numbers.
	for q := uint64(0); q <= seq; q += seq/7 + 1 {
		want := m.scan("k00", "k99", q)
		got := scanAll(t, ix, ScanOptions{Start: "k00", End: "k99", Seq: q, Limit: 1 + rng.Intn(5)})
		if len(want) == 0 && len(got) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Scan at seq %d = %v, model says %v", q, got, want)
		}
	}
}
