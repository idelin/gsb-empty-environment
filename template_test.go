package logtmpl

import "testing"

func tmplOf(line string) *Template {
	return templateFromTokens(Tokenize(line), 0)
}

func TestDistanceTokenCountMismatch(t *testing.T) {
	a := tmplOf("a b c")
	b := tmplOf("a b")
	if _, ok := Distance(a, b); ok {
		t.Fatal("templates with different token counts must be incompatible")
	}
}

func TestDistanceIdentical(t *testing.T) {
	a := tmplOf("status = 200")
	b := tmplOf("status = 200")
	d, ok := Distance(a, b)
	if !ok || d != 0 {
		t.Fatalf("identical templates: d=%d ok=%v, want 0/true", d, ok)
	}
}

func TestDistanceNumberGeneralization(t *testing.T) {
	a := tmplOf("status = 200")
	b := tmplOf("status = 404")
	d, ok := Distance(a, b)
	if !ok || d != 2 {
		t.Fatalf("d=%d ok=%v, want 2/true", d, ok)
	}
	m, _, pos, ok := mergeTemplates(a, b)
	if !ok {
		t.Fatal("merge failed")
	}
	if got, want := m.Signature(), "status = <NUM>"; got != want {
		t.Fatalf("signature %q, want %q", got, want)
	}
	if len(pos) != 1 || pos[0] != 2 {
		t.Fatalf("generalized positions %v, want [2]", pos)
	}
}

func TestErrorCodeFamilyRules(t *testing.T) {
	a := tmplOf("ERR-100 disk full")
	b := tmplOf("ERR-200 disk full")
	m, _, _, ok := mergeTemplates(a, b)
	if !ok {
		t.Fatal("same-family error codes must merge")
	}
	if got, want := m.Signature(), "<ERR:ERR> disk full"; got != want {
		t.Fatalf("signature %q, want %q", got, want)
	}

	// Different families must not merge.
	c := tmplOf("E900 disk full")
	if _, ok := Distance(m, c); ok {
		t.Fatal("ERR family must not merge with E family")
	}

	// An error code is never generalized into a plain number.
	n := tmplOf("500 disk full")
	if _, ok := Distance(a, n); ok {
		t.Fatal("error code must not merge with plain number")
	}
}

func TestCrossTypeIncompatible(t *testing.T) {
	a := tmplOf("user alice logged in")
	b := tmplOf("user 123 logged in")
	if _, ok := Distance(a, b); ok {
		t.Fatal("WORD and NUM at the same position must be incompatible")
	}
}

func TestFrozenPositionZero(t *testing.T) {
	a := tmplOf("get user 1")
	b := tmplOf("put user 1")
	if _, _, _, ok := mergeTemplates(a, b); ok {
		t.Fatal("position 0 WORD is a forbidden-to-generalize field")
	}
}
