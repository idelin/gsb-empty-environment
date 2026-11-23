package envguard

import "testing"

func newTestGraph(t *testing.T) *Graph {
	t.Helper()
	g := NewGraph()
	lang := MustCompileRegex(`[a-z0-9./_-]*`)
	mustBind(t, g, Input{
		Tenant: "tenantA", Domain: "guest", Name: "mode",
		Value: "long", Lang: MustCompileRegex(`short|long`), Trusted: false,
	})
	mustBind(t, g, Input{
		Tenant: "tenantA", Domain: "guest", Name: "name",
		Value: "abc", Lang: lang, Trusted: false,
	})
	mustBind(t, g, Input{
		Tenant: "tenantB", Domain: "guest", Name: "name",
		Value: "xyz", Lang: lang, Trusted: false,
	})
	mustBind(t, g, Input{
		Tenant: "host", Domain: "infra", Name: "region",
		Value: "cn", Lang: MustCompileRegex(`cn|us`), Trusted: true,
	})
	return g
}

func mustBind(t *testing.T, g *Graph, in Input) {
	t.Helper()
	if err := g.Bind(in); err != nil {
		t.Fatalf("bind %s/%s: %v", in.Tenant, in.Name, err)
	}
}

func lit(t *testing.T, g *Graph, s string) NodeID {
	t.Helper()
	id, err := g.Literal(s)
	if err != nil {
		t.Fatalf("literal %q: %v", s, err)
	}
	return id
}

func cat(t *testing.T, g *Graph, parts ...NodeID) NodeID {
	t.Helper()
	id, err := g.Concat(parts...)
	if err != nil {
		t.Fatalf("concat: %v", err)
	}
	return id
}

func TestRegexAutomaton(t *testing.T) {
	d := MustCompileRegex(`a(b|c)*d`)
	cases := map[string]bool{
		"ad":     true,
		"abcbcd": true,
		"abd":    true,
		"axd":    false,
		"":       false,
		"add":    false,
	}
	for in, want := range cases {
		if got := d.AcceptsString(in); got != want {
			t.Errorf("AcceptsString(%q)=%v want %v", in, got, want)
		}
	}
	if !MustCompileRegex(`[0-9]+`).AcceptsString("12345") {
		t.Fatal("class regex failed")
	}
	if MustCompileRegex(`[^0-9]+`).AcceptsString("12a") {
		t.Fatal("negated class should reject")
	}
	if !MustCompileRegex(`\w+`).AcceptsString("a_B9") {
		t.Fatal("shorthand failed")
	}
}

func TestBindRejectsLanguageViolation(t *testing.T) {
	g := NewGraph()
	err := g.Bind(Input{
		Tenant: "t", Name: "x", Value: "AB!",
		Lang: MustCompileRegex(`[a-z]*`),
	})
	if err == nil {
		t.Fatal("expected language violation at bind")
	}
}

func TestContentOriginRejection(t *testing.T) {
	g := newTestGraph(t)
	pub := NewPublisher(g)
	v, err := g.Var("tenantA", "name")
	if err != nil {
		t.Fatal(err)
	}
	pre := lit(t, g, "/srv/")
	rope := cat(t, g, pre, v)
	viol, err := pub.Check(Export{Name: "PATH", Value: rope, IsPath: true,
		ApprovedPrefixes: []string{"/srv"}})
	if err != nil {
		t.Fatal(err)
	}
	if viol == nil || viol.Kind != ViolationContent || viol.From != 5 {
		t.Fatalf("want content violation at 5, got %+v", viol)
	}
}

// Equal branches erase the selector's control dependency even though the
// condition itself reads an untrusted input.
func TestCondEqualBranchesWashControl(t *testing.T) {
	g := newTestGraph(t)
	pub := NewPublisher(g)
	k := lit(t, g, "constant")
	rope, err := g.Cond("tenantA", "mode", k, k)
	if err != nil {
		t.Fatal(err)
	}
	if rope != k {
		t.Fatal("equal branches must intern to the same node and drop the cond")
	}
	viol, err := pub.Check(Export{Name: "MODE", Value: rope})
	if err != nil {
		t.Fatal(err)
	}
	if viol != nil {
		t.Fatalf("constant rope must pass, got %v", viol)
	}
}

// Branches made only of trusted constants still fail when an untrusted
// selector changes which length is produced: the characters are trusted but
// their presence/position is tenant-controlled.
func TestCondBranchLengthOnlyControlViolation(t *testing.T) {
	g := newTestGraph(t)
	pub := NewPublisher(g)
	short := lit(t, g, "abc")
	long := lit(t, g, "abcdef")
	rope, err := g.Cond("tenantA", "mode", long, short)
	if err != nil {
		t.Fatal(err)
	}
	viol, err := pub.Check(Export{Name: "MODE", Value: rope})
	if err != nil {
		t.Fatal(err)
	}
	if viol == nil {
		t.Fatal("expected control violation")
	}
	if viol.Kind != ViolationControl || viol.From != 3 {
		t.Fatalf("want control violation at 3 (first rune only on long branch), got %+v", viol)
	}
	// A trusted selector makes the same shape publishable.
	ok2, err := g.Cond("host", "region", long, short)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := pub.Check(Export{Name: "MODE2", Value: ok2}); v != nil {
		t.Fatalf("trusted selector must pass, got %v", v)
	}
}

func TestSliceAcrossSharedNode(t *testing.T) {
	g := newTestGraph(t)
	shared := lit(t, g, "abcdef")
	other := lit(t, g, "XY")
	rope := cat(t, g, shared, other, shared)
	sliced, err := g.Slice(rope, 4, 10)
	if err != nil {
		t.Fatal(err)
	}
	got := materialize(g, sliced)
	if got != "efXYab" {
		t.Fatalf("slice across shared node = %q", got)
	}
	info, err := g.ProvenanceAt(sliced, 3) // 'X' inside the middle node
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Content) != 1 || !info.Content[0].Literal {
		t.Fatalf("want literal provenance, got %+v", info.Content)
	}
	info, _ = g.ProvenanceAt(sliced, 4) // 'a' of the second shared copy
	if info.Rune != 'a' || len(info.Content) != 1 || !info.Content[0].Literal {
		t.Fatalf("shared-node second copy provenance wrong: %+v", info)
	}
}

func TestSameNameAcrossTenants(t *testing.T) {
	g := newTestGraph(t)
	a, err := g.Var("tenantA", "name")
	if err != nil {
		t.Fatal(err)
	}
	b, err := g.Var("tenantB", "name")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("same-named bindings across tenants must be distinct nodes")
	}
	if materialize(g, a) != "abc" || materialize(g, b) != "xyz" {
		t.Fatal("tenant namespace resolution wrong")
	}
	info, err := g.ProvenanceAt(a, 0)
	if err != nil {
		t.Fatal(err)
	}
	if info.Content[0].Tenant != "tenantA" {
		t.Fatalf("provenance lost tenant: %+v", info.Content[0])
	}
}

func TestFoldPreservesProvenance(t *testing.T) {
	g := newTestGraph(t)
	v, _ := g.Var("tenantA", "name")
	folded, err := g.Fold(v)
	if err != nil {
		t.Fatal(err)
	}
	if materialize(g, folded) != "abc" {
		t.Fatal("fold changed lowercase input")
	}
	up, _ := g.Literal("ABC")
	upF, _ := g.Fold(up)
	if materialize(g, upF) != "abc" {
		t.Fatal("ascii fold failed")
	}
	info, _ := g.ProvenanceAt(upF, 0)
	if !info.Content[0].Literal || info.Rune != 'a' {
		t.Fatalf("fold must preserve origin, got %+v", info)
	}
}

func TestPathNormalizationAndPrefix(t *testing.T) {
	g := newTestGraph(t)
	pub := NewPublisher(g)
	approved := []string{"/srv/app"}
	cases := []struct {
		path     string
		wantNorm string
		pass     bool
	}{
		{"/srv/app/conf", "/srv/app/conf", true},
		{"/srv//app/./conf", "/srv/app/conf", true},
		{"/srv/app/x/../conf", "/srv/app/conf", true},
		{"/srv/app/../../etc/passwd", "/etc/passwd", false},
		{"/srv/appx", "/srv/appx", false}, // boundary forms a new segment
		{"rel/../escape", "escape", false},
	}
	for _, tc := range cases {
		rope := lit(t, g, tc.path)
		if got := g.NormalizedPath(rope); got != tc.wantNorm {
			t.Errorf("normalize(%q)=%q want %q", tc.path, got, tc.wantNorm)
		}
		v, err := pub.Check(Export{Name: "P", Value: rope, IsPath: true,
			ApprovedPrefixes: approved})
		if err != nil {
			t.Fatal(err)
		}
		if tc.pass && v != nil {
			t.Errorf("path %q should pass, got %v", tc.path, v)
		}
		if !tc.pass && (v == nil || v.Kind != ViolationPath) {
			t.Errorf("path %q should fail path constraint, got %v", tc.path, v)
		}
	}
}

// A new path segment formed exactly at a string boundary must not inherit
// the approved prefix.
func TestPathBoundarySegment(t *testing.T) {
	g := newTestGraph(t)
	pub := NewPublisher(g)
	base := lit(t, g, "/srv/app")
	suffix := lit(t, g, "x/file")
	rope := cat(t, g, base, suffix) // "/srv/appx/file"
	v, _ := pub.Check(Export{Name: "P", Value: rope, IsPath: true,
		ApprovedPrefixes: []string{"/srv/app"}})
	if v == nil || v.Kind != ViolationPath {
		t.Fatalf("boundary segment must fail prefix, got %v", v)
	}
	if g.RuneAt(rope, v.From) != 'a' || v.To-v.From != 4 {
		t.Fatalf("violation must point at segment appx, range [%d,%d)", v.From, v.To)
	}
	safe := cat(t, g, base, lit(t, g, "/x/file"))
	if v, _ := pub.Check(Export{Name: "P2", Value: safe, IsPath: true,
		ApprovedPrefixes: []string{"/srv/app"}}); v != nil {
		t.Fatalf("slash-separated continuation must pass, got %v", v)
	}
}
