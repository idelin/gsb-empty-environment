package capsum

import (
	"testing"

	"prescreen.local/ps-2237843e01/pathlang"
)

func rootProgram() *Program {
	return &Program{
		Name: "t",
		Roots: []Root{
			{Name: "ROOT", Version: "v1"},
			{Name: "TENANT", AliasOf: "ROOT", Version: "tenant-v1"},
		},
		MaxHandles: 4,
	}
}

func analyzeOrFail(t *testing.T, p *Program) *Result {
	t.Helper()
	r, err := Analyze(p, 0)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	return r
}

func grant(root string, kind GrantKind, comps ...string) Grant {
	return Grant{Root: root, Components: comps, Kind: kind}
}

func assertReachable(t *testing.T, r *Result, node string) *PointResult {
	t.Helper()
	pr := r.Points[node]
	if pr == nil || pr.Status != StatusReachable {
		t.Fatalf("node %q status = %v, want reachable", node, pr)
	}
	return pr
}

func assertUnreachable(t *testing.T, r *Result, node string) {
	t.Helper()
	pr := r.Points[node]
	if pr == nil || pr.Status != StatusUnreachable {
		t.Fatalf("node %q status = %v, want unreachable", node, pr)
	}
}

func word(root string, comps ...string) []string {
	return append([]string{root}, comps...)
}

// Scenario 1: a root directory's direct children are granted, while the
// directory itself and deeper descendants are not.
func TestRootDirectChild(t *testing.T) {
	p := rootProgram()
	p.Domains = []Domain{
		{Name: "D", Grants: []Grant{grant("ROOT", GrantChild, "data")}},
	}
	p.EntryNode = "main"
	p.EntryDomains = []string{"D"}
	p.Nodes = []string{"main", "use"}
	p.Edges = []Edge{
		{ID: "e1", From: "main", To: "use", Kind: EdgeLocal, Action: &FileAction{
			Name: "read", Root: "ROOT", Path: []string{"data", "f1"},
		}},
	}
	r := analyzeOrFail(t, p)
	pr := assertReachable(t, r, "use")

	child, _ := pathlang.BuildChild(r.Alphabet, r.Components, "ROOT", []string{"data"})
	self, _ := pathlang.BuildSelf(r.Alphabet, r.Components, "ROOT", []string{"data"})
	desc, _ := pathlang.BuildDesc(r.Alphabet, r.Components, "ROOT", []string{"data"})

	if !pathlang.Equal(pr.May, child) || !pathlang.Equal(pr.Must, child) {
		t.Fatalf("effective language is not the direct-child grant")
	}
	if !pathlang.IsEmpty(pathlang.Intersect(pr.May, self)) {
		t.Fatal("directory itself must not be accessible")
	}
	if pathlang.Subset(desc, pr.May) {
		t.Fatal("strict descendants must not be accessible")
	}
	if !pr.May.Accepts(word("ROOT", "data", "f1")) {
		t.Fatal("direct child file not accepted")
	}

	// Tenant alias version of the same resource stays separate.
	tenantChild, _ := pathlang.BuildChild(r.Alphabet, r.Components, "TENANT", []string{"data"})
	if !pathlang.IsEmpty(pathlang.Intersect(pr.May, tenantChild)) {
		t.Fatal("tenant alias resource must not be covered by ROOT grant")
	}
}

// Scenario 2: mutual recursion returns to distinct call sites and the frame
// domains intersect across the recursive stack without a depth bound.
func TestMutualRecursionDistinctReturnSites(t *testing.T) {
	p := rootProgram()
	p.Domains = []Domain{
		{Name: "A", Grants: []Grant{grant("ROOT", GrantDesc, "shared")}},
		{Name: "B", Grants: []Grant{
			grant("ROOT", GrantDesc, "shared"),
			grant("ROOT", GrantChild, "other"),
		}},
	}
	p.EntryNode = "main"
	p.EntryDomains = []string{"A"}
	p.Nodes = []string{"main", "f", "g", "backF", "backG"}
	p.Edges = []Edge{
		{ID: "c1", From: "main", To: "f", Kind: EdgeCall, Domains: []string{"B"}, PairID: "p1"},
		{ID: "r1", From: "f", To: "main", Kind: EdgeReturn, PairID: "p1"},
		{ID: "c2", From: "f", To: "g", Kind: EdgeCall, Domains: []string{"A"}, PairID: "p2"},
		{ID: "r2", From: "g", To: "backF", Kind: EdgeReturn, PairID: "p2"},
		{ID: "l1", From: "backF", To: "f", Kind: EdgeLocal},
		{ID: "c3", From: "g", To: "f", Kind: EdgeCall, Domains: []string{"B"}, PairID: "p3"},
		{ID: "r3", From: "f", To: "backG", Kind: EdgeReturn, PairID: "p3"},
		{ID: "l2", From: "backG", To: "g", Kind: EdgeLocal},
	}
	r := analyzeOrFail(t, p)

	// Every recursive stack includes both frame domains; the effective must
	// language is their intersection (the shared descendant grant).
	shared, _ := pathlang.BuildDesc(r.Alphabet, r.Components, "ROOT", []string{"shared"})
	other, _ := pathlang.BuildChild(r.Alphabet, r.Components, "ROOT", []string{"other"})
	for _, node := range []string{"f", "g", "backF", "backG"} {
		pr := assertReachable(t, r, node)
		if !pathlang.Subset(shared, pr.Must) {
			t.Fatalf("node %q must retain shared descendant across recursion", node)
		}
		if pathlang.Subset(other, pr.Must) {
			t.Fatalf("node %q must not guarantee B-only grant across frames", node)
		}
	}
	if r.Iterations == 0 {
		t.Fatal("fixpoint did not iterate")
	}
}

// Scenario 3: a handle is consumed once. A second consume at a reused consumer
// site must not see the slot again, and the post-time freeze is intersected
// with the consumer's current stack.
func TestConsumerReuseOneShot(t *testing.T) {
	p := rootProgram()
	p.Domains = []Domain{
		{Name: "Post", Grants: []Grant{grant("ROOT", GrantDesc, "box")}},
		{Name: "Cons", Grants: []Grant{
			grant("ROOT", GrantDesc, "box"),
			grant("ROOT", GrantChild, "cons-only"),
		}},
	}
	p.EntryNode = "main"
	p.EntryDomains = []string{"Post"}
	p.Nodes = []string{"main", "posted", "consumer", "task", "after"}
	p.Edges = []Edge{
		{ID: "post1", From: "main", To: "posted", Kind: EdgePost, Handle: "H"},
		{ID: "lc", From: "posted", To: "consumer", Kind: EdgeLocal},
		{ID: "cons1", From: "consumer", To: "task", Kind: EdgeConsume, Handle: "H", ConsumePairID: "cp1"},
		{ID: "tr1", From: "task", To: "after", Kind: EdgeTaskReturn, ConsumePairID: "cp1"},
		{ID: "loop", From: "after", To: "consumer", Kind: EdgeLocal},
	}
	r := analyzeOrFail(t, p)

	box, _ := pathlang.BuildDesc(r.Alphabet, r.Components, "ROOT", []string{"box"})
	consOnly, _ := pathlang.BuildChild(r.Alphabet, r.Components, "ROOT", []string{"cons-only"})

	taskPR := assertReachable(t, r, "task")
	// Task F = freeze(box desc) ∩ consumer-effective(box desc ∪ cons child).
	if !pathlang.Subset(box, taskPR.Must) {
		t.Fatal("task must retain the frozen box restriction")
	}
	if pathlang.Subset(consOnly, taskPR.May) {
		t.Fatal("consumer-only grant must not escape into the task")
	}

	// The handle is consumed exactly once: every reaching configuration at the
	// task must have the H bit clear, and no configuration may reach the task
	// with H still pending after a prior consumption.
	for _, mask := range taskPR.ReachedBy {
		if mask&1 != 0 {
			t.Fatal("consumed handle still present in task queue mask")
		}
	}

	// Re-entering the consumer after the task return leaves H absent, so the
	// reused consume site cannot fire a second time into the task.
	after := assertReachable(t, r, "after")
	for _, mask := range after.ReachedBy {
		if mask&1 != 0 {
			t.Fatal("handle reappeared after one-shot consumption")
		}
	}
}

// Scenario 4: two distinct post sources feed the same handle/program point.
// May is the union of their freezes; must is their intersection.
func TestTwoPostSourcesSamePoint(t *testing.T) {
	p := rootProgram()
	p.Domains = []Domain{
		{Name: "DA", Grants: []Grant{
			grant("ROOT", GrantChild, "a"),
			grant("ROOT", GrantChild, "shared"),
		}},
		{Name: "DB", Grants: []Grant{
			grant("ROOT", GrantChild, "b"),
			grant("ROOT", GrantChild, "shared"),
		}},
	}
	p.EntryNode = "main"
	p.EntryDomains = []string{"DA"}
	p.Nodes = []string{"main", "branchA", "branchB", "join", "sink", "consumer", "task", "back"}
	p.Edges = []Edge{
		{ID: "toA", From: "main", To: "branchA", Kind: EdgeLocal},
		// B source runs in a privileged frame so its freeze carries DB alone,
		// giving two genuinely distinct post-source languages.
		{ID: "toB", From: "main", To: "branchB", Kind: EdgePriv, Domains: []string{"DB"}, PairID: "pb"},
		{ID: "rb", From: "branchB", To: "join", Kind: EdgeReturn, PairID: "pb"},
		{ID: "aJoin", From: "branchA", To: "join", Kind: EdgeLocal},
		{ID: "postA", From: "join", To: "sink", Kind: EdgePost, Handle: "H"},
		// A second source posting the same handle before the join point.
		{ID: "postB", From: "branchB", To: "sink", Kind: EdgePost, Handle: "H"},
		{ID: "toCons", From: "sink", To: "consumer", Kind: EdgeLocal},
		{ID: "cons", From: "consumer", To: "task", Kind: EdgeConsume, Handle: "H", ConsumePairID: "cp"},
		{ID: "tr", From: "task", To: "back", Kind: EdgeTaskReturn, ConsumePairID: "cp"},
	}
	r := analyzeOrFail(t, p)

	childA, _ := pathlang.BuildChild(r.Alphabet, r.Components, "ROOT", []string{"a"})
	childB, _ := pathlang.BuildChild(r.Alphabet, r.Components, "ROOT", []string{"b"})
	shared, _ := pathlang.BuildChild(r.Alphabet, r.Components, "ROOT", []string{"shared"})

	hs := r.Handles["H"]
	if hs == nil || !hs.Present {
		t.Fatal("handle summary missing")
	}
	if !pathlang.Subset(childA, hs.May) || !pathlang.Subset(childB, hs.May) {
		t.Fatal("may freeze must include both post-source languages")
	}
	if pathlang.Subset(childA, hs.Must) || pathlang.Subset(childB, hs.Must) {
		t.Fatal("source-specific grants must not be guaranteed across both posts")
	}
	if !pathlang.Subset(shared, hs.Must) {
		t.Fatal("shared grant must be guaranteed by both post sources")
	}

	// The merged freeze still reaches the task and stays inside the may union.
	taskPR := assertReachable(t, r, "task")
	if pathlang.Subset(childA, taskPR.Must) {
		t.Fatal("task must not guarantee a single source's exclusive grant")
	}
}

// Scenario 5: a privileged frame swallows the ordinary synchronous stack
// restriction but cannot remove a task-carried (frozen) restriction.
func TestPrivilegeKeepsAsyncRestriction(t *testing.T) {
	p := rootProgram()
	p.Domains = []Domain{
		// Base and Frozen share the frozen region, so their synchronous-stack
		// intersection is non-empty; Frozen additionally names the exact
		// descendant region the task is frozen to.
		{Name: "Base", Grants: []Grant{grant("ROOT", GrantDesc, "frozen")}},
		{Name: "Frozen", Grants: []Grant{grant("ROOT", GrantDesc, "frozen")}},
		// Priv grants the frozen region plus an outside region. Inside the
		// task only the part intersecting the carried freeze may survive.
		{Name: "Priv", Grants: []Grant{
			grant("ROOT", GrantDesc, "frozen"),
			grant("ROOT", GrantDesc, "outside"),
		}},
	}
	p.EntryNode = "main"
	p.EntryDomains = []string{"Base"}
	p.Nodes = []string{"main", "frozenFrame", "posted", "consumer", "task", "priv", "privRet"}
	p.Edges = []Edge{
		// Narrow the synchronous stack to the Frozen frame before posting, so
		// the post freeze is Base ∩ Frozen (kept inside the frozen region).
		{ID: "cf", From: "main", To: "frozenFrame", Kind: EdgeCall, Domains: []string{"Frozen"}, PairID: "pf"},
		{ID: "rf", From: "frozenFrame", To: "posted", Kind: EdgeReturn, PairID: "pf"},
		{ID: "post", From: "posted", To: "consumer", Kind: EdgePost, Handle: "H"},
		{ID: "toCons", From: "posted", To: "consumer", Kind: EdgeLocal},
		{ID: "cons", From: "consumer", To: "task", Kind: EdgeConsume, Handle: "H", ConsumePairID: "cp"},
		// Inside the task, a privileged call resets the synchronous stack.
		{ID: "priv", From: "task", To: "priv", Kind: EdgePriv, Domains: []string{"Priv"}, PairID: "pp"},
		{ID: "pr", From: "priv", To: "privRet", Kind: EdgeReturn, PairID: "pp"},
		{ID: "taskLocal", From: "privRet", To: "task", Kind: EdgeLocal},
		{ID: "tr", From: "task", To: "main", Kind: EdgeTaskReturn, ConsumePairID: "cp"},
	}

	r := analyzeOrFail(t, p)

	frozenDesc, _ := pathlang.BuildDesc(r.Alphabet, r.Components, "ROOT", []string{"frozen"})
	outsideDesc, _ := pathlang.BuildDesc(r.Alphabet, r.Components, "ROOT", []string{"outside"})

	privPR := assertReachable(t, r, "priv")
	// The privileged frame's frozen-region grant is effective even though the
	// ordinary synchronous stack was truncated by privilege.
	if !pathlang.Subset(frozenDesc, privPR.May) {
		t.Fatal("privileged frame grant inside the frozen region was swallowed")
	}
	// The task-carried freeze F persists through privilege: the outside-region
	// part of the Priv grant must not become effective.
	if !pathlang.IsEmpty(pathlang.Intersect(privPR.May, outsideDesc)) {
		t.Fatal("task escaped the frozen restriction via privilege")
	}
	// Sanity: a non-privileged task node is also confined to the freeze.
	taskPR := assertReachable(t, r, "task")
	if !pathlang.IsEmpty(pathlang.Intersect(taskPR.May, outsideDesc)) {
		t.Fatal("non-privileged task node escaped the freeze")
	}
}
