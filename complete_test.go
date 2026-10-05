package configcomplete

import (
	"reflect"
	"testing"
)

func sameCats(a, b []TokenCategory) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[TokenCategory]bool, len(a))
	for _, c := range a {
		set[c] = true
	}
	for _, c := range b {
		if !set[c] {
			return false
		}
	}
	return true
}

var valueCats = []TokenCategory{CatObjectStart, CatArrayStart, CatString, CatNumber, CatBoolean, CatNull}

func TestCompleteOK(t *testing.T) {
	cases := []struct {
		in     string
		cats   []TokenCategory
		suffix string
	}{
		// Document start / whitespace only.
		{"", valueCats, "1"},
		{"  \n", valueCats, "1"},
		// Object contexts.
		{"{", []TokenCategory{CatObjectEnd, CatString}, "}"},
		{`{"a`, []TokenCategory{CatString}, `":1}`},
		{`{"a"`, []TokenCategory{CatColon}, `:1}`},
		{`{"a":`, valueCats, `1}`},
		{`{"a":1`, []TokenCategory{CatObjectEnd, CatComma, CatNumber}, `}`},
		{`{"a":1.`, []TokenCategory{CatNumber}, `0}`},
		{`{"a":1,`, []TokenCategory{CatString}, `"":1}`},
		{`{"a":{"b":[1,2]}`, []TokenCategory{CatObjectEnd, CatComma}, `}`},
		// Array contexts.
		{"[", append(append([]TokenCategory{}, valueCats...), CatArrayEnd), "]"},
		{"[[", append(append([]TokenCategory{}, valueCats...), CatArrayEnd), "]]"},
		{"[1", []TokenCategory{CatArrayEnd, CatComma, CatNumber}, "]"},
		{"[1,", valueCats, "1]"},
		{"[[]", []TokenCategory{CatArrayEnd, CatComma}, "]"},
		// Scalars at top level.
		{"1", []TokenCategory{CatNumber}, ""},
		{"1 ", nil, ""},
		{"-0.5e+2", []TokenCategory{CatNumber}, ""},
		{`"abc"`, nil, ""},
		{"true", nil, ""},
		{"null ", nil, ""},
		// Open tokens that must be continued.
		{"-", []TokenCategory{CatNumber}, "0"},
		{"[1.", []TokenCategory{CatNumber}, "0]"},
		{"[1e", []TokenCategory{CatNumber}, "0]"},
		{"[1e+", []TokenCategory{CatNumber}, "0]"},
		{"[2.5E-", []TokenCategory{CatNumber}, "0]"},
		{`["ab`, []TokenCategory{CatString}, `"]`},
		{`["a\`, []TokenCategory{CatString}, `n"]`},
		{`["\u`, []TokenCategory{CatString}, `0000"]`},
		{`["\u12`, []TokenCategory{CatString}, `00"]`},
		{`"a`, []TokenCategory{CatString}, `"`},
		{"[tr", []TokenCategory{CatBoolean}, "ue]"},
		{"[tru", []TokenCategory{CatBoolean}, "e]"},
		{"[f", []TokenCategory{CatBoolean}, "alse]"},
		{"[fals", []TokenCategory{CatBoolean}, "e]"},
		{"[n", []TokenCategory{CatNull}, "ull]"},
		{"[nul", []TokenCategory{CatNull}, "l]"},
		{"[true", []TokenCategory{CatArrayEnd, CatComma}, "]"},
		{"[null,", valueCats, "1]"},
		// Complete documents: nothing legal to add.
		{"{}", nil, ""},
		{"[]", nil, ""},
		{`{"a":1,"b":[true,null,{"c":"d"}]}`, nil, ""},
		{`[1, 2.5, "x", false]`, nil, ""},
	}
	for _, c := range cases {
		r := Complete(c.in, len(c.in))
		if !r.OK {
			t.Errorf("Complete(%q): unexpected error %q at %d", c.in, r.Err, r.ErrOffset)
			continue
		}
		if !sameCats(r.Categories, c.cats) {
			t.Errorf("Complete(%q).Categories = %v, want %v", c.in, r.Categories, c.cats)
		}
		if r.Suffix != c.suffix {
			t.Errorf("Complete(%q).Suffix = %q, want %q", c.in, r.Suffix, c.suffix)
		}
	}
}

func TestCompleteError(t *testing.T) {
	cases := []struct {
		in  string
		off int
	}{
		{"@", 0},
		{"[@", 1},
		{"{]", 1},
		{"[}", 1},
		{"[1 2]", 3},
		{`{"a" 1}`, 5},
		{`{"a" "b"}`, 5},
		{`{"a":}`, 5},
		{`{"a":1,}`, 7},
		{`{"a":1 "b"}`, 7},
		{`{"a":1}]`, 7},
		{"truX", 3},
		{"tru]", 3},
		{"nulx", 3},
		{"truex", 4},
		{"[true false]", 6},
		{"-x", 1},
		{"1.x", 2},
		{"[01]", 2},
		{"1 2", 2},
		{"+1", 0},
		{".5", 0},
		{"1.2.3", 3},
		{"[1e+]", 4},
		{"[\"a\n\"]", 3},  // raw control char inside string
		{`["\q"]`, 3},     // invalid escape
		{`["\u12g4"]`, 6}, // bad hex digit
		{"\xff", 0},
	}
	for _, c := range cases {
		r := Complete(c.in, len(c.in))
		if r.OK {
			t.Errorf("Complete(%q): expected error, got categories %v suffix %q", c.in, r.Categories, r.Suffix)
			continue
		}
		if r.ErrOffset != c.off {
			t.Errorf("Complete(%q).ErrOffset = %d, want %d (%s)", c.in, r.ErrOffset, c.off, r.Err)
		}
	}
}

// The cursor is a byte offset into the original text; text after the
// cursor is ignored. Inside strings, positions count raw source bytes.
func TestCursorInString(t *testing.T) {
	doc := `{"k":"a\né"}`
	// byte layout: 0{ 1" 2k 3" 4: 5" 6a 7\ 8n 9é(2 bytes) 11" 12}
	cases := []struct {
		cursor int
		cats   []TokenCategory
		suffix string
	}{
		{5, valueCats, `1}`},                               // before value
		{6, []TokenCategory{CatString}, `"}`},              // right after opening quote
		{7, []TokenCategory{CatString}, `"}`},              // after 'a'
		{8, []TokenCategory{CatString}, `n"}`},             // between \ and n
		{9, []TokenCategory{CatString}, `"}`},              // after \n
		{10, []TokenCategory{CatString}, `"}`},             // mid UTF-8 rune
		{11, []TokenCategory{CatString}, `"}`},             // after é
		{12, []TokenCategory{CatObjectEnd, CatComma}, `}`}, // after closing quote
	}
	for _, c := range cases {
		r := Complete(doc, c.cursor)
		if !r.OK {
			t.Errorf("Complete(%q, %d): unexpected error %q at %d", doc, c.cursor, r.Err, r.ErrOffset)
			continue
		}
		if !sameCats(r.Categories, c.cats) {
			t.Errorf("Complete(%q, %d).Categories = %v, want %v", doc, c.cursor, r.Categories, c.cats)
		}
		if r.Suffix != c.suffix {
			t.Errorf("Complete(%q, %d).Suffix = %q, want %q", doc, c.cursor, r.Suffix, c.suffix)
		}
	}
}

func TestCursorOutOfRange(t *testing.T) {
	if r := Complete("{}", 3); r.OK {
		t.Error("expected error for cursor beyond text")
	}
	if r := Complete("{}", -1); r.OK {
		t.Error("expected error for negative cursor")
	}
}

func TestCategoryOrderDeterministic(t *testing.T) {
	for _, in := range []string{"", "[", "[1", `{"a":1`, "{"} {
		a := Complete(in, len(in))
		b := Complete(in, len(in))
		if !reflect.DeepEqual(a, b) {
			t.Errorf("Complete(%q) not deterministic: %v vs %v", in, a, b)
		}
	}
}
