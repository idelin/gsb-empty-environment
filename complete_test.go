package configcomplete

import (
	"reflect"
	"testing"
)

var valCats = []TokenCategory{CatLBrace, CatLBracket, CatString, CatNumber, CatTrue, CatFalse, CatNull}
var valCatsOrRBracket = []TokenCategory{CatLBrace, CatLBracket, CatString, CatNumber, CatTrue, CatFalse, CatNull, CatRBracket}
var inString = []TokenCategory{CatStringChar, CatEscape, CatStringEnd}

func TestCompleteTable(t *testing.T) {
	cases := []struct {
		prefix string
		cats   []TokenCategory
		suffix string
	}{
		{"", valCats, "0"},
		{"  \n", valCats, "0"},
		{"{", []TokenCategory{CatString, CatRBrace}, "}"},
		{"{}", nil, ""},
		{`{"a"`, []TokenCategory{CatColon}, ":0}"},
		{`{"a":`, valCats, "0}"},
		{`{"a":1`, []TokenCategory{CatComma, CatRBrace}, "}"},
		{`{"a":1,`, []TokenCategory{CatString}, `"":0}`},
		{`{"a":1,"b":2}`, nil, ""},
		{"[", valCatsOrRBracket, "]"},
		{"[]", nil, ""},
		{"[1", []TokenCategory{CatComma, CatRBracket}, "]"},
		{"[1,", valCats, "0]"},
		{"[[", valCatsOrRBracket, "]]"},
		{"[[]", []TokenCategory{CatComma, CatRBracket}, "]"},
		{`{"a":[{"k"`, []TokenCategory{CatColon}, ":0}]}"},
		{`{"a":"b`, inString, `"}`},
		{`{"a":"b\`, []TokenCategory{CatEscape}, `n"}`},
		{`{"a":"b\u`, []TokenCategory{CatEscapeHex}, `0000"}`},
		{`{"a":"b\u1`, []TokenCategory{CatEscapeHex}, `000"}`},
		{`{"a":"b\u12`, []TokenCategory{CatEscapeHex}, `00"}`},
		{`{"a":"b\u123`, []TokenCategory{CatEscapeHex}, `0"}`},
		{`{"a":"b\u1234`, inString, `"}`},
		{`{"a`, inString, `":0}`},
		{`{"`, inString, `":0}`},
		{`{"a":"`, inString, `"}`},
		{`["`, inString, `"]`},
		{`"ab`, inString, `"`},
		{`"ab"`, nil, ""},
		{"[tr", []TokenCategory{CatTrue}, "ue]"},
		{"[tru", []TokenCategory{CatTrue}, "e]"},
		{"[n", []TokenCategory{CatNull}, "ull]"},
		{"[fals", []TokenCategory{CatFalse}, "e]"},
		{"nul", []TokenCategory{CatNull}, "l"},
		{`{"a":t`, []TokenCategory{CatTrue}, "rue}"},
		{`{"a":n`, []TokenCategory{CatNull}, "ull}"},
		{"[1.", []TokenCategory{CatNumber}, "0]"},
		{"[1e", []TokenCategory{CatNumber}, "0]"},
		{"[1e+", []TokenCategory{CatNumber}, "0]"},
		{"[1E-", []TokenCategory{CatNumber}, "0]"},
		{"[-", []TokenCategory{CatNumber}, "0]"},
		{"[-0", []TokenCategory{CatComma, CatRBracket}, "]"},
		{"[0", []TokenCategory{CatComma, CatRBracket}, "]"},
		{"[12", []TokenCategory{CatComma, CatRBracket}, "]"},
		{"[1.5", []TokenCategory{CatComma, CatRBracket}, "]"},
		{"true", nil, ""},
		{"true ", nil, ""},
		{"[true,false,null]", nil, ""},
	}
	for _, c := range cases {
		res := Complete(c.prefix)
		if res.Conflict != nil {
			t.Errorf("Complete(%q): unexpected conflict at %d: %s", c.prefix, res.Conflict.Pos, res.Conflict.Msg)
			continue
		}
		if !reflect.DeepEqual(res.Categories, c.cats) && !(len(res.Categories) == 0 && len(c.cats) == 0) {
			t.Errorf("Complete(%q) categories = %v, want %v", c.prefix, res.Categories, c.cats)
		}
		if res.Suffix != c.suffix {
			t.Errorf("Complete(%q) suffix = %q, want %q", c.prefix, res.Suffix, c.suffix)
		}
		if !Valid(c.prefix + res.Suffix) {
			t.Errorf("Complete(%q): prefix+suffix %q is not valid", c.prefix, c.prefix+res.Suffix)
		}
	}
}

func TestConflicts(t *testing.T) {
	cases := []struct {
		text string
		pos  int
	}{
		{"@", 0},
		{"}", 0},
		{":", 0},
		{"{@}", 1},
		{"{,}", 1},
		{`{"a" 1}`, 5},
		{`{"a":}`, 5},
		{`{"a":1 "b"}`, 7},
		{`{"a":1,}`, 7},
		{`{"a":01}`, 6},
		{`{"a":"x\y"}`, 8},
		{`{"a":"\u12x4"}`, 10},
		{"{\"a\":\"\t\"}", 6},
		{"[1 2]", 3},
		{"[1,]", 3},
		{"[1}", 2},
		{"01", 1},
		{"1 2", 2},
		{"-x", 1},
		{"1.x", 2},
		{"1e+x", 3},
		{"nulx", 3},
		{"truex", 4},
		{"true false", 5},
		{"[tru ]", 4},
		{"{}}", 2},
		{"[]]", 2},
		{"\"a\" \"b\"", 4},
		{"\"a\nb\"", 2},
		{"{tr", 1},
		{"{1", 1},
		{"{1.", 1},
		{"[1 tr", 3},
		{`{"a" "b`, 5},
	}
	for _, c := range cases {
		res := Complete(c.text)
		if res.Conflict == nil {
			t.Errorf("Complete(%q): expected conflict at %d, got categories %v suffix %q",
				c.text, c.pos, res.Categories, res.Suffix)
			continue
		}
		if res.Conflict.Pos != c.pos {
			t.Errorf("Complete(%q) conflict at %d (%s), want %d", c.text, res.Conflict.Pos, res.Conflict.Msg, c.pos)
		}
	}
}

func TestCompleteAtUsesPrefixOnly(t *testing.T) {
	text := `{"a": [1, 2], "b": "x"}`
	for cursor := 0; cursor <= len(text); cursor++ {
		got := CompleteAt(text, cursor)
		want := Complete(text[:cursor])
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("CompleteAt(%q, %d) = %+v, want %+v", text, cursor, got, want)
		}
	}
	if got := CompleteAt(text, -5); !reflect.DeepEqual(got, Complete("")) {
		t.Fatalf("negative cursor not clamped: %+v", got)
	}
	if got := CompleteAt(text, len(text)+100); !reflect.DeepEqual(got, Complete(text)) {
		t.Fatalf("overflow cursor not clamped: %+v", got)
	}
}

func TestValid(t *testing.T) {
	valid := []string{
		`0`, `-1.5e+10`, `""`, `"a\nbA"`, `true`, `null`,
		`{}`, `[]`, `{"a":1}`, `[1,2,[3,{"x":null}]]`,
		"{\n\t\"k\" : [ true , \"v\" ]\n}",
	}
	for _, s := range valid {
		if !Valid(s) {
			t.Errorf("Valid(%q) = false, want true", s)
		}
	}
	invalid := []string{
		``, ` `, `{`, `[1,]`, `{"a"}`, `{"a":}`, `01`, `1.`, `.5`, `tru`,
		`{"a":1`, `}`, `nul`, `"`, "[1", `{"a":"\q"}`,
	}
	for _, s := range invalid {
		if Valid(s) {
			t.Errorf("Valid(%q) = true, want false", s)
		}
	}
}
