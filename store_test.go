package dedup

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustCommit(t *testing.T, s *Store, txID string, obj Object) CommitResult {
	t.Helper()
	res, err := s.Commit(txID, obj)
	if err != nil {
		t.Fatalf("commit %s: %v", txID, err)
	}
	return res
}

func TestDeduplication(t *testing.T) {
	s := New()
	a, b, c := []byte("alpha"), []byte("beta"), []byte("gamma")
	r1 := mustCommit(t, s, "tx1", Object{Root: "obj1", Blocks: [][]byte{a, b}})
	r2 := mustCommit(t, s, "tx2", Object{Root: "obj2", Blocks: [][]byte{b, c}})
	if r1.NewBlocks != 2 || r2.NewBlocks != 1 {
		t.Fatalf("new blocks: got %d and %d, want 2 and 1", r1.NewBlocks, r2.NewBlocks)
	}
	if s.BlockCount() != 3 {
		t.Fatalf("block count = %d, want 3", s.BlockCount())
	}
	if got := s.Refs(r1.Keys[1]); got != 2 {
		t.Fatalf("shared block refs = %d, want 2", got)
	}
	if r1.Keys[1] != r2.Keys[0] {
		t.Fatalf("shared content mapped to different keys: %s vs %s", r1.Keys[1], r2.Keys[0])
	}
}

func TestDuplicateCommit(t *testing.T) {
	s := New()
	obj := Object{Root: "obj1", Blocks: [][]byte{[]byte("alpha"), []byte("beta")}}
	first := mustCommit(t, s, "tx1", obj)
	second := mustCommit(t, s, "tx1", obj)
	if !second.Duplicate {
		t.Fatal("second commit with same txID not flagged as duplicate")
	}
	for i, key := range first.Keys {
		if got := s.Refs(key); got != 1 {
			t.Fatalf("block %d refs = %d after duplicate commit, want 1", i, got)
		}
	}
	if s.BlockCount() != 2 {
		t.Fatalf("block count = %d, want 2", s.BlockCount())
	}
	// Same txID with different content must not change state either.
	third := mustCommit(t, s, "tx1", Object{Root: "objX", Blocks: [][]byte{[]byte("evil")}})
	if !third.Duplicate || third.Root != "obj1" {
		t.Fatalf("conflicting txID reused: got %+v", third)
	}
	if s.BlockCount() != 2 {
		t.Fatalf("block count changed by reused txID: %d", s.BlockCount())
	}
	if _, err := s.Get("objX"); err != ErrUnknownRoot {
		t.Fatalf("objX should not exist, err = %v", err)
	}
}

func TestFaultIsolation(t *testing.T) {
	// Injected fault: strong hash always returns the same digest, so
	// distinct contents collide. The store must keep every copy.
	broken := func(content []byte) string { return "deadbeef" }
	s := New(WithStrongHash(broken))
	obj1 := Object{Root: "obj1", Blocks: [][]byte{[]byte("alpha"), []byte("beta")}}
	obj2 := Object{Root: "obj2", Blocks: [][]byte{[]byte("gamma"), []byte("delta")}}
	obj3 := Object{Root: "obj3", Blocks: [][]byte{[]byte("alpha")}}
	r1 := mustCommit(t, s, "tx1", obj1)
	r2 := mustCommit(t, s, "tx2", obj2)
	r3 := mustCommit(t, s, "tx3", obj3)
	if s.BlockCount() != 4 {
		t.Fatalf("block count = %d, want 4 isolated copies", s.BlockCount())
	}
	if len(s.Conflicts()) != 3 {
		t.Fatalf("conflicts = %d, want 3", len(s.Conflicts()))
	}
	// All keys distinct except the true duplicate "alpha".
	seen := map[string]bool{}
	for _, key := range append(append(r1.Keys, r2.Keys...), r3.Keys...) {
		seen[key] = true
	}
	if len(seen) != 4 {
		t.Fatalf("distinct keys = %d, want 4", len(seen))
	}
	if r3.Keys[0] != r1.Keys[0] {
		t.Fatalf("identical content not deduplicated: %s vs %s", r3.Keys[0], r1.Keys[0])
	}
	// Both conflicting copies remain retrievable and intact.
	for root, want := range map[string][][]byte{
		"obj1": {[]byte("alpha"), []byte("beta")},
		"obj2": {[]byte("gamma"), []byte("delta")},
		"obj3": {[]byte("alpha")},
	} {
		got, err := s.Get(root)
		if err != nil {
			t.Fatalf("get %s: %v", root, err)
		}
		for i := range want {
			if string(got[i]) != string(want[i]) {
				t.Fatalf("%s block %d = %q, want %q", root, i, got[i], want[i])
			}
		}
	}
	if got := s.Refs(r1.Keys[0]); got != 2 {
		t.Fatalf("alpha refs = %d, want 2", got)
	}
}

func TestCommitRollback(t *testing.T) {
	s := New()
	shared := []byte("shared")
	mustCommit(t, s, "tx0", Object{Root: "keeper", Blocks: [][]byte{shared}})
	keeperKey := mustCommit(t, s, "tx-probe", Object{Root: "probe", Blocks: [][]byte{shared}}).Keys[0]
	if err := s.Release("probe"); err != nil {
		t.Fatal(err)
	}
	before := s.Refs(keeperKey)
	// Fail at step 2 of a 3-block commit: steps 0 (shared, refs++) and
	// 1 (new block) must be rolled back.
	s.SetCommitFailHook(func(step int) error {
		if step == 2 {
			return errors.New("injected crash")
		}
		return nil
	})
	_, err := s.Commit("tx1", Object{Root: "obj1", Blocks: [][]byte{shared, []byte("new"), []byte("more")}})
	if err == nil {
		t.Fatal("expected injected failure")
	}
	s.SetCommitFailHook(nil)
	if got := s.Refs(keeperKey); got != before {
		t.Fatalf("shared refs = %d after rollback, want %d", got, before)
	}
	if s.BlockCount() != 1 {
		t.Fatalf("block count = %d after rollback, want 1", s.BlockCount())
	}
	if _, err := s.Get("obj1"); err != ErrUnknownRoot {
		t.Fatalf("obj1 should not exist after rollback, err = %v", err)
	}
	// The failed txID must not be treated as committed; retry succeeds.
	res := mustCommit(t, s, "tx1", Object{Root: "obj1", Blocks: [][]byte{shared, []byte("new"), []byte("more")}})
	if res.Duplicate {
		t.Fatal("retried commit wrongly marked duplicate")
	}
	if got := s.Refs(keeperKey); got != before+1 {
		t.Fatalf("shared refs = %d after retry, want %d", got, before+1)
	}
}

func TestNegativeRefProtection(t *testing.T) {
	s := New()
	res := mustCommit(t, s, "tx1", Object{Root: "obj1", Blocks: [][]byte{[]byte("alpha")}})
	key := res.Keys[0]
	if err := s.Release("obj1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Release("obj1"); err != ErrUnknownRoot {
		t.Fatalf("double release: err = %v, want ErrUnknownRoot", err)
	}
	if err := s.Release("ghost"); err != ErrUnknownRoot {
		t.Fatalf("unknown root: err = %v, want ErrUnknownRoot", err)
	}
	// Corrupt the count to zero, re-attach the object, and verify the
	// release is rejected without touching any count.
	mustCommit(t, s, "tx2", Object{Root: "obj2", Blocks: [][]byte{[]byte("alpha")}})
	s.mu.Lock()
	s.blocks[key].refs = 0
	s.mu.Unlock()
	if err := s.Release("obj2"); err == nil {
		t.Fatal("release should refuse to drive refcount negative")
	}
	if got := s.Refs(key); got != 0 {
		t.Fatalf("refs = %d, want 0 (release must be all-or-nothing)", got)
	}
	// Recount repairs the drift instead of letting it go negative.
	rep := s.Recount()
	if rep.Adjusted[key] != [2]int{0, 1} {
		t.Fatalf("recount adjustment = %v, want [0 1]", rep.Adjusted[key])
	}
}

func TestSweepCommitRace(t *testing.T) {
	s := New()
	shared := []byte("hot-block")
	mustCommit(t, s, "tx0", Object{Root: "seed", Blocks: [][]byte{shared}})
	if err := s.Release("seed"); err != nil {
		t.Fatal(err)
	}
	const n = 64
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			root := fmt.Sprintf("obj-%d", i)
			_, err := s.Commit(fmt.Sprintf("tx-%d", i), Object{
				Root:   root,
				Blocks: [][]byte{shared, []byte(fmt.Sprintf("unique-%d", i))},
			})
			if err != nil {
				errs <- err
			}
		}(i)
		go func() {
			defer wg.Done()
			s.Sweep()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	// Every committed object must be fully retrievable despite sweeps.
	for i := 0; i < n; i++ {
		got, err := s.Get(fmt.Sprintf("obj-%d", i))
		if err != nil {
			t.Fatalf("get obj-%d: %v", i, err)
		}
		if string(got[0]) != string(shared) {
			t.Fatalf("obj-%d shared block = %q, want %q", i, got[0], shared)
		}
	}
	// Reference counts must match an independent recount exactly.
	rep := s.Recount()
	if len(rep.Adjusted) != 0 || len(rep.Missing) != 0 {
		t.Fatalf("recount after race: adjusted=%v missing=%v", rep.Adjusted, rep.Missing)
	}
}

func TestRecount(t *testing.T) {
	s := New()
	r1 := mustCommit(t, s, "tx1", Object{Root: "obj1", Blocks: [][]byte{[]byte("a"), []byte("b")}})
	mustCommit(t, s, "tx2", Object{Root: "obj2", Blocks: [][]byte{[]byte("b"), []byte("c")}})
	// Corrupt counts and add an orphaned block with a bogus count.
	s.mu.Lock()
	s.blocks[r1.Keys[0]].refs = 99
	s.blocks[r1.Keys[1]].refs = 0
	orphan, _ := s.putLocked([]byte("orphan"))
	s.blocks[orphan].refs = 5
	s.mu.Unlock()
	rep := s.Recount()
	if len(rep.Missing) != 0 {
		t.Fatalf("missing = %v, want none", rep.Missing)
	}
	if rep.Adjusted[r1.Keys[0]] != [2]int{99, 1} {
		t.Fatalf("adjustment for a = %v, want [99 1]", rep.Adjusted[r1.Keys[0]])
	}
	if rep.Adjusted[r1.Keys[1]] != [2]int{0, 2} {
		t.Fatalf("adjustment for b = %v, want [0 2]", rep.Adjusted[r1.Keys[1]])
	}
	if rep.Adjusted[orphan] != [2]int{5, 0} {
		t.Fatalf("adjustment for orphan = %v, want [5 0]", rep.Adjusted[orphan])
	}
	// Orphan now has zero refs and is collectable; live blocks survive.
	collected := s.Sweep()
	if len(collected) != 1 || collected[0] != orphan {
		t.Fatalf("sweep collected %v, want [%s]", collected, orphan)
	}
	if s.BlockCount() != 3 {
		t.Fatalf("block count = %d, want 3", s.BlockCount())
	}
	if rep := s.Recount(); len(rep.Adjusted) != 0 {
		t.Fatalf("second recount adjusted %v, want clean", rep.Adjusted)
	}
}
