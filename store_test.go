package checkpoint

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func mustCommit(t *testing.T, s *Store, id string, parents []string, ops []Op) *Snapshot {
	t.Helper()
	sn, err := s.Commit(id, parents, ops)
	if err != nil {
		t.Fatalf("commit %s: %v", id, err)
	}
	return sn
}

func mustExpand(t *testing.T, s *Store, id string) map[string]Hash {
	t.Helper()
	st, err := s.Expand(id)
	if err != nil {
		t.Fatalf("expand %s: %v", id, err)
	}
	return st
}

// Incremental snapshots share base chunks and record only deltas.
func TestIncrementalSnapshotsShareBaseChunks(t *testing.T) {
	s := NewStore()
	c1 := s.PutChunk([]byte("chunk-1"))
	c2 := s.PutChunk([]byte("chunk-2"))
	c3 := s.PutChunk([]byte("chunk-3"))

	mustCommit(t, s, "s0", nil, []Op{Add("a", c1), Add("b", c2)})
	s1 := mustCommit(t, s, "s1", []string{"s0"}, []Op{Add("c", c3)})
	s2 := mustCommit(t, s, "s2", []string{"s1"}, []Op{Remove("a")})

	// Deltas only: s1 and s2 record just their own ops.
	if len(s1.Ops) != 1 || len(s2.Ops) != 1 {
		t.Fatalf("expected delta-only snapshots, got %v / %v", s1.Ops, s2.Ops)
	}
	want1 := map[string]Hash{"a": c1, "b": c2, "c": c3}
	want2 := map[string]Hash{"b": c2, "c": c3}
	if got := mustExpand(t, s, "s1"); !reflect.DeepEqual(got, want1) {
		t.Fatalf("s1 = %v, want %v", got, want1)
	}
	if got := mustExpand(t, s, "s2"); !reflect.DeepEqual(got, want2) {
		t.Fatalf("s2 = %v, want %v", got, want2)
	}
	if err := s.ValidateDAG(); err != nil {
		t.Fatal(err)
	}
}

// A remove on one branch must not affect a forked sibling branch.
func TestForkRemoveIsolation(t *testing.T) {
	s := NewStore()
	c1 := s.PutChunk([]byte("shared"))
	c2 := s.PutChunk([]byte("only-b"))

	mustCommit(t, s, "base", nil, []Op{Add("x", c1)})
	mustCommit(t, s, "branchA", []string{"base"}, nil)
	mustCommit(t, s, "branchB", []string{"base"}, []Op{Remove("x"), Add("y", c2)})

	if got := mustExpand(t, s, "branchA"); !reflect.DeepEqual(got, map[string]Hash{"x": c1}) {
		t.Fatalf("branchA lost reference: %v", got)
	}
	if got := mustExpand(t, s, "branchB"); !reflect.DeepEqual(got, map[string]Hash{"y": c2}) {
		t.Fatalf("branchB = %v", got)
	}
}

// GC must keep chunks referenced by any surviving branch head.
func TestGCRespectsOtherBranches(t *testing.T) {
	s := NewStore()
	shared := s.PutChunk([]byte("shared"))
	dead := s.PutChunk([]byte("dead"))

	mustCommit(t, s, "base", nil, []Op{Add("keep", shared), Add("tmp", dead)})
	mustCommit(t, s, "A", []string{"base"}, nil)
	mustCommit(t, s, "B", []string{"base"}, []Op{Remove("tmp")})

	// B dropped "tmp", but A still references it: nothing is collected.
	removed, err := s.GC([]string{"A", "B"})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Fatalf("GC removed chunks still used by branch A: %v", removed)
	}

	// Once only B remains a head, the dead chunk is collected while the
	// shared chunk survives.
	removed, err = s.GC([]string{"B"})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != dead {
		t.Fatalf("removed = %v, want [%s]", removed, dead)
	}
	if _, ok := s.Chunk(shared); !ok {
		t.Fatal("shared chunk wrongly collected")
	}
}

// Download plans dedup chunks referenced by several names and skip
// chunks already present locally.
func TestDownloadPlanDedup(t *testing.T) {
	s := NewStore()
	c1 := s.PutChunk([]byte("c1"))
	c2 := s.PutChunk([]byte("c2"))
	c3 := s.PutChunk([]byte("c3"))

	// Two names reference the same chunk c1.
	mustCommit(t, s, "s0", nil, []Op{Add("p", c1), Add("q", c1), Add("r", c2)})
	mustCommit(t, s, "s1", []string{"s0"}, []Op{Add("s", c3)})

	plan, err := s.DownloadPlan("s1", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []Hash{c1, c2, c3}
	sort.Slice(want, func(i, j int) bool { return lessHash(want[i], want[j]) })
	if !reflect.DeepEqual(plan, want) {
		t.Fatalf("plan = %v, want %v (c1 must appear once)", plan, want)
	}

	// Locally held chunks are excluded.
	plan, err = s.DownloadPlan("s1", []Hash{c1, c3})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan, []Hash{c2}) {
		t.Fatalf("plan with local = %v, want [%s]", plan, c2)
	}
}

func TestMissingParentAndSelfParent(t *testing.T) {
	s := NewStore()
	if _, err := s.Commit("s0", []string{"ghost"}, nil); err == nil {
		t.Fatal("expected missing-parent error")
	}
	if _, err := s.Commit("s0", []string{"s0"}, nil); err != ErrSelfParent {
		t.Fatalf("expected ErrSelfParent, got %v", err)
	}
	if _, err := s.Expand("ghost"); err == nil {
		t.Fatal("expected unknown snapshot error")
	}
}

func TestDuplicateSnapshotID(t *testing.T) {
	s := NewStore()
	mustCommit(t, s, "s0", nil, nil)
	if _, err := s.Commit("s0", nil, nil); err != ErrDuplicateSnapshot {
		t.Fatalf("expected ErrDuplicateSnapshot, got %v", err)
	}
}

func TestRemoveUnknownRef(t *testing.T) {
	s := NewStore()
	mustCommit(t, s, "s0", nil, nil)
	if _, err := s.Commit("s1", []string{"s0"}, []Op{Remove("nope")}); err == nil {
		t.Fatal("expected unknown-ref error")
	}
}

func TestUnknownChunk(t *testing.T) {
	s := NewStore()
	if _, err := s.Commit("s0", nil, []Op{Add("a", Hash{1})}); err == nil {
		t.Fatal("expected unknown-chunk error")
	}
}

// Clean merge: disjoint changes from both sides are unioned.
func TestMergeClean(t *testing.T) {
	s := NewStore()
	c1 := s.PutChunk([]byte("c1"))
	c2 := s.PutChunk([]byte("c2"))
	c3 := s.PutChunk([]byte("c3"))

	mustCommit(t, s, "base", nil, []Op{Add("keep", c1), Add("drop", c2)})
	mustCommit(t, s, "left", []string{"base"}, []Op{Remove("drop")})
	mustCommit(t, s, "right", []string{"base"}, []Op{Add("new", c3)})
	mustCommit(t, s, "merged", []string{"left", "right"}, nil)

	want := map[string]Hash{"keep": c1, "new": c3}
	if got := mustExpand(t, s, "merged"); !reflect.DeepEqual(got, want) {
		t.Fatalf("merged = %v, want %v", got, want)
	}
}

// Conflicting merge: one side updates a reference, the other removes it.
func TestMergeConflict(t *testing.T) {
	s := NewStore()
	c1 := s.PutChunk([]byte("c1"))
	c2 := s.PutChunk([]byte("c2"))

	mustCommit(t, s, "base", nil, []Op{Add("x", c1)})
	mustCommit(t, s, "left", []string{"base"}, []Op{Add("x", c2)})
	mustCommit(t, s, "right", []string{"base"}, []Op{Remove("x")})

	_, err := s.Commit("merged", []string{"left", "right"}, nil)
	mc, ok := err.(*MergeConflictError)
	if !ok {
		t.Fatalf("expected MergeConflictError, got %v", err)
	}
	if !reflect.DeepEqual(mc.Names, []string{"x"}) {
		t.Fatalf("conflict names = %v", mc.Names)
	}
	// Failed merge must not leave a snapshot behind.
	if _, ok := s.Snapshot("merged"); ok {
		t.Fatal("conflicted merge was committed")
	}

	// Both sides updating the same name to different chunks also conflicts.
	c3 := s.PutChunk([]byte("c3"))
	mustCommit(t, s, "left2", []string{"base"}, []Op{Add("x", c3)})
	if _, err := s.Commit("merged2", []string{"left", "left2"}, nil); err == nil {
		t.Fatal("expected conflict for divergent updates")
	}
}

// The digest binds parent snapshots and the operation order.
func TestDigestBindsParentsAndOpOrder(t *testing.T) {
	build := func(parents []string, ops []Op) Hash {
		s := NewStore()
		c1 := s.PutChunk([]byte("c1"))
		c2 := s.PutChunk([]byte("c2"))
		mustCommit(t, s, "p0", nil, []Op{Add("a", c1)})
		mustCommit(t, s, "p1", nil, []Op{Add("b", c2)})
		sn := mustCommit(t, s, "s", parents, ops)
		return sn.Digest
	}
	c1 := HashBytes([]byte("c1"))
	c2 := HashBytes([]byte("c2"))

	single := build([]string{"p0"}, []Op{Add("x", c1), Add("y", c2)})

	// Operation order matters.
	reordered := build([]string{"p0"}, []Op{Add("y", c2), Add("x", c1)})
	if single == reordered {
		t.Fatal("digest ignores operation order")
	}
	// Parent identity matters.
	otherParent := build([]string{"p1"}, []Op{Add("x", c1), Add("y", c2)})
	if single == otherParent {
		t.Fatal("digest ignores parent snapshot")
	}
	// Merge parent order matters.
	mergeAB := build([]string{"p0", "p1"}, nil)
	mergeBA := build([]string{"p1", "p0"}, nil)
	if mergeAB == mergeBA {
		t.Fatal("digest ignores merge parent order")
	}
	// Deterministic: identical inputs give identical digests.
	if again := build([]string{"p0"}, []Op{Add("x", c1), Add("y", c2)}); again != single {
		t.Fatal("digest not deterministic")
	}
}

// Cross-check the incremental store against the full-set reference model
// on randomized histories with forks and merges.
func TestCrossCheckFullSetModel(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 1999} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			crossCheckFullSetModel(t, seed)
		})
	}
}

func crossCheckFullSetModel(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	s := NewStore()
	m := NewFullModel()

	var chunks []Hash
	for i := 0; i < 12; i++ {
		chunks = append(chunks, s.PutChunk([]byte(fmt.Sprintf("chunk-%d", i))))
	}
	names := []string{"a", "b", "c", "d", "e", "f"}

	var ids []string
	commitBoth := func(id string, parents []string, ops []Op) {
		t.Helper()
		_, sErr := s.Commit(id, parents, ops)
		mErr := m.Commit(id, parents, ops)
		if (sErr == nil) != (mErr == nil) {
			t.Fatalf("error mismatch for %s: store=%v model=%v", id, sErr, mErr)
		}
		if sErr != nil {
			return
		}
		ids = append(ids, id)
		got := mustExpand(t, s, id)
		want, _ := m.Full(id)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("state mismatch for %s:\nstore=%v\nmodel=%v", id, got, want)
		}
	}

	commitBoth("v0", nil, []Op{Add("a", chunks[0]), Add("b", chunks[1])})
	for i := 1; i <= 400; i++ {
		id := fmt.Sprintf("v%d", i)
		// Pick parents: mostly one, sometimes a merge of two versions.
		var parents []string
		if len(ids) >= 2 && rng.Intn(4) == 0 {
			p1 := ids[rng.Intn(len(ids))]
			p2 := ids[rng.Intn(len(ids))]
			for p2 == p1 {
				p2 = ids[rng.Intn(len(ids))]
			}
			parents = []string{p1, p2}
		} else {
			parents = []string{ids[rng.Intn(len(ids))]}
		}
		// Build random ops; removes only target names present in every
		// parent's expanded state so commits stay valid.
		present := make(map[string]bool)
		for _, n := range names {
			present[n] = true
		}
		for _, p := range parents {
			st := mustExpand(t, s, p)
			for n := range present {
				if _, ok := st[n]; !ok {
					delete(present, n)
				}
			}
		}
		var ops []Op
		for j := 0; j < 1+rng.Intn(3); j++ {
			if rng.Intn(3) == 0 && len(present) > 0 {
				var cand []string
				for n := range present {
					cand = append(cand, n)
				}
				n := cand[rng.Intn(len(cand))]
				ops = append(ops, Remove(n))
				delete(present, n)
			} else {
				n := names[rng.Intn(len(names))]
				ops = append(ops, Add(n, chunks[rng.Intn(len(chunks))]))
				present[n] = true
			}
		}
		commitBoth(id, parents, ops)
	}
	if err := s.ValidateDAG(); err != nil {
		t.Fatal(err)
	}
}
