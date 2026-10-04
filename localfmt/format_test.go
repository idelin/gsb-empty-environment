package localfmt

import (
	"strings"
	"testing"
)

func mustFormat(t *testing.T, src string, s, e int) *Result {
	t.Helper()
	res, err := FormatSelection(src, s, e)
	if err != nil {
		t.Fatalf("FormatSelection(%q, %d, %d): %v", src, s, e, err)
	}
	if !res.Verified {
		t.Fatalf("verification failed: %v", res.VerifyNotes)
	}
	return res
}

func TestExpandsSelectionInsideToken(t *testing.T) {
	src := "alpha+beta"
	// Selection cuts through the middle of the identifier "beta".
	idx := strings.Index(src, "beta") + 2
	res := mustFormat(t, src, idx, idx+1)
	if res.NodeKind != NIdent || src[res.NodeSpan[0]:res.NodeSpan[1]] != "beta" {
		t.Fatalf("expected expansion to ident beta, got %v %v", res.NodeKind, res.NodeSpan)
	}
	out := Apply(src, res.Patch)
	if out != src {
		t.Fatalf("formatting an already-canonical ident should be a no-op, got %q", out)
	}
}

func TestExpandsToMinimalCompleteNode(t *testing.T) {
	src := "a+b*c"
	// Select only the '*' operator: not a complete node, so the minimal
	// covering node is the product b*c, not the whole sum.
	star := strings.Index(src, "*")
	res := mustFormat(t, src, star, star+1)
	if res.NodeKind != NBinary || src[res.NodeSpan[0]:res.NodeSpan[1]] != "b*c" {
		t.Fatalf("expected minimal node b*c, got %v span %v", res.NodeKind, res.NodeSpan)
	}
}

func TestPatchLeavesRestOfFileUntouched(t *testing.T) {
	src := "foo( 1+2 ,3 )+bar( 4 ,5 )"
	// Select only the second call's argument list region.
	start := strings.Index(src, "bar")
	res := mustFormat(t, src, start, len(src))
	out := Apply(src, res.Patch)
	prefix := "foo( 1+2 ,3 )+"
	if !strings.HasPrefix(out, prefix) {
		t.Fatalf("bytes outside the patch changed: %q", out)
	}
	if !strings.Contains(out, "bar(4, 5)") {
		t.Fatalf("selected call not formatted canonically: %q", out)
	}
	// The first call keeps its sloppy spacing: no whole-file reformat.
	if !strings.Contains(out, "foo( 1+2 ,3 )") {
		t.Fatalf("unselected node was reformatted: %q", out)
	}
}

func TestAttachedWhitespaceNormalized(t *testing.T) {
	src := "a  +   b"
	res := mustFormat(t, src, 0, len(src))
	out := Apply(src, res.Patch)
	if out != "a + b" {
		t.Fatalf("got %q", out)
	}
}

func TestCommentsBetweenOperatorsPreserved(t *testing.T) {
	src := "a/* left */+/* right */b"
	res := mustFormat(t, src, 0, len(src))
	out := Apply(src, res.Patch)
	if out != "a /* left */ + /* right */ b" {
		t.Fatalf("comments not preserved in place: %q", out)
	}
	// Semantic check is part of VerifyNotes; mustFormat already asserted it.
	found := false
	for _, n := range res.VerifyNotes {
		if strings.Contains(n, "语义不变") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing semantic verification note: %v", res.VerifyNotes)
	}
}

func TestCommentInCallArgs(t *testing.T) {
	src := "f(a,/*keep*/b,  c)"
	res := mustFormat(t, src, 0, len(src))
	out := Apply(src, res.Patch)
	if out != "f(a, /*keep*/ b, c)" {
		t.Fatalf("got %q", out)
	}
}

func TestIdempotencyExplicit(t *testing.T) {
	src := "g(1,2)*h( 3 ,4 )"
	res := mustFormat(t, src, 0, len(src))
	out1 := Apply(src, res.Patch)
	res2, err := FormatSelection(out1, 0, len(out1))
	if err != nil {
		t.Fatal(err)
	}
	out2 := Apply(out1, res2.Patch)
	if out1 != out2 {
		t.Fatalf("not idempotent: %q -> %q", out1, out2)
	}
}

func TestUnaryMinus(t *testing.T) {
	src := "a*-b"
	res := mustFormat(t, src, 0, len(src))
	out := Apply(src, res.Patch)
	if out != "a * -b" {
		t.Fatalf("got %q", out)
	}
}

func TestLineCommentKeepsNewline(t *testing.T) {
	src := "a+// note\nb"
	res := mustFormat(t, src, 0, len(src))
	out := Apply(src, res.Patch)
	if !strings.Contains(out, "// note\n") {
		t.Fatalf("line comment swallowed following token: %q", out)
	}
	if _, _, err := Parse(out); err != nil {
		t.Fatalf("output does not reparse: %v", err)
	}
}

func TestNestedSelectionPicksInnermost(t *testing.T) {
	src := "f(a+g(1,2),b)"
	inner := strings.Index(src, "g(")
	res := mustFormat(t, src, inner, inner+len("g(1,2)"))
	if src[res.NodeSpan[0]:res.NodeSpan[1]] != "g(1,2)" {
		t.Fatalf("expected innermost call, got span %v", res.NodeSpan)
	}
	out := Apply(src, res.Patch)
	if out != "f(a+g(1, 2),b)" {
		t.Fatalf("unexpected result: %q", out)
	}
	if out[:strings.Index(out, "g(")] != "f(a+" || !strings.HasSuffix(out, ",b)") {
		t.Fatalf("bytes outside the patch changed: %q", out)
	}
}
