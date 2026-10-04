package main

import (
	"strings"
	"testing"
)

func run(t *testing.T, src string, start, end int) *Result {
	t.Helper()
	res, err := formatSelection(src, start, end)
	if err != nil {
		t.Fatalf("formatSelection(%q, %d, %d): %v", src, start, end, err)
	}
	if !res.Checks.OutsideUnchanged {
		t.Errorf("OutsideUnchanged=false for %q", src)
	}
	if !res.Checks.Idempotent {
		t.Errorf("Idempotent=false for %q (replacement %q)", src, res.Patch.Replacement)
	}
	if !res.Checks.SemanticsPreserved {
		t.Errorf("SemanticsPreserved=false for %q", src)
	}
	return res
}

// Selection cuts through tokens: it must grow to the smallest node, and the
// messy formatting outside that node must be left byte-for-byte alone.
func TestExpandFromTokenMiddle(t *testing.T) {
	src := "f(12+34,  5 )"
	// select "2+3": inside token 12 and inside token 34
	res := run(t, src, 3, 6)
	if res.Patch.Start != 2 || res.Patch.End != 7 {
		t.Errorf("patch span = [%d,%d), want [2,7)", res.Patch.Start, res.Patch.End)
	}
	if res.Patch.Replacement != "12 + 34" {
		t.Errorf("replacement = %q, want %q", res.Patch.Replacement, "12 + 34")
	}
	if res.Patched != "f(12 + 34,  5 )" {
		t.Errorf("patched = %q; bytes outside the node must stay untouched", res.Patched)
	}
	joined := strings.Join(res.Rationale, "\n")
	if !strings.Contains(joined, "cuts through") || !strings.Contains(joined, "BinaryExpr") {
		t.Errorf("rationale should explain token and node expansion, got:\n%s", joined)
	}
}

func TestWholeCall(t *testing.T) {
	src := "f( 1 ,2 )"
	res := run(t, src, 0, len(src))
	if res.Patch.Replacement != "f(1, 2)" {
		t.Errorf("replacement = %q, want %q", res.Patch.Replacement, "f(1, 2)")
	}
}

// Comments wedged between operands and operators must survive with the
// same token stream (semantics unchanged).
func TestCommentsAroundOperators(t *testing.T) {
	src := "1/*l*/+/*r*/2"
	res := run(t, src, 0, len(src))
	want := "1 /*l*/ + /*r*/ 2"
	if res.Patch.Replacement != want {
		t.Errorf("replacement = %q, want %q", res.Patch.Replacement, want)
	}
	if !res.Checks.SemanticsPreserved {
		t.Error("comment between operator and operand changed the token stream")
	}
}

func TestLineCommentInsideNode(t *testing.T) {
	src := "1 + // plus\n 2"
	res := run(t, src, 0, len(src))
	want := "1 + // plus\n2"
	if res.Patch.Replacement != want {
		t.Errorf("replacement = %q, want %q", res.Patch.Replacement, want)
	}
}

func TestCommentAfterOpenParen(t *testing.T) {
	src := "f( /*c*/ a )"
	res := run(t, src, 0, len(src))
	if res.Patch.Replacement != "f(/*c*/ a)" {
		t.Errorf("replacement = %q", res.Patch.Replacement)
	}
}

// Formatting a sub-expression must not normalize its surroundings.
func TestLocalityInsideParens(t *testing.T) {
	src := "( 1+2 )*3"
	res := run(t, src, 2, 5) // "1+2"
	if res.Patched != "( 1 + 2 )*3" {
		t.Errorf("patched = %q, want %q", res.Patched, "( 1 + 2 )*3")
	}
	res = run(t, src, 0, len(src))
	if res.Patch.Replacement != "(1 + 2) * 3" {
		t.Errorf("replacement = %q, want %q", res.Patch.Replacement, "(1 + 2) * 3")
	}
}

func TestUnaryMinus(t *testing.T) {
	src := "a*- -b"
	res := run(t, src, 0, len(src))
	if res.Patch.Replacement != "a * - -b" {
		t.Errorf("replacement = %q, want %q", res.Patch.Replacement, "a * - -b")
	}
}

// A cursor inside a token selects exactly that token's node.
func TestCursorInsideToken(t *testing.T) {
	src := "12+34"
	res := run(t, src, 1, 1)
	if res.Patch.Start != 0 || res.Patch.End != 2 || res.Patch.Replacement != "12" {
		t.Errorf("patch = %+v, want span [0,2) replacement %q", res.Patch, "12")
	}
}

// Re-running the tool on the patched output at the same span must be a no-op.
func TestSecondRunIsNoOp(t *testing.T) {
	src := "g( 1+2 ,f(3,4) )"
	res := run(t, src, 0, len(src))
	again, err := formatSelection(res.Patched, res.Patch.Start, res.Patch.Start+len(res.Patch.Replacement))
	if err != nil {
		t.Fatal(err)
	}
	if again.Patch.Original != again.Patch.Replacement {
		t.Errorf("second run changed %q to %q", again.Patch.Original, again.Patch.Replacement)
	}
	if again.Patched != res.Patched {
		t.Errorf("second run altered the file: %q -> %q", res.Patched, again.Patched)
	}
}

func TestSelectionOutsideExpression(t *testing.T) {
	src := "1+2   "
	if _, err := formatSelection(src, 4, 5); err == nil {
		t.Error("expected an error for a selection outside any expression")
	}
}

func TestNestedCallsAndCommas(t *testing.T) {
	src := "h(x,f( 1 ,2 ),( 3+4 ))"
	res := run(t, src, 0, len(src))
	want := "h(x, f(1, 2), (3 + 4))"
	if res.Patch.Replacement != want {
		t.Errorf("replacement = %q, want %q", res.Patch.Replacement, want)
	}
}
