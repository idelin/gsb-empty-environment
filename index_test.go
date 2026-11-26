package gin

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func TestPendingAndFlushedQueriesAreTIDIdentical(t *testing.T) {
	idx := New(
		WithPendingCapacity(1000),
		WithPostingTreeThreshold(8),
		WithoutLossyPages(),
	)

	docs := []struct {
		tid  TID
		keys []string
	}{
		{MakeTID(1, 1), []string{"go", "db", "gin"}},
		{MakeTID(1, 2), []string{"go", "search"}},
		{MakeTID(2, 1), []string{"db", "postgres"}},
		{MakeTID(2, 2), []string{"gin", "postgres"}},
		{MakeTID(3, 1), []string{"go", "gin", "postgres"}},
	}
	for _, doc := range docs {
		if err := idx.Insert(doc.tid, doc.keys...); err != nil {
			t.Fatalf("insert %d: %v", doc.tid, err)
		}
	}

	queries := []Predicate{
		Key("go"),
		And("go", "gin"),
		Or("db", "search"),
		Subset("go", "gin", "postgres"),
		Not(Key("go")),
		Not(Key("missing-key")),
		And(Key("go"), Not(Key("postgres"))),
		And("go", "missing-key"),
		And(Not(Key("missing-key")), "go"),
	}
	before := make(map[int][]Match, len(queries))
	for i, query := range queries {
		before[i] = idx.Query(query)
	}
	if idx.PendingLen() == 0 {
		t.Fatal("test setup should leave a non-empty pending list")
	}

	if err := idx.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if idx.PendingLen() != 0 {
		t.Fatalf("pending entries after flush: %d", idx.PendingLen())
	}

	for i, query := range queries {
		after := idx.Query(query)
		if !reflect.DeepEqual(before[i], after) {
			t.Fatalf("query %d changed across flush:\nbefore=%+v\nafter=%+v", i, before[i], after)
		}
	}
	if err := idx.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestDeleteAndReinsertDoesNotResurrectOldTID(t *testing.T) {
	idx := New(WithPendingCapacity(1000), WithoutLossyPages())
	tid := MakeTID(7, 3)

	if err := idx.Insert(tid, "old", "shared"); err != nil {
		t.Fatal(err)
	}
	if !idx.Delete(tid) {
		t.Fatal("delete should report an existing TID")
	}
	if err := idx.Insert(tid, "new"); err != nil {
		t.Fatal(err)
	}

	assertQueryTIDs(t, idx, Key("old"), nil)
	assertQueryTIDs(t, idx, Key("shared"), nil)
	assertQueryTIDs(t, idx, Key("new"), []TID{tid})
	assertQueryTIDs(t, idx, Not(Key("old")), []TID{tid})

	if err := idx.Flush(); err != nil {
		t.Fatal(err)
	}
	assertQueryTIDs(t, idx, Key("old"), nil)
	assertQueryTIDs(t, idx, Key("shared"), nil)
	assertQueryTIDs(t, idx, Key("new"), []TID{tid})
	assertQueryTIDs(t, idx, Not(Key("old")), []TID{tid})

	// Exercise repeated reuse both across flushes and while entries are pending.
	for i, key := range []string{"k1", "k2", "k3", "k4", "k5", "k6"} {
		if err := idx.Insert(tid, key); err != nil {
			t.Fatal(err)
		}
		if i == 2 {
			if err := idx.Flush(); err != nil {
				t.Fatal(err)
			}
		}
		if !idx.Delete(tid) {
			t.Fatal("repeated delete should succeed")
		}
	}
	if err := idx.Insert(tid, "final"); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"old", "shared", "new", "k1", "k2", "k3", "k4", "k5", "k6"} {
		assertQueryTIDs(t, idx, Key(key), nil)
	}
	assertQueryTIDs(t, idx, Key("final"), []TID{tid})
	if err := idx.Flush(); err != nil {
		t.Fatal(err)
	}
	assertQueryTIDs(t, idx, Key("final"), []TID{tid})
	for _, key := range []string{"old", "shared", "new", "k1", "k2", "k3", "k4", "k5", "k6"} {
		assertQueryTIDs(t, idx, Key(key), nil)
	}
}

func TestPostingListUpgradesToPostingTreeAndPurgesDeleted(t *testing.T) {
	idx := New(
		WithPendingCapacity(5),
		WithPostingTreeThreshold(4),
		WithoutLossyPages(),
	)

	const count = 25
	for i := 0; i < count; i++ {
		if err := idx.Insert(MakeTID(uint64(100+i/8), uint16(i%8)), "dense"); err != nil {
			t.Fatal(err)
		}
	}
	if err := idx.Flush(); err != nil {
		t.Fatal(err)
	}

	info, ok := idx.PostingInfo("dense")
	if !ok {
		t.Fatal("dense posting not found")
	}
	if !info.IsTree {
		t.Fatalf("posting should be a tree, len=%d threshold=%d", info.Len, idx.Config().PostingTreeThreshold)
	}
	if info.Height < 2 {
		t.Fatalf("posting tree should have nontrivial height, got %d", info.Height)
	}
	if info.Len != count {
		t.Fatalf("posting length=%d, want %d", info.Len, count)
	}
	if !sort.SliceIsSorted(info.TIDs, func(i, j int) bool { return info.TIDs[i] < info.TIDs[j] }) {
		t.Fatal("posting tree traversal is not ordered")
	}

	// Duplicate inserts must deduplicate.
	if err := idx.Insert(info.TIDs[0], "dense"); err != nil {
		t.Fatal(err)
	}
	if err := idx.Flush(); err != nil {
		t.Fatal(err)
	}
	info, _ = idx.PostingInfo("dense")
	if info.Len != count {
		t.Fatalf("duplicate insert changed posting length to %d", info.Len)
	}

	for i := 0; i < 10; i++ {
		idx.Delete(info.TIDs[i])
	}
	if err := idx.Flush(); err != nil {
		t.Fatal(err)
	}
	info, _ = idx.PostingInfo("dense")
	if info.Len != count-10 {
		t.Fatalf("deleted TIDs were not purged: len=%d want=%d", info.Len, count-10)
	}
	if !info.IsTree {
		t.Fatal("posting tree should stay upgraded after purge")
	}
	if err := idx.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestLossyPagePruningAndRecheck(t *testing.T) {
	idx := New(
		WithPendingCapacity(1000),
		WithPostingTreeThreshold(4),
		WithMaxExactTIDsPerScan(4),
	)

	var page10, page20 []TID
	for i := 0; i < 4; i++ {
		page10 = append(page10, MakeTID(10, uint16(i)))
		page20 = append(page20, MakeTID(20, uint16(i)))
		if err := idx.Insert(page10[i], "a"); err != nil {
			t.Fatal(err)
		}
		if err := idx.Insert(page20[i], "a", "b"); err != nil {
			t.Fatal(err)
		}
	}
	exact := MakeTID(50, 0)
	if err := idx.Insert(exact, "a", "b"); err != nil {
		t.Fatal(err)
	}
	aOnly := MakeTID(60, 0)
	if err := idx.Insert(aOnly, "a"); err != nil {
		t.Fatal(err)
	}
	unrelated := MakeTID(20, 7)
	if err := idx.Insert(unrelated, "c"); err != nil {
		t.Fatal(err)
	}
	if err := idx.Flush(); err != nil {
		t.Fatal(err)
	}

	matches := idx.Query(And("a", "b"))
	wantPage20 := append(append([]TID{}, page20...), unrelated)
	assertMatchSet(t, matches, append(wantPage20, exact))
	for _, match := range matches {
		if TIDPage(match.TID) == 20 {
			if !match.Recheck || !match.Lossy || match.Truth != Maybe {
				t.Fatalf("page 20 match should be lossy MAYBE with recheck: %+v", match)
			}
		} else if match.TID == exact {
			if match.Recheck || match.Lossy || match.Truth != True {
				t.Fatalf("exact match should not require recheck: %+v", match)
			}
		} else {
			t.Fatalf("unexpected match from non-matching page: %+v", match)
		}
	}

	// FALSE AND MAYBE is FALSE and prunes the page without a heap recheck.
	assertQueryTIDs(t, idx, And("missing", "b"), nil)

	// TRUE OR MAYBE is TRUE, so a lossy page does not need a recheck.
	trueOrMaybe := Custom([]string{"b"}, func(lookup KeyLookup) Truth {
		return TriOr(True, lookup("b"))
	})
	matches = idx.Query(trueOrMaybe)
	for _, match := range matches {
		if match.Recheck || match.Truth != True {
			t.Fatalf("TRUE OR MAYBE should remain exact TRUE: %+v", match)
		}
	}
	if !containsTID(matches, page20[0]) {
		t.Fatal("lossy page candidates missing from TRUE OR MAYBE result")
	}

	// TRUE AND MAYBE is MAYBE and must require a heap recheck.
	trueAndMaybe := Custom([]string{"b"}, func(lookup KeyLookup) Truth {
		return TriAnd(True, lookup("b"))
	})
	matches = idx.Query(trueAndMaybe)
	if !containsTID(matches, page20[0]) {
		t.Fatal("lossy page candidates missing from TRUE AND MAYBE result")
	}
	for _, match := range matches {
		if TIDPage(match.TID) == 20 {
			if match.Truth != Maybe || !match.Recheck {
				t.Fatalf("TRUE AND MAYBE should produce MAYBE + recheck: %+v", match)
			}
		}
	}

	// The unrelated tuple on the lossy page is returned as a recheck candidate.
	if !containsTID(matches, unrelated) {
		t.Fatal("lossy page should return every live tuple on the page as a candidate")
	}
	if got := idx.Evaluate(And("a", "b"), unrelated); got != False {
		t.Fatalf("heap recheck should reject unrelated tuple, got %s", got)
	}
	if got := idx.Evaluate(And("a", "b"), page20[0]); got != True {
		t.Fatalf("heap recheck should accept matching tuple, got %s", got)
	}
}

func TestArrayJSONBAndCustomPredicates(t *testing.T) {
	idx := New(WithPendingCapacity(1000), WithoutLossyPages())

	arrayTID := MakeTID(1, 1)
	if err := idx.InsertArray(arrayTID, []string{"go", "db", "go"}); err != nil {
		t.Fatal(err)
	}
	jsonTID := MakeTID(2, 1)
	if err := idx.InsertJSONB(jsonTID, []byte(`{"tags":["go","gin"],"user":{"name":"alice"}}`)); err != nil {
		t.Fatal(err)
	}

	assertQueryTIDs(t, idx, And("go", "db"), []TID{arrayTID})
	assertQueryTIDs(t, idx, And("tags[]=go", "user.name=alice"), []TID{jsonTID})
	assertQueryTIDs(t, idx, Not(Key("db")), []TID{jsonTID})

	mapPredicate := Custom([]string{"a", "b"}, func(states map[string]Truth) Truth {
		return TriOr(states["a"], states["b"])
	})
	if err := idx.Insert(MakeTID(3, 1), "b"); err != nil {
		t.Fatal(err)
	}
	assertQueryTIDs(t, idx, mapPredicate, []TID{MakeTID(3, 1)})
	assertQueryTIDs(t, idx, Custom([]string{"a", "b"}, func(lookup KeyLookup) Truth {
		return TriOr(lookup("a"), lookup("b"))
	}), []TID{MakeTID(3, 1)})
	assertQueryTIDs(t, idx, []string{"b"}, []TID{MakeTID(3, 1)})

	direct := idx.Query([]string{"a", "b"}, func(lookup KeyLookup) Truth {
		return TriOr(lookup("a"), lookup("b"))
	})
	if len(direct) != 1 || direct[0].TID != MakeTID(3, 1) {
		t.Fatalf("Query(keys, consistency) mismatch: %+v", direct)
	}

	keys, err := JSONBKeys([]byte(`{"tags":["go"],"active":true}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tags", "tags[]", "tags[]=go", "go", "active", "active=true", "true"} {
		if !containsString(keys, want) {
			t.Fatalf("JSONB keys missing %q: %v", want, keys)
		}
	}
}

func TestRandomizedOperationsMatchReference(t *testing.T) {
	rng := rand.New(rand.NewSource(31))
	idx := New(
		WithPendingCapacity(11),
		WithPostingTreeThreshold(3),
		WithoutLossyPages(),
	)
	reference := make(map[TID]map[string]struct{})
	keys := []string{"a", "b", "c", "d", ""}

	randomKeys := func() []string {
		result := make([]string, 0, len(keys))
		for _, key := range keys {
			if rng.Intn(3) != 0 {
				result = append(result, key)
			}
		}
		return result
	}
	randomTID := func() TID {
		return MakeTID(uint64(rng.Intn(6)), uint16(rng.Intn(8)))
	}

	for step := 0; step < 2500; step++ {
		tid := randomTID()
		switch operation := rng.Intn(10); {
		case operation < 5:
			inserted := randomKeys()
			if err := idx.Insert(tid, inserted...); err != nil {
				t.Fatalf("step %d insert: %v", step, err)
			}
			set := make(map[string]struct{}, len(inserted))
			for _, key := range inserted {
				set[key] = struct{}{}
			}
			reference[tid] = set
		case operation < 7:
			idx.Delete(tid)
			delete(reference, tid)
		case operation == 7:
			if err := idx.Flush(); err != nil {
				t.Fatalf("step %d flush: %v", step, err)
			}
		default:
			queryKey := keys[rng.Intn(len(keys))]
			want := make([]TID, 0)
			for candidate, set := range reference {
				if _, ok := set[queryKey]; ok {
					want = append(want, candidate)
				}
			}
			sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
			assertQueryTIDs(t, idx, Key(queryKey), want)
		}

		if step%97 == 0 {
			if err := idx.Validate(); err != nil {
				t.Fatalf("step %d validate: %v", step, err)
			}
			if got := idx.PendingLen(); got > idx.Config().PendingCapacity {
				t.Fatalf("step %d pending length %d exceeds capacity", step, got)
			}
		}
	}

	if err := idx.Flush(); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		want := make([]TID, 0)
		for candidate, set := range reference {
			if _, ok := set[key]; ok {
				want = append(want, candidate)
			}
		}
		sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
		assertQueryTIDs(t, idx, Key(key), want)
	}
	if err := idx.Validate(); err != nil {
		t.Fatalf("final validate: %v", err)
	}
	if got := idx.Stats(); got.LiveTIDs != len(reference) {
		t.Fatalf("live TIDs=%d, want %d", got.LiveTIDs, len(reference))
	}
}

func TestConcurrentReadersAndWriters(t *testing.T) {
	idx := New(
		WithPendingCapacity(17),
		WithPostingTreeThreshold(5),
		WithoutLossyPages(),
	)
	const writers = 4
	const perWriter = 300
	errs := make(chan error, writers)
	for writer := 0; writer < writers; writer++ {
		go func(writer int) {
			for i := 0; i < perWriter; i++ {
				tid := MakeTID(uint64(writer), uint16(i))
				if err := idx.Insert(tid, "shared", fmt.Sprintf("w%d", writer)); err != nil {
					errs <- err
					return
				}
				if i%11 == 0 {
					idx.Delete(tid)
				}
				if i%29 == 0 {
					_ = idx.Query(Key("shared"))
				}
			}
			errs <- nil
		}(writer)
	}
	for i := 0; i < writers; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if err := idx.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := idx.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func assertQueryTIDs(t *testing.T, idx *Index, predicate any, want []TID) {
	t.Helper()
	matches := idx.Query(predicate)
	got := make([]TID, len(matches))
	for i, match := range matches {
		got[i] = match.TID
	}
	if !equalTIDs(got, want) {
		t.Fatalf("query TIDs=%v, want %v (matches=%+v)", got, want, matches)
	}
}

func assertMatchSet(t *testing.T, matches []Match, want []TID) {
	t.Helper()
	got := make([]TID, len(matches))
	for i, match := range matches {
		got[i] = match.TID
	}
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("match TIDs=%v, want %v (matches=%+v)", got, want, matches)
	}
}

func containsTID(matches []Match, tid TID) bool {
	index := sort.Search(len(matches), func(i int) bool { return matches[i].TID >= tid })
	return index < len(matches) && matches[index].TID == tid
}

func equalTIDs(left, right []TID) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
