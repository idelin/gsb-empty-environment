package shardscan

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// sortedModel is the sorted full-table reference every scan is checked
// against: the scan must reproduce it exactly, no key lost or duplicated.
func sortedModel(live map[string]string) []string {
	keys := make([]string, 0, len(live))
	for k := range live {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func pageKeys(page []KV) []string {
	keys := make([]string, len(page))
	for i, kv := range page {
		keys[i] = kv.Key
	}
	return keys
}

func expectPage(t *testing.T, expected []string, pos int, page []KV) {
	t.Helper()
	want := expected[pos:]
	if len(want) > len(page) {
		want = want[:len(page)]
	}
	got := pageKeys(page)
	if len(got) != len(want) {
		t.Fatalf("page length = %d, want %d (got %v, want prefix of %v)", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("page[%d] = %q, want %q (page %v)", i, got[i], want[i], got)
		}
	}
}

// TestScanModelRandomOps interleaves random puts, splits, merges and scan
// steps, checking every page against the sorted full-table model pinned at
// snapshot time. Shard invariants are checked after every mutation.
func TestScanModelRandomOps(t *testing.T) {
	for seed := int64(0); seed < 25; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runRandomModel(t, seed)
		})
	}
}

func runRandomModel(t *testing.T, seed int64) {
	rnd := rand.New(rand.NewSource(seed))
	st := NewStore()
	live := map[string]string{}

	var sc *Scanner
	var cur Cursor
	var expected []string
	pos := 0

	randKey := func() string { return fmt.Sprintf("k%03d", rnd.Intn(60)) }

	drain := func() {
		for sc != nil && !cur.Done {
			page, next, err := sc.Next(cur, 1+rnd.Intn(7))
			if err != nil {
				t.Fatalf("seed %d: drain Next: %v", seed, err)
			}
			expectPage(t, expected, pos, page)
			pos += len(page)
			cur = next
		}
		if sc != nil && pos != len(expected) {
			t.Fatalf("seed %d: scan covered %d keys, want %d: lost or duplicated keys", seed, pos, len(expected))
		}
	}

	for step := 0; step < 400; step++ {
		switch op := rnd.Intn(100); {
		case op < 35: // write to the live table
			k := randKey()
			if err := st.Put(k, "v"); err != nil {
				t.Fatalf("seed %d: Put: %v", seed, err)
			}
			live[k] = "v"
		case op < 55: // split a shard, possibly mid-scan
			_ = st.SplitAt(randKey())
		case op < 70: // merge adjacent shards, possibly mid-scan
			_ = st.MergeAt(randKey())
		default: // scan one page
			if sc == nil {
				sc = st.BeginScan()
				expected = sortedModel(live)
				cur = Cursor{}
				pos = 0
			}
			page, next, err := sc.Next(cur, 1+rnd.Intn(7))
			if err != nil {
				t.Fatalf("seed %d: Next: %v", seed, err)
			}
			expectPage(t, expected, pos, page)
			pos += len(page)
			cur = next
			if cur.Done {
				if pos != len(expected) {
					t.Fatalf("seed %d: scan covered %d keys, want %d", seed, pos, len(expected))
				}
				sc.Close()
				sc = nil
			}
		}
		if err := st.checkInvariants(); err != nil {
			t.Fatalf("seed %d step %d: %v", seed, step, err)
		}
	}
	drain()
}

// TestBoundaryKeys splits exactly on existing keys, on shard starts, and
// beyond all data, then scans key by key.
func TestBoundaryKeys(t *testing.T) {
	st := NewStore()
	keys := []string{"a", "b", "c", "d", "e"}
	for _, k := range keys {
		if err := st.Put(k, "v"); err != nil {
			t.Fatal(err)
		}
	}
	sc := st.BeginScan()

	for _, k := range []string{"c", "c", "a", "e", "zzz", "0"} {
		if err := st.SplitAt(k); err != nil {
			t.Fatalf("SplitAt(%q): %v", k, err)
		}
	}
	if err := st.checkInvariants(); err != nil {
		t.Fatal(err)
	}

	cur := Cursor{}
	pos := 0
	for !cur.Done {
		page, next, err := sc.Next(cur, 1)
		if err != nil {
			t.Fatal(err)
		}
		expectPage(t, keys, pos, page)
		pos += len(page)
		cur = next
	}
	if pos != len(keys) {
		t.Fatalf("scanned %d keys, want %d", pos, len(keys))
	}
}

// TestEmptyShards builds shards containing no data at all, including empty
// shards at both ends of the key space, and scans across them.
func TestEmptyShards(t *testing.T) {
	st := NewStore()
	for _, k := range []string{"m", "n"} {
		if err := st.Put(k, "v"); err != nil {
			t.Fatal(err)
		}
	}
	sc := st.BeginScan()

	// Empty shards before, between, and after the data, plus an empty shard
	// from splitting exactly on a boundary.
	for _, k := range []string{"b", "d", "f", "m", "x", "y", "z"} {
		if err := st.SplitAt(k); err != nil {
			t.Fatalf("SplitAt(%q): %v", k, err)
		}
	}
	if err := st.checkInvariants(); err != nil {
		t.Fatal(err)
	}

	expected := []string{"m", "n"}
	cur := Cursor{}
	pos := 0
	for !cur.Done {
		page, next, err := sc.Next(cur, 5)
		if err != nil {
			t.Fatal(err)
		}
		expectPage(t, expected, pos, page)
		pos += len(page)
		cur = next
	}
	if pos != len(expected) {
		t.Fatalf("scanned %d keys, want %d", pos, len(expected))
	}
}

// TestContinuousSplitMerge hammers the store with split/merge bursts between
// small pages and requires the scan to still reproduce the snapshot exactly.
func TestContinuousSplitMerge(t *testing.T) {
	rnd := rand.New(rand.NewSource(99))
	st := NewStore()
	live := map[string]string{}
	for i := 0; i < 100; i++ {
		k := fmt.Sprintf("key%04d", i)
		if err := st.Put(k, "v"); err != nil {
			t.Fatal(err)
		}
		live[k] = "v"
	}
	expected := sortedModel(live)
	sc := st.BeginScan()

	randKey := func() string { return fmt.Sprintf("key%04d", rnd.Intn(120)) }
	cur := Cursor{}
	pos := 0
	for !cur.Done {
		for i := rnd.Intn(6); i > 0; i-- {
			_ = st.SplitAt(randKey())
		}
		for i := rnd.Intn(4); i > 0; i-- {
			_ = st.MergeAt(randKey())
		}
		if err := st.checkInvariants(); err != nil {
			t.Fatal(err)
		}
		page, next, err := sc.Next(cur, 3)
		if err != nil {
			t.Fatal(err)
		}
		expectPage(t, expected, pos, page)
		pos += len(page)
		cur = next
	}
	if pos != len(expected) {
		t.Fatalf("scanned %d keys, want %d", pos, len(expected))
	}
}

// TestCursorSnapshotValidation ensures cursors are bound to their snapshot:
// foreign cursors are rejected by Next and by MergeCursors, and a released
// snapshot fails closed.
func TestCursorSnapshotValidation(t *testing.T) {
	st := NewStore()
	for _, k := range []string{"a", "b", "c", "d", "e", "f"} {
		if err := st.Put(k, "v"); err != nil {
			t.Fatal(err)
		}
	}
	a := st.BeginScan()
	b := st.BeginScan()

	_, curA, err := a.Next(Cursor{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Next(curA, 2); err != ErrSnapshotMismatch {
		t.Fatalf("foreign cursor accepted by Next: err = %v", err)
	}
	if _, err := MergeCursors(curA, Cursor{Snapshot: b.Snapshot(), After: "z"}); err != ErrSnapshotMismatch {
		t.Fatalf("cross-snapshot merge accepted: err = %v", err)
	}

	// Same-snapshot merge keeps the further boundary and both-done flag.
	_, curA2, err := a.Next(curA, 2)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := MergeCursors(curA, curA2)
	if err != nil {
		t.Fatal(err)
	}
	if merged.After != curA2.After || merged.Done {
		t.Fatalf("merged cursor = %+v, want After=%q Done=false", merged, curA2.After)
	}

	a.Close()
	if _, _, err := a.Next(Cursor{}, 1); err != ErrSnapshotGone {
		t.Fatalf("scan on released snapshot: err = %v", err)
	}
}

// TestEmptyTable scans a snapshot with no rows at all.
func TestEmptyTable(t *testing.T) {
	st := NewStore()
	_ = st.SplitAt("k")
	sc := st.BeginScan()
	page, cur, err := sc.Next(Cursor{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 0 || !cur.Done {
		t.Fatalf("empty scan = %v, %+v, want empty done page", pageKeys(page), cur)
	}
}
