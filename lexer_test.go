package tpllex

import (
	"reflect"
	"strings"
	"testing"
)

// lexAll feeds the input in the given chunks, closes, and drains all tokens.
func lexAll(t *testing.T, cfg Config, chunks ...string) []Token {
	t.Helper()
	l := New(cfg)
	for _, c := range chunks {
		l.Feed([]byte(c))
	}
	l.Close()
	var toks []Token
	for {
		tok, ok := l.Next()
		if !ok {
			t.Fatal("Next reported need-input after Close")
		}
		toks = append(toks, tok)
		if tok.Kind == KindEOF {
			return toks
		}
	}
}

// summary renders tokens as "Kind:Val" pairs for compact comparison.
func summary(toks []Token) []string {
	var out []string
	for _, tok := range toks {
		out = append(out, tok.Kind.String()+":"+tok.Val)
	}
	return out
}

// checkTiling verifies that token raw intervals exactly tile the input and
// that delimiter tokens mirror their raw source bytes.
func checkTiling(t *testing.T, input string, toks []Token) {
	t.Helper()
	pos := 0
	for _, tok := range toks {
		switch tok.Kind {
		case KindEOF, KindError:
			continue
		}
		if tok.Start != pos {
			t.Errorf("token %v starts at %d, want %d (gap or overlap in raw spans)", tok.Kind, tok.Start, pos)
		}
		if tok.End < tok.Start || tok.End > len(input) {
			t.Errorf("token %v has invalid span [%d:%d]", tok.Kind, tok.Start, tok.End)
		}
		switch tok.Kind {
		case KindExprOpen, KindExprClose, KindRawOpen, KindRawClose:
			if input[tok.Start:tok.End] != tok.Val {
				t.Errorf("delimiter token Val %q != raw interval %q", tok.Val, input[tok.Start:tok.End])
			}
		}
		pos = tok.End
	}
	if pos != len(input) {
		t.Errorf("tokens cover %d bytes, input has %d", pos, len(input))
	}
}

func TestModeSwitchingAndEscapes(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "plain text and expression",
			input: "hello {{ name }} world",
			want: []string{
				"Text:hello ", "ExprOpen:{{", "Expr: name ", "ExprClose:}}",
				"Text: world", "EOF:",
			},
		},
		{
			name:  "raw string suppresses expression delimiters",
			input: "a [[ x {{ y }} ]] b",
			want: []string{
				"Text:a ", "RawOpen:[[", "Raw: x {{ y }} ", "RawClose:]]",
				"Text: b", "EOF:",
			},
		},
		{
			name:  "nested raw strings",
			input: "[[ a [[ b ]] c ]]",
			want: []string{
				"RawOpen:[[", "Raw: a ", "RawOpen:[[", "Raw: b ",
				"RawClose:]]", "Raw: c ", "RawClose:]]", "EOF:",
			},
		},
		{
			name:  "escape applies in text mode",
			input: `\{{ not an expr }}`,
			want:  []string{"Text:{{ not an expr }}", "EOF:"},
		},
		{
			name:  "escape applies in expression mode",
			input: `{{ a \}} b }}`,
			want:  []string{"ExprOpen:{{", "Expr: a }} b ", "ExprClose:}}", "EOF:"},
		},
		{
			name:  "escape is literal in raw mode",
			input: `[[ a \ b ]]`,
			want:  []string{"RawOpen:[[", `Raw: a \ b `, "RawClose:]]", "EOF:"},
		},
		{
			name:  "escape before ordinary byte is literal",
			input: `a\b`,
			want:  []string{`Text:a\b`, "EOF:"},
		},
		{
			name:  "escaped escape re-enables delimiter",
			input: `\\{{ x }}`,
			want: []string{
				`Text:\`, "ExprOpen:{{", "Expr: x ", "ExprClose:}}", "EOF:",
			},
		},
		{
			name:  "close delimiter is literal in text mode",
			input: "a }} b",
			want:  []string{"Text:a }} b", "EOF:"},
		},
		{
			name:  "expression directly followed by raw",
			input: "{{x}}[[y]]",
			want: []string{
				"ExprOpen:{{", "Expr:x", "ExprClose:}}",
				"RawOpen:[[", "Raw:y", "RawClose:]]", "EOF:",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			toks := lexAll(t, Config{}, tc.input)
			if got := summary(toks); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
			checkTiling(t, tc.input, toks)
		})
	}
}

func TestDelimiterPrefixOverlap(t *testing.T) {
	// "<%" is a proper prefix of "<%%"; "%>" is a proper prefix of "%%>".
	// Longest match must win, and partial prefixes must survive chunk splits.
	cfg := Config{ExprOpen: "<%", ExprClose: "%>", RawOpen: "<%%", RawClose: "%%>"}
	input := "a<%e%>b<%%r<%s%>t%%>c"
	want := []string{
		"Text:a", "ExprOpen:<%", "Expr:e", "ExprClose:%>",
		"Text:b", "RawOpen:<%%", "Raw:r<%s%>t", "RawClose:%%>",
		"Text:c", "EOF:",
	}
	toks := lexAll(t, cfg, input)
	if got := summary(toks); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	checkTiling(t, input, toks)

	// Same input arriving one byte at a time must give identical tokens.
	var byteToks []Token
	l := New(cfg)
	for i := 0; i < len(input); i++ {
		l.Feed([]byte{input[i]})
	}
	l.Close()
	for {
		tok, ok := l.Next()
		if !ok {
			t.Fatal("Next reported need-input after Close")
		}
		byteToks = append(byteToks, tok)
		if tok.Kind == KindEOF {
			break
		}
	}
	if !reflect.DeepEqual(toks, byteToks) {
		t.Errorf("byte-by-byte tokens differ:\nwhole: %v\nbytes: %v", summary(toks), summary(byteToks))
	}
}

func TestByteByByteEqualsWholeInput(t *testing.T) {
	inputs := []string{
		"hello {{ name }} world",
		"a [[ x {{ y }} ]] b",
		"[[ a [[ b ]] c ]]",
		`\{{ esc }} and {{ a \}} b }}`,
		"trailing partial delimiter {",
		"trailing escape\\",
		"{{unterminated",
		"[[unterminated [[ nested",
		"{{{ x }}}", // '{' then expr-open, expr ' x ', close then '}'
		"[[ ]][[ ]]",
	}
	for _, input := range inputs {
		whole := lexAll(t, Config{}, input)

		l := New(Config{})
		for i := 0; i < len(input); i++ {
			l.Feed([]byte{input[i]})
		}
		l.Close()
		var bytewise []Token
		for {
			tok, ok := l.Next()
			if !ok {
				t.Fatalf("Next reported need-input after Close for %q", input)
			}
			bytewise = append(bytewise, tok)
			if tok.Kind == KindEOF {
				break
			}
		}
		if !reflect.DeepEqual(whole, bytewise) {
			t.Errorf("input %q: byte-by-byte differs\nwhole: %v\nbytes: %v",
				input, summary(whole), summary(bytewise))
		}
		checkTiling(t, input, bytewise)
	}
}

func TestEOFInEachMode(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "EOF in text mode",
			input: "hello",
			want:  []string{"Text:hello", "EOF:"},
		},
		{
			name:  "EOF on partial delimiter in text mode",
			input: "ab{",
			want:  []string{"Text:ab{", "EOF:"},
		},
		{
			name:  "EOF on trailing escape in text mode",
			input: `ab\`,
			want:  []string{`Text:ab\`, "EOF:"},
		},
		{
			name:  "EOF in expression mode",
			input: "{{ abc",
			want:  []string{"ExprOpen:{{", "Expr: abc", "Error:unterminated expression", "EOF:"},
		},
		{
			name:  "EOF on trailing escape in expression mode",
			input: `{{ a\`,
			want:  []string{"ExprOpen:{{", `Expr: a\`, "Error:unterminated expression", "EOF:"},
		},
		{
			name:  "EOF in raw mode",
			input: "[[ abc",
			want:  []string{"RawOpen:[[", "Raw: abc", "Error:unterminated raw string", "EOF:"},
		},
		{
			name:  "EOF in nested raw mode",
			input: "[[ a [[ b ]]",
			want: []string{
				"RawOpen:[[", "Raw: a ", "RawOpen:[[", "Raw: b ",
				"RawClose:]]", "Error:unterminated raw string", "EOF:",
			},
		},
		{
			name:  "EOF on partial raw close delimiter",
			input: "[[ a ]",
			want:  []string{"RawOpen:[[", "Raw: a ]", "Error:unterminated raw string", "EOF:"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			toks := lexAll(t, Config{}, tc.input)
			if got := summary(toks); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
			checkTiling(t, tc.input, toks)
		})
	}
}

// drain reads tokens until the lexer needs more input or emits EOF.
func drain(t *testing.T, l *Lexer) []Token {
	t.Helper()
	var toks []Token
	for {
		tok, ok := l.Next()
		if !ok {
			return toks
		}
		toks = append(toks, tok)
		if tok.Kind == KindEOF {
			return toks
		}
	}
}

func TestMarkReset(t *testing.T) {
	input := `a {{ x }} b [[ c [[ d ]] e ]] f {{ y }} g`
	l := New(Config{})
	m0 := l.Mark() // save point before any input arrives
	l.Feed([]byte(input))
	l.Close()
	reference := drain(t, l)
	checkTiling(t, input, reference)

	// Full replay from the very beginning.
	l.Reset(m0)
	replay := drain(t, l)
	if !reflect.DeepEqual(reference, replay) {
		t.Fatalf("replay from start differs:\nref:    %v\nreplay: %v", summary(reference), summary(replay))
	}
	checkTiling(t, input, replay)
}

// TestMarkResetMidStream saves a restore point at every possible feed split
// (covering marks inside text, expression and nested raw modes, as well as
// mid-token and mid-delimiter states), then requires the re-lexed tail to
// be identical to the corresponding suffix of the reference stream.
func TestMarkResetMidStream(t *testing.T) {
	input := `a {{ x }} b [[ c [[ d ]] e ]] f {{ y }} g`
	reference := lexAll(t, Config{}, input)
	for i := 0; i <= len(input); i++ {
		l := New(Config{})
		l.Feed([]byte(input[:i]))
		m := l.Mark()
		l.Feed([]byte(input[i:]))
		l.Close()

		all := drain(t, l)
		if !reflect.DeepEqual(reference, all) {
			t.Fatalf("split at %d: stream differs:\nref: %v\ngot:  %v",
				i, summary(reference), summary(all))
		}

		l.Reset(m)
		rest := drain(t, l)
		if len(rest) == 0 {
			t.Fatalf("split at %d: no tokens after reset", i)
		}
		if len(rest) > len(reference) {
			t.Fatalf("split at %d: more tokens after reset than in reference", i)
		}
		if tail := reference[len(reference)-len(rest):]; !reflect.DeepEqual(tail, rest) {
			t.Fatalf("split at %d: re-lexed tail is not a suffix of the reference:\nref tail: %v\ngot:      %v",
				i, summary(tail), summary(rest))
		}
		// The re-lexed tail must tile the raw input up to its end.
		pos := rest[0].Start
		for _, tok := range rest {
			if tok.Kind == KindEOF || tok.Kind == KindError {
				continue
			}
			if tok.Start != pos {
				t.Fatalf("split at %d: token %v starts at %d, want %d", i, tok.Kind, tok.Start, pos)
			}
			pos = tok.End
		}
		if pos != len(input) {
			t.Fatalf("split at %d: re-lexed tail ends at %d, input has %d bytes", i, pos, len(input))
		}
	}
}

func TestMarkResetAcrossFeeds(t *testing.T) {
	input := `abc {{ x \}} y }} def [[ g ]]`
	l := New(Config{})

	// Feed a prefix ending mid-text, then mark: the save point captures the
	// partially accumulated text token.
	l.Feed([]byte("abc "))
	m := l.Mark()
	l.Feed([]byte(input[4:]))
	l.Close()
	first := drain(t, l)

	l.Reset(m)
	// After reset the lexer must re-lex the buffered remainder identically,
	// including merging it with the pre-mark text into one Text token.
	second := drain(t, l)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("tokens differ after reset across feeds:\nfirst:  %v\nsecond: %v",
			summary(first), summary(second))
	}
	want := []string{
		"Text:abc ", "ExprOpen:{{", "Expr: x }} y ", "ExprClose:}}",
		"Text: def ", "RawOpen:[[", "Raw: g ", "RawClose:]]", "EOF:",
	}
	if got := summary(first); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	checkTiling(t, input, first)
}

func TestNoPrefixRescan(t *testing.T) {
	// Each fed byte must be consumed exactly once: total scanned bytes equal
	// the input length even when input drips in one byte at a time.
	input := strings.Repeat("a", 5000) + "{{ x }}" + strings.Repeat("b", 5000) + "[[ c ]]"
	l := New(Config{})
	for i := 0; i < len(input); i++ {
		l.Feed([]byte{input[i]})
	}
	l.Close()
	if l.scanned != len(input) {
		t.Errorf("scanned %d bytes for %d input bytes; consumed prefix was rescanned",
			l.scanned, len(input))
	}
	n := 0
	for {
		tok, ok := l.Next()
		if !ok {
			t.Fatal("Next reported need-input after Close")
		}
		n++
		if tok.Kind == KindEOF {
			break
		}
	}
	if n != 9 { // Text, ExprOpen, Expr, ExprClose, Text, RawOpen, Raw, RawClose, EOF
		t.Errorf("got %d tokens, want 9", n)
	}
}
