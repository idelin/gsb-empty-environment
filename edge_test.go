package jsonpathstream

import (
	"strings"
	"testing"
)

func TestEdgeCases(t *testing.T) {
	cases := []struct {
		q, doc string
		n      int
	}{
		{"$", "42", 1},
		{"$", `"hello"`, 1},
		{"$", "true", 1},
		{"$", "null", 1},
		{"$", "[1,2,3]", 1},
		{"$[*]", "[1,2,3]", 3},
		{"$[?(@ > 2)]", "[1,2,3]", 1},
		{"$.nope", `{"a":1}`, 0},
		{"$..missing", `{"a":[{"b":1}]}`, 0},
	}
	for _, c := range cases {
		p, err := Parse(c.q)
		if err != nil {
			t.Fatalf("parse %q: %v", c.q, err)
		}
		doc := []byte(c.doc)
		got := streamEval(t, p, doc, 2)
		want := referenceEval(t, p, doc)
		assertMatches(t, c.q, got, want)
		if len(got) != c.n {
			t.Fatalf("%s: got %d want %d", c.q, len(got), c.n)
		}
	}
}

func TestMalformed(t *testing.T) {
	p, _ := Parse("$.a")
	err := EvaluateReader(p, strings.NewReader("{\"a\":"), func(Match) error { return nil })
	if err == nil {
		t.Fatal("expected error for truncated document")
	}
}
