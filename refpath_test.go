package jsonpathstream

import "testing"

func TestQuotedAndRefPaths(t *testing.T) {
	doc := []byte(`{"weird key":[{"with quote":42,"nested":[7,8]}]}`)
	cases := []struct {
		q    string
		path string
		val  any
	}{
		{`$["weird key"][0]["with quote"]`, `$["weird key"][0]["with quote"]`, float64(42)},
		{`$["weird key"][0].nested[1]`, "$[\"weird key\"][0].nested[1]", float64(8)},
	}
	for _, c := range cases {
		p, err := Parse(c.q)
		if err != nil {
			t.Fatalf("parse %q: %v", c.q, err)
		}
		want := referenceEval(t, p, doc)
		got := streamEval(t, p, doc, 3)
		assertMatches(t, c.q, got, want)
		if len(got) != 1 || got[0].Path != c.path || got[0].Value != c.val {
			t.Fatalf("%s: got %#v", c.q, got)
		}
	}
}

func TestFilterRefWithIndex(t *testing.T) {
	doc := []byte(`[{"vals":[3,9],"id":"a"},{"vals":[1,2],"id":"b"}]`)
	q := "$[?(@.vals[1] == 9)].id"
	p, err := Parse(q)
	if err != nil {
		t.Fatal(err)
	}
	got := streamEval(t, p, doc, 4)
	want := referenceEval(t, p, doc)
	assertMatches(t, q, got, want)
	if len(got) != 1 || got[0].Value != "a" {
		t.Fatalf("got %#v", got)
	}
}
