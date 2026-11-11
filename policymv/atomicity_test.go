package policymv

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestNoHalfAppliedVersion is a torn-publication sentinel. In a correct store
// every observable version must satisfy, for the toggled (edge, grant) pair:
//
//	child holds P  <=>  parent holds P  <=>  parent grant exists
//
// The grant is the only source of P and the edge is the only path for the
// child to inherit it. A publication that became visible half applied would
// break one direction of this equivalence.
func TestNoHalfAppliedVersion(t *testing.T) {
	svc := New()

	install := ChangeSet{
		AddEdges:  []Edge{{Child: "child", Parent: "parent"}},
		AddGrants: []RoleGrants{{Role: "parent", Perms: []Permission{P}}},
	}
	remove := ChangeSet{
		RemoveEdges:  []Edge{{Child: "child", Parent: "parent"}},
		RemoveGrants: []RoleGrants{{Role: "parent", Perms: []Permission{P}}},
	}

	const rounds = 150
	var bad int64

	var readers sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 6; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				v := svc.Current()
				child := v.HasPermission("child", P)
				parent := v.HasPermission("parent", P)
				if child != parent {
					atomic.AddInt64(&bad, 1)
				}
			}
		}()
	}

	base := 0
	for i := 0; i < rounds; i++ {
		on, err := svc.Publish(base, install)
		if err != nil {
			t.Fatalf("install: %v", err)
		}
		if !on.HasPermission("child", P) || !on.HasPermission("parent", P) {
			t.Fatalf("installed version is half applied")
		}
		off, err := svc.Publish(on.ID(), remove)
		if err != nil {
			t.Fatalf("remove: %v", err)
		}
		if off.HasPermission("child", P) || off.HasPermission("parent", P) {
			t.Fatalf("removed version is half applied")
		}
		base = off.ID()
	}

	close(stop)
	readers.Wait()
	if atomic.LoadInt64(&bad) != 0 {
		t.Fatalf("readers observed %d torn versions", bad)
	}
}

// TestAutoMergeRebaseEdgeAddRemove proves disjoint edge operations on
// different roles rebase cleanly over an intervening publication.
func TestAutoMergeRebaseEdgeAddRemove(t *testing.T) {
	svc := New()
	base, err := svc.Publish(0, ChangeSet{
		AddEdges: []Edge{
			{Child: "a", Parent: "root"},
			{Child: "b", Parent: "root"},
		},
	})
	if err != nil {
		t.Fatalf("base: %v", err)
	}

	if _, err := svc.Submit(base.ID(), ChangeSet{
		AddGrants: []RoleGrants{{Role: "root", Perms: []Permission{P}}},
	}); err != nil {
		t.Fatalf("first submit: %v", err)
	}

	// Stale base; touches only the a/root edge subtree, disjoint from the
	// grant-only change above (whose closure is {root,a,b})... note root is
	// shared, so this must conflict instead.
	_, err = svc.Submit(base.ID(), ChangeSet{
		RemoveEdges: []Edge{{Child: "a", Parent: "root"}},
	})
	if err == nil {
		t.Fatalf("expected conflict: edge and grant closures share root")
	}

	// A change confined to a brand-new role has an empty overlap and merges.
	merged, err := svc.Submit(base.ID(), ChangeSet{
		AddEdges: []Edge{{Child: "c", Parent: "isolated"}},
	})
	if err != nil {
		t.Fatalf("disjoint edge add: %v", err)
	}
	if !merged.Exists("c") || !merged.Exists("isolated") {
		t.Fatalf("rebased edge add missing roles")
	}
}
