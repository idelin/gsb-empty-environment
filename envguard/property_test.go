package envguard

import (
	"math/rand"
	"path"
	"strings"
	"testing"
)

func TestNormalizeMatchesPathClean(t *testing.T) {
	g := NewGraph()
	samples := []string{
		"", "/", "//", "/a", "a//b", "./a", "a/./b", "a/b/..",
		"/a/../b", "a/../..", "/../x", "a//b/./c/../d", "..",
		"/srv//app/./x/../conf", "a/b/c/../../d",
	}
	rng := rand.New(rand.NewSource(1))
	alphabet := []rune{'a', 'b', '.', '/'}
	for i := 0; i < 500; i++ {
		n := rng.Intn(12)
		runes := make([]rune, n)
		for j := range runes {
			runes[j] = alphabet[rng.Intn(len(alphabet))]
		}
		samples = append(samples, string(runes))
	}
	for _, s := range samples {
		id, err := g.Literal(s)
		if err != nil {
			t.Fatal(err)
		}
		got := g.NormalizedPath(id)
		want := path.Clean(s)
		if got != want {
			t.Errorf("normalize(%q)=%q want %q", s, got, want)
		}
	}
}

func TestPathCheckStreamsOverGiantRope(t *testing.T) {
	g := NewGraph()
	seg, _ := g.Literal("ok/")
	giant, err := g.Loop(seg, 1<<12) // 12288 runes in one loop node
	if err != nil {
		t.Fatal(err)
	}
	root, _ := g.Literal("/srv/app/")
	rope, err := g.Concat(root, giant)
	if err != nil {
		t.Fatal(err)
	}
	pub := NewPublisher(g)
	if v, _ := pub.Check(Export{Name: "P", Value: rope, IsPath: true,
		ApprovedPrefixes: []string{"/srv/app"}}); v != nil {
		t.Fatalf("giant approved path must pass without expansion: %v", v)
	}
	// A single escaping segment at the far end is still located.
	evil, _ := g.Literal(strings.Repeat("../", 1<<13) + "etc/passwd")
	bad, _ := g.Concat(root, giant, evil)
	v, _ := pub.Check(Export{Name: "P2", Value: bad, IsPath: true,
		ApprovedPrefixes: []string{"/srv/app"}})
	if v == nil || v.Kind != ViolationPath {
		t.Fatalf("far-end escape must be reported, got %v", v)
	}
}

func TestCondControlOnlyOnDivergentRunes(t *testing.T) {
	g := newTestGraph(t)
	a := lit(t, g, "same-tail")
	b := lit(t, g, "same-head")
	rope, err := g.Cond("tenantA", "mode", a, b)
	if err != nil {
		t.Fatal(err)
	}
	// Prefix "same-" is identical on both branches: no selector control.
	for i := int64(0); i < 5; i++ {
		info, err := g.ProvenanceAt(rope, i)
		if err != nil {
			t.Fatal(err)
		}
		if len(info.Control) != 0 {
			t.Fatalf("rune %d shared by both branches must have no control origin: %+v", i, info)
		}
	}
	info, err := g.ProvenanceAt(rope, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Control) != 1 || info.Control[0].Name != "mode" {
		t.Fatalf("divergent rune must carry selector control, got %+v", info.Control)
	}
}

func TestTrustedInputPublishes(t *testing.T) {
	g := newTestGraph(t)
	pub := NewPublisher(g)
	v, _ := g.Var("host", "region")
	rope := cat(t, g, lit(t, g, "/srv/"), v)
	viol, err := pub.Check(Export{Name: "R", Value: rope, IsPath: true,
		ApprovedPrefixes: []string{"/srv/cn"}})
	if err != nil {
		t.Fatal(err)
	}
	if viol != nil {
		t.Fatalf("trusted binding under approved prefix must pass, got %v", viol)
	}
}

func TestIndependentlyBuiltEqualBranchesCollapse(t *testing.T) {
	g := newTestGraph(t)
	// Two builder calls produce the same interned literal node.
	a, _ := g.Literal("same")
	b, _ := g.Literal("same")
	if a != b {
		t.Fatal("literals must be hash-consed")
	}
	rope, err := g.Cond("tenantA", "mode", a, b)
	if err != nil {
		t.Fatal(err)
	}
	if rope != a {
		t.Fatal("equal branches must eliminate the condition")
	}
}

func TestControlViolationInsideLoop(t *testing.T) {
	g := newTestGraph(t)
	pub := NewPublisher(g)
	v, _ := g.Var("tenantA", "name")
	body, _ := g.Concat(lit(t, g, "ok-"), v)
	loop, err := g.Loop(body, 3)
	if err != nil {
		t.Fatal(err)
	}
	viol, err := pub.Check(Export{Name: "L", Value: loop})
	if err != nil {
		t.Fatal(err)
	}
	if viol == nil || viol.Kind != ViolationContent {
		t.Fatalf("want content violation inside loop, got %v", viol)
	}
	if viol.From != 3 {
		t.Fatalf("first untrusted rune at offset 3, got %d", viol.From)
	}
}

func TestExportRequiresPathForPrefixCheck(t *testing.T) {
	g := newTestGraph(t)
	pub := NewPublisher(g)
	rope := lit(t, g, "/etc/secret")
	// Without IsPath, only origin constraints apply and the trusted literal
	// passes even though no prefix was declared.
	if v, _ := pub.Check(Export{Name: "X", Value: rope}); v != nil {
		t.Fatalf("non-path export must skip prefix check, got %v", v)
	}
	if v, _ := pub.Check(Export{Name: "X", Value: rope, IsPath: true,
		ApprovedPrefixes: []string{"/srv"}}); v == nil || v.Kind != ViolationPath {
		t.Fatal("path export outside prefix must be rejected")
	}
}

func TestSanitizerRemoveWithBlockBoundary(t *testing.T) {
	g := NewGraph()
	// Alternate kept/deleted runes so checkpoints and scans are exercised.
	value := make([]rune, 0, 2*sanBlockSize+7)
	for i := 0; i < int(2*sanBlockSize+7); i++ {
		if i%2 == 0 {
			value = append(value, 'x')
		} else {
			value = append(value, '/')
		}
	}
	mustBind(t, g, Input{
		Tenant: "t", Name: "x", Value: string(value),
		Lang: MustCompileRegex(`[x/]*`),
	})
	if err := g.DeclareSanitizer(NewRemoveSanitizer("rm", []RuneRange{{'/', '/'}})); err != nil {
		t.Fatal(err)
	}
	v, _ := g.Var("t", "x")
	clean, err := g.Sanitize("rm", v)
	if err != nil {
		t.Fatal(err)
	}
	want := (int64(len(value)) + 1) / 2
	if g.RuneLen(clean) != want {
		t.Fatalf("len=%d want %d", g.RuneLen(clean), want)
	}
	for i := int64(0); i < want; i++ {
		if g.RuneAt(clean, i) != 'x' {
			t.Fatalf("offset %d wrong rune", i)
		}
	}
}
