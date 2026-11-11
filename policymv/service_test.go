package policymv

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
)

const (
	P = Permission("P")
	Q = Permission("Q")
)

func TestPublishAtomicEdgeAndGrant(t *testing.T) {
	svc := New()
	v, err := svc.Publish(0, ChangeSet{
		AddEdges:  []Edge{{Child: "eng", Parent: "staff"}},
		AddGrants: []RoleGrants{{Role: "staff", Perms: []Permission{P}}},
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if v.ID() != 1 {
		t.Fatalf("id = %d, want 1", v.ID())
	}
	if !v.HasPermission("staff", P) || !v.HasPermission("eng", P) {
		t.Fatalf("direct and inherited permission both must be visible together")
	}
	if v.HasPermission("eng", Q) {
		t.Fatalf("unexpected permission Q")
	}
	if got := v.EffectivePerms("eng"); len(got) != 1 || got[0] != P {
		t.Fatalf("effective perms = %v, want [P]", got)
	}
}

func TestReaderPinsOldVersion(t *testing.T) {
	svc := New()
	v1, _ := svc.Publish(0, ChangeSet{
		AddGrants: []RoleGrants{{Role: "r", Perms: []Permission{P}}},
	})

	pinned, err := svc.Version(1)
	if err != nil {
		t.Fatalf("version: %v", err)
	}

	if _, err := svc.Publish(v1.ID(), ChangeSet{
		RemoveGrants: []RoleGrants{{Role: "r", Perms: []Permission{P}}},
		AddGrants:    []RoleGrants{{Role: "r", Perms: []Permission{Q}}},
	}); err != nil {
		t.Fatalf("second publish: %v", err)
	}

	if !pinned.HasPermission("r", P) || pinned.HasPermission("r", Q) {
		t.Fatalf("pinned version mutated by later publication: %v", pinned.EffectivePerms("r"))
	}
	head := svc.Current()
	if head.HasPermission("r", P) || !head.HasPermission("r", Q) {
		t.Fatalf("current version = %v, want [Q]", head.EffectivePerms("r"))
	}
}

func TestCycleRejection(t *testing.T) {
	cases := []struct {
		name   string
		seed   ChangeSet
		cyclic ChangeSet
	}{
		{
			name:   "self loop",
			seed:   ChangeSet{},
			cyclic: ChangeSet{AddEdges: []Edge{{Child: "a", Parent: "a"}}},
		},
		{
			name:   "two node loop",
			seed:   ChangeSet{AddEdges: []Edge{{Child: "a", Parent: "b"}}},
			cyclic: ChangeSet{AddEdges: []Edge{{Child: "b", Parent: "a"}}},
		},
		{
			name: "three node loop",
			seed: ChangeSet{AddEdges: []Edge{
				{Child: "a", Parent: "b"},
				{Child: "b", Parent: "c"},
			}},
			cyclic: ChangeSet{AddEdges: []Edge{{Child: "c", Parent: "a"}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := New()
			base := 0
			if len(tc.seed.AddEdges) > 0 {
				v, err := svc.Publish(0, tc.seed)
				if err != nil {
					t.Fatalf("seed: %v", err)
				}
				base = v.ID()
			}
			_, err := svc.Publish(base, tc.cyclic)
			if !errors.Is(err, ErrCycle) {
				t.Fatalf("err = %v, want ErrCycle", err)
			}
			if svc.Current().ID() != base {
				t.Fatalf("rejected publication must not create a version")
			}
			if _, err := svc.Publish(base, ChangeSet{
				AddGrants: []RoleGrants{{Role: "a", Perms: []Permission{P}}},
			}); err != nil {
				t.Fatalf("valid publish after rejection: %v", err)
			}
		})
	}
}

func TestMutationValidation(t *testing.T) {
	svc := New()
	edge := Edge{Child: "a", Parent: "b"}
	v1, err := svc.Publish(0, ChangeSet{
		AddEdges:  []Edge{edge},
		AddGrants: []RoleGrants{{Role: "b", Perms: []Permission{P}}},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := svc.Publish(v1.ID(), ChangeSet{AddEdges: []Edge{edge}}); !errors.Is(err, ErrDuplicateEdge) {
		t.Fatalf("duplicate edge: %v", err)
	}
	if _, err := svc.Publish(v1.ID(), ChangeSet{RemoveEdges: []Edge{{Child: "a", Parent: "x"}}}); !errors.Is(err, ErrMissingEdge) {
		t.Fatalf("missing edge: %v", err)
	}
	if _, err := svc.Publish(v1.ID(), ChangeSet{AddGrants: []RoleGrants{{Role: "b", Perms: []Permission{P}}}}); !errors.Is(err, ErrDuplicateGrant) {
		t.Fatalf("duplicate grant: %v", err)
	}
	if _, err := svc.Publish(v1.ID(), ChangeSet{RemoveGrants: []RoleGrants{{Role: "b", Perms: []Permission{Q}}}}); !errors.Is(err, ErrMissingGrant) {
		t.Fatalf("missing grant: %v", err)
	}
}

func TestAutoMergeDisjointClosures(t *testing.T) {
	svc := New()
	base, err := svc.Publish(0, ChangeSet{
		AddEdges: []Edge{
			{Child: "eng", Parent: "staff"},
			{Child: "fin", Parent: "staff"},
		},
	})
	if err != nil {
		t.Fatalf("base: %v", err)
	}

	adminA := ChangeSet{AddGrants: []RoleGrants{{Role: "eng", Perms: []Permission{P}}}}
	adminB := ChangeSet{AddGrants: []RoleGrants{{Role: "fin", Perms: []Permission{Q}}}}

	va, err := svc.Submit(base.ID(), adminA)
	if err != nil {
		t.Fatalf("admin A: %v", err)
	}
	vb, err := svc.Submit(base.ID(), adminB)
	if err != nil {
		t.Fatalf("admin B auto-merge: %v", err)
	}
	if vb.ID() != va.ID()+1 {
		t.Fatalf("merged version id = %d, want %d", vb.ID(), va.ID()+1)
	}

	head := svc.Current()
	if !head.HasPermission("eng", P) || !head.HasPermission("fin", Q) {
		t.Fatalf("both changes must be present after auto-merge: %v %v",
			head.EffectivePerms("eng"), head.EffectivePerms("fin"))
	}
	if head.HasPermission("eng", Q) || head.HasPermission("fin", P) {
		t.Fatalf("auto-merge leaked permissions across closures")
	}
}

func TestAutoMergeClosureViaInheritance(t *testing.T) {
	svc := New()
	base, err := svc.Publish(0, ChangeSet{
		AddEdges: []Edge{{Child: "leaf", Parent: "eng"}},
	})
	if err != nil {
		t.Fatalf("base: %v", err)
	}

	touchEng := ChangeSet{AddGrants: []RoleGrants{{Role: "eng", Perms: []Permission{P}}}}
	touchLeaf := ChangeSet{AddGrants: []RoleGrants{{Role: "leaf", Perms: []Permission{Q}}}}

	if _, err := svc.Submit(base.ID(), touchEng); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if _, err := svc.Submit(base.ID(), touchLeaf); !errors.Is(err, ErrDependencyConflict) {
		t.Fatalf("overlapping inherited closures: %v", err)
	}
	if svc.Current().ID() != base.ID()+1 {
		t.Fatalf("conflicting submit must not create a version")
	}
}

func TestPublishRejectsStaleBase(t *testing.T) {
	svc := New()
	v1, _ := svc.Publish(0, ChangeSet{AddGrants: []RoleGrants{{Role: "a", Perms: []Permission{P}}}})
	v2, _ := svc.Publish(v1.ID(), ChangeSet{AddGrants: []RoleGrants{{Role: "b", Perms: []Permission{Q}}}})

	_, err := svc.Publish(v1.ID(), ChangeSet{AddGrants: []RoleGrants{{Role: "c", Perms: []Permission{P}}}})
	if !errors.Is(err, ErrStaleBase) {
		t.Fatalf("stale publish: %v", err)
	}
	if svc.Current().ID() != v2.ID() {
		t.Fatalf("stale publish must not advance the version")
	}
	if _, err := svc.Version(99); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("unknown version: %v", err)
	}
}

func TestClosureIncludesDescendants(t *testing.T) {
	s := newSnapshot()
	s.addEdge(Edge{Child: "c1", Parent: "p"})
	s.addEdge(Edge{Child: "c2", Parent: "c1"})
	got := s.closure(ChangeSet{AddGrants: []RoleGrants{{Role: "p", Perms: []Permission{P}}}})
	var roles []string
	for r := range got {
		roles = append(roles, string(r))
	}
	sort.Strings(roles)
	want := []string{"c1", "c2", "p"}
	if fmt.Sprint(roles) != fmt.Sprint(want) {
		t.Fatalf("closure = %v, want %v", roles, want)
	}
}

// TestConcurrentReadersNoTransientPrivilege runs a publisher that toggles an
// inheritance edge and its parent grant together in every publication, while
// readers hammer Current(). Any observed version must be internally
// consistent: the child can only hold P through the edge, so its effective P
// must always match the parent's grant state. A half-applied publication
// would expose an instant where they disagree.
func TestConcurrentReadersNoTransientPrivilege(t *testing.T) {
	svc := New()

	install := ChangeSet{
		AddEdges:  []Edge{{Child: "child", Parent: "parent"}},
		AddGrants: []RoleGrants{{Role: "parent", Perms: []Permission{P}}},
	}
	const rounds = 200
	var violations int64

	var readers sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
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
					atomic.AddInt64(&violations, 1)
				}
				if v.HasPermission("outsider", P) {
					atomic.AddInt64(&violations, 1)
				}
			}
		}()
	}

	remove := ChangeSet{
		RemoveEdges:  []Edge{{Child: "child", Parent: "parent"}},
		RemoveGrants: []RoleGrants{{Role: "parent", Perms: []Permission{P}}},
	}
	base := 0
	for i := 0; i < rounds; i++ {
		installed, err := svc.Publish(base, install)
		if err != nil {
			t.Fatalf("install round %d: %v", i, err)
		}
		removed, err := svc.Publish(installed.ID(), remove)
		if err != nil {
			t.Fatalf("remove round %d: %v", i, err)
		}
		base = removed.ID()
	}

	pinned, err := svc.Version(1)
	if err != nil {
		t.Fatalf("pin: %v", err)
	}

	close(stop)
	readers.Wait()

	if atomic.LoadInt64(&violations) != 0 {
		t.Fatalf("observed %d transient privilege inconsistencies", violations)
	}
	if !pinned.HasPermission("child", P) {
		t.Fatalf("pinned version changed during concurrent publications")
	}
	if got := svc.Current().ID(); got != 2*rounds {
		t.Fatalf("final version = %d, want %d", got, 2*rounds)
	}
}

func TestConcurrentDisjointSubmitsAllMerge(t *testing.T) {
	svc := New()
	base, err := svc.Publish(0, ChangeSet{})
	if err != nil {
		t.Fatalf("base: %v", err)
	}

	const admins = 16
	var wg sync.WaitGroup
	errs := make(chan error, admins)
	for i := 0; i < admins; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			role := Role(fmt.Sprintf("role%02d", i))
			if _, err := svc.Submit(base.ID(), ChangeSet{
				AddGrants: []RoleGrants{{Role: role, Perms: []Permission{P}}},
			}); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("disjoint submit failed: %v", err)
	}

	head := svc.Current()
	if head.ID() != base.ID()+admins {
		t.Fatalf("versions = %d, want %d", head.ID(), base.ID()+admins)
	}
	for i := 0; i < admins; i++ {
		role := Role(fmt.Sprintf("role%02d", i))
		if !head.HasPermission(role, P) {
			t.Fatalf("role %s missing grant after merge", role)
		}
	}
}
