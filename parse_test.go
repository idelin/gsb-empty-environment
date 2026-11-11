package jsonpathstream

import "testing"

func TestParseValid(t *testing.T) {
	valid := []string{
		"$",
		"$.a",
		`$.a["b c"]`,
		"$[*]",
		"$.*",
		"$[0]",
		"$[-1]",
		"$[1:3]",
		"$[:5:2]",
		"$[2::2]",
		"$[::3]",
		"$..a",
		"$..*",
		"$..[0]",
		"$..book[?(@.price < 10)].title",
		`$.x[?(@.name == "bob")]`,
		"$[?(@.a != @.b)]",
		"$[?(@.v >= 1.5)]",
		"$[?(@.flag == true)]",
		"$[?(@.x == null)]",
		"$.a.b.c[0][*].d",
		"$..[?(@.id == 1)]",
	}
	for _, q := range valid {
		if _, err := Parse(q); err != nil {
			t.Errorf("Parse(%q) unexpected error: %v", q, err)
		}
	}
}

func TestParseInvalid(t *testing.T) {
	invalid := []string{
		"",
		"a.b",
		"$.",
		"$[",
		"$[]",
		"$[1:2:0]",
		"$[1:2:-1]",
		"$[?($.x == 1)]",
		"$[?(@.a)]",
		"$[?(@.a === 1)]",
		"$[abc]",
	}
	for _, q := range invalid {
		if _, err := Parse(q); err == nil {
			t.Errorf("Parse(%q) expected error, got nil", q)
		}
	}
}

func TestSliceStep(t *testing.T) {
	p, err := Parse("$[1:6:2]")
	if err != nil {
		t.Fatal(err)
	}
	seg := p.segments[0]
	if seg.kind != segSlice || seg.step != 2 {
		t.Fatalf("unexpected segment %+v", seg)
	}
	if *seg.start != 1 || *seg.end != 6 {
		t.Fatalf("bad bounds %+v", seg)
	}
}
