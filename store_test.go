package scanmodel

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// collect drains the scanner in pages of limit and returns every key.
func collect(t *testing.T, sc *Scanner, limit int) []string {
	t.Helper()
	var got []string
	for {
		page, done, err := sc.Next(limit)
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		got = append(got, page...)
		if done {
			return got
		}
		if len(page) == 0 {
			t.Fatalf("Next returned an empty non-final page (limit %d)", limit)
		}
	}
}

// model is the sorted full-table reference every scan is checked against.
func modelOf(snap *Snapshot) []string { return snap.Keys() }

func mustStore(t *testing.T, keys ...string) *Store {
	t.Helper()
	s, err := NewStore(keys...)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func mustSplit(t *testing.T, s *Store, id uint64, key string) {
	t.Helper()
	if err := s.Split(id, key); err != nil {
		t.Fatalf("Split(%d, %q): %v", id, key, err)
	}
}

func mustMerge(t *testing.T, s *Store, id uint64) {
	t.Helper()
	if err := s.Merge(id); err != nil {
		t.Fatalf("Merge(%d): %v", id, err)
	}
}

func onlyShard(t *testing.T, s *Store) Shard {
	t.Helper()
	shards := s.Shards()
	if len(shards) != 1 {
		t.Fatalf("expected 1 shard, got %d", len(shards))
	}
	return shards[0]
}

func TestScanMatchesSortedModel(t *testing.T) {
	keys := []string{"fig", "apple", "kiwi", "banana", "cherry", "date", "grape", "plum"}
	s := mustStore(t, keys...)
	snap := s.BeginSnapshot()
	for _, limit := range []int{1, 2, 3, 7, 100} {
		got := collect(t, s.NewScanner(snap), limit)
		want := modelOf(snap)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("limit %d: got %v, want %v", limit, got, want)
		}
	}
}

func TestSplitDuringScanKeepsLogicalCursor(t *testing.T) {
	keys := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	s := mustStore(t, keys...)
	snap := s.BeginSnapshot()
	sc := s.NewScanner(snap)

	page, _, err := sc.Next(3) // a b c
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(page, want) {
		t.Fatalf("first page = %v, want %v", page, want)
	}

	// Split the only shard right at the cursor boundary and ahead of it.
	root := onlyShard(t, s).ID
	mustSplit(t, s, root, "c") // boundary == cursor LastKey
	shards := s.Shards()
	mustSplit(t, s, shards[1].ID, "f") // split the shard ahead of the cursor

	rest := collect(t, sc, 2)
	if want := []string{"d", "e", "f", "g", "h"}; !reflect.DeepEqual(rest, want) {
		t.Fatalf("rest = %v, want %v", rest, want)
	}
}

func TestSplitAtBoundaryKeys(t *testing.T) {
	keys := []string{"m", "n", "o", "p", "q"}
	s := mustStore(t, keys...)
	snap := s.BeginSnapshot()
	want := modelOf(snap)

	// Split at the first key, the last key, and between every adjacent
	// pair — every split point is a logical boundary the scan may sit on.
	root := onlyShard(t, s).ID
	mustSplit(t, s, root, "n")
	for _, sh := range s.Shards() {
		if sh.Contains("p") {
			mustSplit(t, s, sh.ID, "p")
		}
	}
	for _, sh := range s.Shards() {
		if sh.Contains("q") {
			mustSplit(t, s, sh.ID, "q")
		}
	}

	got := collect(t, s.NewScanner(snap), 1)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestEmptyShardsAreSkipped(t *testing.T) {
	// Keys are sparse; splits in the gaps create shards holding no keys.
	keys := []string{"a", "m", "z"}
	s := mustStore(t, keys...)
	snap := s.BeginSnapshot()
	want := modelOf(snap)

	root := onlyShard(t, s).ID
	mustSplit(t, s, root, "g") // [−inf,g) holds a; [g,+inf) holds m,z
	for _, sh := range s.Shards() {
		if sh.Contains("t") || (sh.Start == "g" && sh.End == "") {
			mustSplit(t, s, sh.ID, "t") // [g,t) holds m; [t,+inf) holds z
		}
	}
	for _, sh := range s.Shards() {
		if sh.Start == "g" && sh.End == "t" {
			mustSplit(t, s, sh.ID, "j") // [g,j) holds m; [j,t) is EMPTY
		}
	}

	// Sanity: at least one shard contains no snapshot key.
	empty := 0
	for _, sh := range s.Shards() {
		has := false
		for _, k := range want {
			if sh.Contains(k) {
				has = true
			}
		}
		if !has {
			empty++
		}
	}
	if empty == 0 {
		t.Fatal("test setup did not produce an empty shard")
	}

	got := collect(t, s.NewScanner(snap), 1)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestContinuousSplitMerge(t *testing.T) {
	keys := []string{"k01", "k02", "k03", "k04", "k05", "k06", "k07", "k08"}
	s := mustStore(t, keys...)
	snap := s.BeginSnapshot()
	want := modelOf(snap)
	sc := s.NewScanner(snap)

	var got []string
	for step := 0; ; step++ {
		page, done, err := sc.Next(1)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, page...)
		if done {
			break
		}
		// Between every two pages, churn the topology: split the shard
		// covering the cursor, then merge everything back down.
		cur := sc.Cursor()
		for _, sh := range s.Shards() {
			if sh.Contains(cur.LastKey) && sh.End != "" && len(s.Shards()) < 6 {
				splitKey := cur.LastKey + "0" // strictly inside (LastKey, End)
				if sh.End == "" || splitKey < sh.End {
					mustSplit(t, s, sh.ID, splitKey)
				}
			}
		}
		for len(s.Shards()) > 1 {
			mustMerge(t, s, s.Shards()[0].ID)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMergeCursorsValidatesSnapshot(t *testing.T) {
	s := mustStore(t, "a", "b", "c")
	snap1 := s.BeginSnapshot()
	snap2 := s.BeginSnapshot()

	c1 := Cursor{SnapshotID: snap1.ID(), LastKey: "a"}
	c2 := Cursor{SnapshotID: snap1.ID(), LastKey: "c"}
	merged, err := MergeCursors(c1, c2)
	if err != nil {
		t.Fatalf("MergeCursors same snapshot: %v", err)
	}
	if merged.LastKey != "c" {
		t.Fatalf("merged cursor = %q, want furthest progress %q", merged.LastKey, "c")
	}

	if _, err := MergeCursors(c1, Cursor{SnapshotID: snap2.ID()}); !errors.Is(err, ErrSnapshotMismatch) {
		t.Fatalf("MergeCursors across snapshots: got %v, want ErrSnapshotMismatch", err)
	}
}

func TestResumeValidatesSnapshot(t *testing.T) {
	s := mustStore(t, "a", "b", "c")
	snap1 := s.BeginSnapshot()
	snap2 := s.BeginSnapshot()

	sc := s.NewScanner(snap1)
	if _, _, err := sc.Next(1); err != nil {
		t.Fatal(err)
	}
	cur := sc.Cursor()

	if _, err := s.Resume(snap2, cur); !errors.Is(err, ErrStaleSnapshot) {
		t.Fatalf("Resume with foreign snapshot: got %v, want ErrStaleSnapshot", err)
	}
	sc2, err := s.Resume(snap1, cur)
	if err != nil {
		t.Fatalf("Resume with own snapshot: %v", err)
	}
	got := collect(t, sc2, 10)
	if want := []string{"b", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("resumed scan = %v, want %v", got, want)
	}
}

// TestRandomizedSplitMergeScan is the model-based check: a scan over a
// fixed snapshot must return exactly the sorted full-table model no matter
// how the shard topology splits and merges around it.
func TestRandomizedSplitMergeScan(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 1337, 20261007} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))

			// Sparse random key set.
			var keys []string
			for i := 0; i < 60; i++ {
				if rng.Intn(100) < 70 {
					keys = append(keys, fmt.Sprintf("k%03d", i))
				}
			}
			s := mustStore(t, keys...)
			snap := s.BeginSnapshot()
			want := modelOf(snap)
			sc := s.NewScanner(snap)

			var got []string
			for {
				page, done, err := sc.Next(1 + rng.Intn(5))
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, page...)
				if done {
					break
				}
				// Random topology churn between pages.
				for op := 0; op < rng.Intn(4); op++ {
					shards := s.Shards()
					sh := shards[rng.Intn(len(shards))]
					if rng.Intn(2) == 0 {
						// Split at a random interior point: an existing
						// key, a boundary-adjacent key, or an empty gap.
						cand := fmt.Sprintf("k%03d", rng.Intn(61))
						if err := s.Split(sh.ID, cand); err != nil && !errors.Is(err, ErrSplitOutOfRange) {
							t.Fatalf("Split: %v", err)
						}
					} else {
						if err := s.Merge(sh.ID); err != nil && !errors.Is(err, ErrNotAdjacent) {
							t.Fatalf("Merge: %v", err)
						}
					}
				}
			}

			if !reflect.DeepEqual(got, want) {
				t.Fatalf("scan diverged from model:\n got %v\nwant %v", got, want)
			}
			if !sort.StringsAreSorted(got) {
				t.Fatal("scan output is not sorted")
			}
		})
	}
}

func TestSplitMergeValidation(t *testing.T) {
	s := mustStore(t, "a", "b")
	root := onlyShard(t, s).ID
	if err := s.Split(root, ""); !errors.Is(err, ErrSplitOutOfRange) {
		t.Fatalf("split at empty key: %v", err)
	}
	if err := s.Split(9999, "x"); !errors.Is(err, ErrShardNotFound) {
		t.Fatalf("split unknown shard: %v", err)
	}
	if err := s.Merge(root); !errors.Is(err, ErrNotAdjacent) {
		t.Fatalf("merge single shard: %v", err)
	}
	mustSplit(t, s, root, "a") // [−inf,a) empty, [a,+inf) holds a,b
	shards := s.Shards()
	mustMerge(t, s, shards[0].ID)
	if got := onlyShard(t, s); got.Start != "" || got.End != "" {
		t.Fatalf("merged shard = [%q,%q), want full range", got.Start, got.End)
	}
	// Old IDs must be dead after rebalancing.
	if err := s.Split(root, "x"); !errors.Is(err, ErrShardNotFound) {
		t.Fatalf("split with stale shard ID: got %v, want ErrShardNotFound", err)
	}
}
