package pdfscan

import "testing"

func TestIsContiguousRef(t *testing.T) {
	cases := []struct {
		src  string
		want bool
	}{
		{"20 0 R /Length", true},
		{"1 5 2 /Index", false},
		{"5 2 ]", false},
		{"7 0 R>>", true},
	}
	for _, c := range cases {
		p := newParser([]byte(c.src))
		first, _ := p.peekAt(0)
		gen, _ := p.peekAt(1)
		r, _ := p.peekAt(2)
		got := isContiguousRef(p.lx.data, first, gen, r)
		if got != c.want {
			t.Errorf("%q: got %v want %v (gen kind=%d r kind=%d text=%q)",
				c.src, got, c.want, gen.kind, r.kind, r.text)
		}
	}
}

func TestNumberSequencesAreNotMistakenForRefs(t *testing.T) {
	cases := []struct {
		src string
	}{
		{"[1 5 2] /Index [0 1]"},
		{"<< /W [1 5 2] /Index [0 1 1 1 20 2] /Root 20 0 R >>"},
		{"20 0 R /Length 30"},
		{"7 0 R>>"},
		{"3.14 2 0 R"},
	}
	for _, c := range cases {
		p := newParser([]byte(c.src))
		v, err := p.parseValue()
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.src, err)
			continue
		}
		// Every entry must parse without consuming following tokens.
		if v.Kind == KindArray && len(v.Array) != 3 && c.src[0] == '[' {
			t.Errorf("%q: array length = %d, want 3", c.src, len(v.Array))
		}
	}
}

func TestRealNumberNotParsedAsRefGeneration(t *testing.T) {
	p := newParser([]byte("1.5 2 R"))
	v, err := p.parseValue()
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind != KindReal {
		t.Fatalf("expected real, got kind %d", v.Kind)
	}
}
