package envguard

import (
	"strings"
	"testing"
)

func TestRemoveSanitizerDoesNotWashControl(t *testing.T) {
	g := newTestGraph(t)
	pub := NewPublisher(g)
	// Removes '/'; a classic "delete dangerous characters" sanitizer.
	san := NewRemoveSanitizer("strip-slash", []RuneRange{{'/', '/'}})
	if err := g.DeclareSanitizer(san); err != nil {
		t.Fatal(err)
	}
	v, _ := g.Var("tenantA", "name")
	clean, err := g.Sanitize("strip-slash", v)
	if err != nil {
		t.Fatal(err)
	}
	if materialize(g, clean) != "abc" {
		t.Fatal("remove sanitizer output wrong")
	}
	// Content is now host-vouched, so no content violation...
	if cv := g.checkContent(clean); cv != nil {
		t.Fatalf("remove sanitizer must vouch content, got %v", cv)
	}
	// ...but shape control remains, so export is still rejected.
	viol, err := pub.Check(Export{Name: "CLEAN", Value: clean})
	if err != nil {
		t.Fatal(err)
	}
	if viol == nil || viol.Kind != ViolationControl {
		t.Fatalf("deletion must not launder control, got %v", viol)
	}
}

func TestRemoveSanitizerOffsetMapping(t *testing.T) {
	g := NewGraph()
	mustBind(t, g, Input{
		Tenant: "t", Name: "x", Value: "a/b/c",
		Lang: MustCompileRegex(`[a-z/]*`),
	})
	san := NewRemoveSanitizer("strip", []RuneRange{{'/', '/'}})
	if err := g.DeclareSanitizer(san); err != nil {
		t.Fatal(err)
	}
	v, _ := g.Var("t", "x")
	clean, err := g.Sanitize("strip", v)
	if err != nil {
		t.Fatal(err)
	}
	if g.RuneLen(clean) != 3 {
		t.Fatalf("len=%d", g.RuneLen(clean))
	}
	for i, want := range []rune{'a', 'b', 'c'} {
		info, err := g.ProvenanceAt(clean, int64(i))
		if err != nil {
			t.Fatal(err)
		}
		if info.Rune != want {
			t.Fatalf("offset %d = %c want %c", i, info.Rune, want)
		}
		if info.Content[0].Tenant != "t" {
			t.Fatal("remove sanitizer must preserve surviving origin")
		}
	}
}

func TestFixedSanitizerWashesDataControl(t *testing.T) {
	g := newTestGraph(t)
	pub := NewPublisher(g)
	// Always emits a 3-rune, deterministic rewrite: output shape is constant.
	fixed := NewFixedSanitizer("fixed3", 3, func(seq RuneSeq, out int64) rune {
		seed := rune('0')
		if seq.RuneLen() > 0 {
			seed = seq.RuneAt(0)
		}
		return seed + rune(out)
	})
	if err := g.DeclareSanitizer(fixed); err != nil {
		t.Fatal(err)
	}
	v, _ := g.Var("tenantA", "name")
	clean, err := g.Sanitize("fixed3", v)
	if err != nil {
		t.Fatal(err)
	}
	if materialize(g, clean) != "abc" {
		t.Fatalf("fixed map output = %q", materialize(g, clean))
	}
	viol, err := pub.Check(Export{Name: "FIXED", Value: clean})
	if err != nil {
		t.Fatal(err)
	}
	if viol != nil {
		t.Fatalf("fixed-shape sanitizer must wash data control, got %v", viol)
	}
}

func TestFixedSanitizerKeepsBranchControl(t *testing.T) {
	g := newTestGraph(t)
	pub := NewPublisher(g)
	fixed := NewFixedSanitizer("fixed1", 1, func(RuneSeq, int64) rune { return 'z' })
	if err := g.DeclareSanitizer(fixed); err != nil {
		t.Fatal(err)
	}
	v, _ := g.Var("tenantA", "name")
	clean, _ := g.Sanitize("fixed1", v)
	other := lit(t, g, "q")
	// Untrusted selector still decides which branch exists.
	rope, err := g.Cond("tenantA", "mode", clean, other)
	if err != nil {
		t.Fatal(err)
	}
	viol, err := pub.Check(Export{Name: "BR", Value: rope})
	if err != nil {
		t.Fatal(err)
	}
	if viol == nil || viol.Kind != ViolationControl {
		t.Fatalf("branch control must survive fixed sanitizer, got %v", viol)
	}
}

func TestSanitizerDeclarationValidation(t *testing.T) {
	g := NewGraph()
	if err := g.DeclareSanitizer(&Sanitizer{Name: "bad"}); err == nil {
		t.Fatal("sanitizer without relation must be rejected")
	}
	if err := g.DeclareSanitizer(NewFixedSanitizer("neg", -1, nil)); err == nil {
		t.Fatal("negative fixed length must be rejected")
	}
}

func TestBoundedLoopAndLengthCeiling(t *testing.T) {
	g := newTestGraph(t)
	body := lit(t, g, "ab")
	loop, err := g.Loop(body, 4)
	if err != nil {
		t.Fatal(err)
	}
	if materialize(g, loop) != "abababab" || g.RuneLen(loop) != 8 {
		t.Fatal("loop expansion wrong")
	}
	// Graph stays small: one loop node addresses the whole expansion.
	if len(g.nodes) > 12 {
		t.Fatalf("loop must not materialize nodes, have %d", len(g.nodes))
	}
	if _, err := g.Loop(body, (1<<43)+1); err == nil {
		t.Fatal("loop beyond 2^44 must be rejected")
	}
}

func TestGiantLoopProvenanceAndChecks(t *testing.T) {
	g := NewGraph()
	mustBind(t, g, Input{
		Tenant: "t", Name: "x", Value: "z",
		Lang: MustCompileRegex(`z`),
	})
	body := lit(t, g, "0123456789abcdef") // 16 runes
	loop, err := g.Loop(body, 1<<20)      // 16 Mi runes, one node
	if err != nil {
		t.Fatal(err)
	}
	pub := NewPublisher(g)
	if v, _ := pub.Check(Export{Name: "BIG", Value: loop}); v != nil {
		t.Fatalf("trusted giant rope must pass, got %v", v)
	}
	info, err := g.ProvenanceAt(loop, (1<<20)*16-1)
	if err != nil {
		t.Fatal(err)
	}
	if info.Rune != 'f' || !info.Content[0].Literal {
		t.Fatalf("giant loop tail provenance wrong: %+v", info)
	}
	// Now splice a single untrusted rune in via concat and locate it without
	// expanding anything.
	v, _ := g.Var("t", "x")
	rope := cat(t, g, lit(t, g, "head"), v, loop)
	viol, err := pub.Check(Export{Name: "BIG2", Value: rope})
	if err != nil {
		t.Fatal(err)
	}
	if viol == nil || viol.From != 4 || viol.Kind != ViolationContent {
		t.Fatalf("want content violation at 4, got %+v", viol)
	}
}

func TestExplainRendersPerCharacter(t *testing.T) {
	g := newTestGraph(t)
	v, _ := g.Var("tenantA", "name")
	rope := cat(t, g, lit(t, g, "P-"), v)
	rows, err := g.Explain(rope)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatalf("explain rows=%d", len(rows))
	}
	if len(rows[0].Control) != 0 {
		t.Fatal("literal rune must carry no control origin")
	}
	if len(rows[2].Control) != 1 || !strings.Contains(rows[2].Control[0], "tenantA") {
		t.Fatalf("tenant rune control origin wrong: %+v", rows[2])
	}
	if !strings.Contains(RenderExplanation(rows), "literal(trusted)") {
		t.Fatal("explanation missing literal annotation")
	}
}

func TestBuildRejectsAndEnvRoundTrip(t *testing.T) {
	g := newTestGraph(t)
	pub := NewPublisher(g)
	good := lit(t, g, "/srv/app/conf")
	bad := cat(t, g, lit(t, g, "/srv/"), g.mustVar("tenantA", "name"))
	_, err := pub.Build([]Export{
		{Name: "GOOD", Value: good, IsPath: true,
			ApprovedPrefixes: []string{"/srv/app"}},
		{Name: "BAD", Value: bad, IsPath: true,
			ApprovedPrefixes: []string{"/srv"}},
	})
	if err == nil {
		t.Fatal("Build must reject untrusted export")
	}
	re, ok := err.(*RejectError)
	if !ok || re.Export != "BAD" {
		t.Fatalf("want RejectError for BAD, got %v", err)
	}
	env, err := pub.Build([]Export{
		{Name: "GOOD", Value: good, IsPath: true,
			ApprovedPrefixes: []string{"/srv/app"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := env.Get("GOOD"); !ok || v != "/srv/app/conf" {
		t.Fatalf("env round trip wrong: %q", v)
	}
}

func (g *Graph) mustVar(tenant, name string) NodeID {
	id, err := g.Var(tenant, name)
	if err != nil {
		panic(err)
	}
	return id
}
