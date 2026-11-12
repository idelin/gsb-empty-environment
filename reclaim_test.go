package netsec

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func ids(prefix string, n int) []BlockID {
	out := make([]BlockID, n)
	for i := range out {
		out[i] = BlockID(fmt.Sprintf("%s%d", prefix, i))
	}
	return out
}

// TestSharedSubtreeDestructionSet commits two snapshots sharing a parent
// subtree and checks that revocation destroys exactly the exclusively
// reachable blocks at each step.
func TestSharedSubtreeDestructionSet(t *testing.T) {
	r := New()

	// snapA: a1 -> {a2, s}; shared subtree s -> {l1, l2}.
	// snapB: b1 -> {b2, s}; reuses the already retained shared subtree.
	if err := r.Commit("snapA", "a1", map[BlockID][]BlockID{
		"a1": {"a2", "s"},
		"a2": nil,
		"s":  {"l1", "l2"},
		"l1": nil,
		"l2": nil,
	}); err != nil {
		t.Fatalf("commit snapA: %v", err)
	}
	if err := r.Commit("snapB", "b1", map[BlockID][]BlockID{
		"b1": {"b2", "s"},
		"b2": nil,
	}); err != nil {
		t.Fatalf("commit snapB: %v", err)
	}

	got := r.Revoke("snapA")
	want := []BlockID{"a1", "a2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("revoke snapA destroyed %v, want %v", got, want)
	}
	for _, id := range []BlockID{"s", "l1", "l2", "b1", "b2"} {
		if !r.Retained(id) {
			t.Fatalf("block %q must survive revocation of snapA", id)
		}
	}

	got = r.Revoke("snapB")
	want = []BlockID{"b1", "b2", "l1", "l2", "s"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("revoke snapB destroyed %v, want %v", got, want)
	}
	if roots := r.Roots(); len(roots) != 0 {
		t.Fatalf("roots remaining: %v", roots)
	}
}

// TestMultiRootCycle places two authorization roots on a reference cycle
// and checks that keys survive until the last root on the cycle is revoked.
func TestMultiRootCycle(t *testing.T) {
	r := New()

	// Cycle x <-> y, plus tail y -> z. rootR2 enters the cycle at y.
	if err := r.Commit("rootR1", "x", map[BlockID][]BlockID{
		"x": {"y"},
		"y": {"x", "z"},
		"z": nil,
	}); err != nil {
		t.Fatalf("commit rootR1: %v", err)
	}
	if err := r.Commit("rootR2", "y", nil); err != nil {
		t.Fatalf("commit rootR2: %v", err)
	}

	if got := r.Revoke("rootR1"); len(got) != 0 {
		t.Fatalf("revoke rootR1 destroyed %v, want nothing: rootR2 still authorizes the cycle", got)
	}
	for _, id := range []BlockID{"x", "y", "z"} {
		if !r.Retained(id) {
			t.Fatalf("block %q must survive while rootR2 is live", id)
		}
	}

	got := r.Revoke("rootR2")
	want := []BlockID{"x", "y", "z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("revoke rootR2 destroyed %v, want %v", got, want)
	}
}

// TestCommitRejects checks that invalid commits are refused and leave the
// reclaimer state untouched.
func TestCommitRejects(t *testing.T) {
	r := New()
	if err := r.Commit("snap", "a", map[BlockID][]BlockID{"a": nil}); err != nil {
		t.Fatalf("commit snap: %v", err)
	}

	cases := []struct {
		name string
		root RootID
		tip  BlockID
		edge map[BlockID][]BlockID
		want error
	}{
		{"duplicate root", "snap", "b", map[BlockID][]BlockID{"b": nil}, ErrRootExists},
		{"duplicate block", "snap2", "a", map[BlockID][]BlockID{"a": nil}, ErrBlockExists},
		{"dangling edge", "snap3", "c", map[BlockID][]BlockID{"c": {"ghost"}}, ErrDanglingEdge},
		{"unknown tip", "snap4", "ghost", nil, ErrUnknownTip},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := r.Commit(tc.root, tc.tip, tc.edge); !errors.Is(err, tc.want) {
				t.Fatalf("commit error = %v, want %v", err, tc.want)
			}
		})
	}

	if roots := r.Roots(); !reflect.DeepEqual(roots, []RootID{"snap"}) {
		t.Fatalf("rejected commits changed roots: %v", roots)
	}
	if got := r.Revoke("snap"); !reflect.DeepEqual(got, []BlockID{"a"}) {
		t.Fatalf("revoke after rejected commits destroyed %v, want [a]", got)
	}
}

// TestRevokeUnknownRoot checks that revoking a missing root is a no-op.
func TestRevokeUnknownRoot(t *testing.T) {
	r := New()
	if got := r.Revoke("nope"); got != nil {
		t.Fatalf("revoke unknown root destroyed %v, want nil", got)
	}
}

// TestConcurrentCommitRevoke hammers the reclaimer with concurrent commits
// and revocations over a shared base snapshot, then verifies the retained
// set against an independently computed reachability union of the live
// roots. It exercises the mutual exclusion of commit and revoke; run with
// -race for the publication guarantee.
func TestConcurrentCommitRevoke(t *testing.T) {
	r := New()
	if err := r.Commit("base", "s", map[BlockID][]BlockID{
		"s":  {"l1", "l2"},
		"l1": nil,
		"l2": nil,
	}); err != nil {
		t.Fatalf("commit base: %v", err)
	}

	const snapshots = 32
	var wg sync.WaitGroup
	for i := 0; i < snapshots; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			root := RootID(fmt.Sprintf("snap%d", i))
			tip := BlockID(fmt.Sprintf("tip%d", i))
			if err := r.Commit(root, tip, map[BlockID][]BlockID{tip: {"s"}}); err != nil {
				t.Errorf("commit %q: %v", root, err)
				return
			}
			if i%2 == 0 {
				r.Revoke(root)
			}
		}(i)
	}
	wg.Wait()

	// Odd-numbered snapshots remain live; each retains its tip plus the
	// shared base subtree.
	wantRetained := map[BlockID]bool{"s": true, "l1": true, "l2": true}
	for i := 1; i < snapshots; i += 2 {
		wantRetained[BlockID(fmt.Sprintf("tip%d", i))] = true
	}
	for id := range wantRetained {
		if !r.Retained(id) {
			t.Fatalf("block %q should be retained", id)
		}
	}
	for i := 0; i < snapshots; i += 2 {
		if r.Retained(BlockID(fmt.Sprintf("tip%d", i))) {
			t.Fatalf("tip%d should have been destroyed with its root", i)
		}
	}

	// Revoking the base last must destroy exactly the shared subtree.
	for i := 1; i < snapshots; i += 2 {
		r.Revoke(RootID(fmt.Sprintf("snap%d", i)))
	}
	got := r.Revoke("base")
	want := []BlockID{"l1", "l2", "s"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("final revoke destroyed %v, want %v", got, want)
	}
}

// TestIdsHelper keeps the ids helper referenced for future table tests.
func TestIdsHelper(t *testing.T) {
	if got := ids("b", 2); !reflect.DeepEqual(got, []BlockID{"b0", "b1"}) {
		t.Fatalf("ids helper broken: %v", got)
	}
}
