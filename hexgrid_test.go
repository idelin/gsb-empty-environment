package hexgrid

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"
)

func TestDirections(t *testing.T) {
	want := [6]Hex{{1, 0}, {1, -1}, {0, -1}, {-1, 0}, {-1, 1}, {0, 1}}
	if Directions != want {
		t.Fatalf("Directions = %v, want %v", Directions, want)
	}
	if got := (Hex{}).Neighbors(); got != want {
		t.Fatalf("Neighbors() = %v, want %v", got, want)
	}
}

// diskCells returns all cells within the given hex distance of the origin,
// including negative coordinates.
func diskCells(radius int) []Hex {
	var out []Hex
	for q := -radius; q <= radius; q++ {
		lo := -radius
		if -q-radius > lo {
			lo = -q - radius
		}
		hi := radius
		if -q+radius < hi {
			hi = -q + radius
		}
		for r := lo; r <= hi; r++ {
			out = append(out, Hex{q, r})
		}
	}
	return out
}

func compareResults(t *testing.T, name string, got, want *Result) {
	t.Helper()
	if !reflect.DeepEqual(got.Reachable, want.Reachable) {
		t.Errorf("%s: reachable mismatch\n got: %v\nwant: %v", name, got.Reachable, want.Reachable)
	}
	if !reflect.DeepEqual(got.Dist, want.Dist) {
		t.Errorf("%s: dist mismatch\n got: %v\nwant: %v", name, got.Dist, want.Dist)
	}
	if !reflect.DeepEqual(got.Path, want.Path) {
		t.Errorf("%s: path mismatch\n got: %v\nwant: %v", name, got.Path, want.Path)
	}
}

func queryBoth(t *testing.T, store *Store, ref *Reference, q Query) (*Result, *Result) {
	t.Helper()
	got, err := store.Query(q)
	if err != nil {
		t.Fatalf("store query %+v: %v", q, err)
	}
	want, err := ref.Query(q)
	if err != nil {
		t.Fatalf("reference query %+v: %v", q, err)
	}
	return got, want
}

// TestCrossPartitionMatchesReference builds a map that spans many
// partitions and negative coordinates, then checks a battery of queries
// against the non-partitioned reference, before and after a batch update.
func TestCrossPartitionMatchesReference(t *testing.T) {
	layout := Layout{PageSize: 3, PartitionSize: 2}
	rng := rand.New(rand.NewSource(42))
	all := diskCells(6)
	protected := map[Hex]bool{{0, 0}: true, {-4, 2}: true, {3, -5}: true, {5, 5}: true}
	var walkable, obstacles []Hex
	for _, h := range all {
		if !protected[h] && rng.Float64() < 0.12 {
			obstacles = append(obstacles, h)
		} else {
			walkable = append(walkable, h)
		}
	}
	store := NewStore(layout, walkable, obstacles)
	ref := NewReference(walkable, obstacles)

	// Pick a far-away but reachable target dynamically so random
	// obstacles cannot make the test flaky.
	probe, err := store.Query(Query{Start: Hex{-4, 2}, K: 14})
	if err != nil {
		t.Fatal(err)
	}
	target := Hex{-4, 2}
	maxDist := -1
	for _, h := range probe.Reachable {
		if d := probe.Dist[h]; d > maxDist {
			maxDist, target = d, h
		}
	}
	queries := []Query{
		{Start: Hex{0, 0}, K: 6},
		{Start: Hex{-4, 2}, K: 14, Target: &target},
		{Start: Hex{3, -5}, K: 4},
		{Start: Hex{0, 0}, K: 0},
	}
	for _, q := range queries {
		got, want := queryBoth(t, store, ref, q)
		compareResults(t, "v1", got, want)
	}

	// The result must actually cross partition boundaries and use
	// negative coordinates, otherwise the test proves nothing.
	res, err := store.Query(queries[1])
	if err != nil {
		t.Fatal(err)
	}
	parts := map[PartitionID]bool{}
	neg := false
	for _, h := range res.Reachable {
		pid, _ := layout.Locate(h)
		parts[pid] = true
		if h.Q < 0 || h.R < 0 {
			neg = true
		}
	}
	if len(parts) < 2 {
		t.Errorf("reachable set spans only %d partition(s), want several", len(parts))
	}
	if !neg {
		t.Errorf("reachable set has no negative coordinates")
	}
	if len(res.Path) == 0 {
		t.Fatalf("no path to %v", target)
	}
	crossed := map[PartitionID]bool{}
	for _, h := range res.Path {
		pid, _ := layout.Locate(h)
		crossed[pid] = true
	}
	if len(crossed) < 2 {
		t.Errorf("path stays within one partition, want a boundary crossing")
	}

	// Batch walkability update: block and unblock a deterministic set.
	changes := map[Hex]bool{}
	for i, h := range all {
		if protected[h] {
			continue
		}
		switch i % 17 {
		case 0:
			changes[h] = false
		case 1:
			changes[h] = true
		}
	}
	if v := store.Update(changes); v != 2 {
		t.Fatalf("store version = %d, want 2", v)
	}
	ref.Update(changes)

	for _, q := range queries {
		got, want := queryBoth(t, store, ref, q) // latest version
		compareResults(t, "v2", got, want)
		q.Version = 1 // old-version query must still work
		got, want = queryBoth(t, store, ref, q)
		compareResults(t, "v1-after-update", got, want)
	}
}

func TestKZeroAndBlockedStart(t *testing.T) {
	walkable := []Hex{{0, 0}, {1, 0}, {0, 1}}
	obstacles := []Hex{{2, 2}}
	store := NewStore(Layout{PageSize: 2, PartitionSize: 2}, walkable, obstacles)

	res, err := store.Query(Query{Start: Hex{0, 0}, K: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Reachable, []Hex{{0, 0}}) {
		t.Errorf("K=0 reachable = %v, want [(0,0)]", res.Reachable)
	}
	if res.Dist[Hex{0, 0}] != 0 || len(res.Dist) != 1 {
		t.Errorf("K=0 dist = %v", res.Dist)
	}

	if _, err := store.Query(Query{Start: Hex{2, 2}, K: 3}); !errors.Is(err, ErrStartBlocked) {
		t.Errorf("obstacle start: err = %v, want ErrStartBlocked", err)
	}
	if _, err := store.Query(Query{Start: Hex{9, 9}, K: 3}); !errors.Is(err, ErrStartBlocked) {
		t.Errorf("outside-map start: err = %v, want ErrStartBlocked", err)
	}
	if _, err := store.Query(Query{Start: Hex{0, 0}, K: -1}); !errors.Is(err, ErrInvalidQuery) {
		t.Errorf("negative K: err = %v, want ErrInvalidQuery", err)
	}
}

func TestSurroundedByObstacles(t *testing.T) {
	start := Hex{0, 0}
	var obstacles []Hex
	for _, d := range Directions {
		obstacles = append(obstacles, d)
	}
	store := NewStore(Layout{PageSize: 2, PartitionSize: 1}, []Hex{start}, obstacles)
	res, err := store.Query(Query{Start: start, K: 5})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Reachable, []Hex{start}) {
		t.Errorf("reachable = %v, want only the start", res.Reachable)
	}
	if res.Dist[start] != 0 || len(res.Dist) != 1 {
		t.Errorf("dist = %v", res.Dist)
	}
}

func TestMissingPageRetry(t *testing.T) {
	layout := Layout{PageSize: 2, PartitionSize: 2}
	walkable := diskCells(3)
	store := NewStore(layout, walkable, nil)
	ref := NewReference(walkable, nil)
	q := Query{Start: Hex{-3, 0}, K: 6}

	// The page holding (3,0) "loads" only after two failed fetches.
	_, gid := layout.Locate(Hex{3, 0})
	if !store.DeferPageLoad(gid, 2) {
		t.Fatalf("page %v not found", gid)
	}
	got, err := store.Query(q)
	if err != nil {
		t.Fatalf("query with slow page: %v", err)
	}
	want, err := ref.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	compareResults(t, "slow-page", got, want)
	if got.Stats.PageRetries < 2 {
		t.Errorf("PageRetries = %d, want >= 2", got.Stats.PageRetries)
	}

	// If the page never arrives within MaxRetries, the query is rejected
	// with no partial result, instead of stitching a holed map.
	if !store.DeferPageLoad(gid, 10) {
		t.Fatalf("page %v not found", gid)
	}
	res, err := store.Query(q)
	if !errors.Is(err, ErrMissingPage) {
		t.Fatalf("err = %v, want ErrMissingPage", err)
	}
	if res != nil {
		t.Errorf("rejected query returned a partial result: %+v", res)
	}
}

func TestVersionMismatchRetryAndReject(t *testing.T) {
	layout := Layout{PageSize: 2, PartitionSize: 2}
	walkable := diskCells(3)
	store := NewStore(layout, walkable, nil)
	ref := NewReference(walkable, nil)

	changes := map[Hex]bool{{0, 0}: false, {1, 0}: false, {2, -2}: false}
	store.Update(changes)
	ref.Update(changes)
	q := Query{Start: Hex{-3, 0}, K: 6, Version: 2}

	// A partition that has not synced version 2 yet must be retried and
	// then rejected, never read at the old version.
	pid, _ := layout.Locate(Hex{2, 2})
	if !store.DesyncPartition(pid, 1) {
		t.Fatalf("partition %v not found", pid)
	}
	res, err := store.Query(q)
	if !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("err = %v, want ErrStaleVersion", err)
	}
	if res != nil {
		t.Errorf("rejected query returned a partial result: %+v", res)
	}

	// After the partition catches up, the same query succeeds and matches
	// the reference.
	store.DesyncPartition(pid, 2)
	got, want := queryBoth(t, store, ref, q)
	compareResults(t, "v2-after-sync", got, want)

	// Old-version queries keep working while history is retained.
	q.Version = 1
	got, want = queryBoth(t, store, ref, q)
	compareResults(t, "v1-retained", got, want)

	// After GC, old versions are rejected instead of silently wrong.
	store.DropHistoryBefore(2)
	if _, err := store.Query(q); !errors.Is(err, ErrVersionGone) {
		t.Errorf("err = %v, want ErrVersionGone", err)
	}
	if _, err := store.Query(Query{Start: Hex{-3, 0}, K: 1, Version: 99}); !errors.Is(err, ErrUnknownVersion) {
		t.Errorf("err = %v, want ErrUnknownVersion", err)
	}
}

// TestLexicographicPath pins the exact lexicographically smallest shortest
// path on a hand-verified map (same scenario as testdata/replay.json).
func TestLexicographicPath(t *testing.T) {
	var walkable []Hex
	obstacles := map[Hex]bool{{0, 0}: true, {0, 1}: true}
	for q := -2; q <= 2; q++ {
		for r := -2; r <= 2; r++ {
			if !obstacles[Hex{q, r}] {
				walkable = append(walkable, Hex{q, r})
			}
		}
	}
	store := NewStore(Layout{PageSize: 2, PartitionSize: 2}, walkable, []Hex{{0, 0}, {0, 1}})
	target := Hex{2, 0}
	res, err := store.Query(Query{Start: Hex{-2, 0}, K: 6, Target: &target})
	if err != nil {
		t.Fatal(err)
	}
	want := []Hex{{-2, 0}, {-1, -1}, {0, -1}, {1, -1}, {1, 0}, {2, 0}}
	if !reflect.DeepEqual(res.Path, want) {
		t.Errorf("path = %v, want %v", res.Path, want)
	}
	if res.Dist[target] != 5 {
		t.Errorf("dist = %d, want 5", res.Dist[target])
	}
}

func TestAccessStatsDeterministic(t *testing.T) {
	layout := Layout{PageSize: 2, PartitionSize: 2}
	walkable := diskCells(4)
	store := NewStore(layout, walkable, nil)
	ref := NewReference(walkable, nil)
	q := Query{Start: Hex{-4, 0}, K: 8}

	first, err := store.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	if first.Stats != second.Stats {
		t.Errorf("stats not deterministic: %+v vs %+v", first.Stats, second.Stats)
	}
	if first.Stats.PagesLoaded == 0 {
		t.Errorf("PagesLoaded = 0, want > 0")
	}
	if first.Stats.PageRetries != 0 {
		t.Errorf("PageRetries = %d, want 0 without faults", first.Stats.PageRetries)
	}
	wantRef, err := ref.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	if first.Stats.CellsExpanded != wantRef.Stats.CellsExpanded {
		t.Errorf("CellsExpanded = %d, reference = %d", first.Stats.CellsExpanded, wantRef.Stats.CellsExpanded)
	}
	// Pages are cached per query: loading cannot exceed one fetch per
	// distinct page touched.
	pages := map[PageID]bool{}
	for _, h := range first.Reachable {
		_, gid := layout.Locate(h)
		pages[gid] = true
	}
	if first.Stats.PagesLoaded < len(pages) {
		t.Errorf("PagesLoaded = %d < %d distinct reachable pages", first.Stats.PagesLoaded, len(pages))
	}
}

func TestReplayFile(t *testing.T) {
	f, err := LoadReplayFile("testdata/replay.json")
	if err != nil {
		t.Fatal(err)
	}
	report := RunReplay(f)
	for _, q := range report.Queries {
		if !q.MatchesReference {
			t.Errorf("query %q: partitioned store disagrees with reference", q.ID)
		}
		if !q.MatchesExpect {
			t.Errorf("query %q: result disagrees with pinned expectation (err=%q)", q.ID, q.Error)
		}
	}
	if !report.OK {
		t.Errorf("replay report not OK: %+v", report)
	}
}
