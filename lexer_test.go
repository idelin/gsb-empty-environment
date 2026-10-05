package lex

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func tok(t TokenType, val string, pos, end int) Token {
	return Token{Type: t, Val: val, Pos: pos, End: end}
}

// run drains all currently available tokens, stopping at ErrNeedMore or EOF.
func run(t *testing.T, lx *Lexer) []Token {
	t.Helper()
	var toks []Token
	for {
		tk, err := lx.Next()
		if err == ErrNeedMore {
			return toks
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		toks = append(toks, tk)
		if tk.Type == TokenEOF {
			return toks
		}
	}
}

// lexBatch tokenizes input fed in a single chunk.
func lexBatch(t *testing.T, in string) []Token {
	t.Helper()
	lx := New()
	lx.Feed([]byte(in))
	lx.Close()
	return run(t, lx)
}

// sig renders a token stream as a compact string for table comparisons.
func sig(toks []Token) string {
	var b strings.Builder
	for i, tk := range toks {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%s:%q", tk.Type, tk.Val)
	}
	return b.String()
}

func TestBasic(t *testing.T) {
	got := lexBatch(t, "Hello {{ name }}!")
	want := []Token{
		tok(TokenText, "Hello ", 0, 6),
		tok(TokenExprOpen, "{{", 6, 8),
		tok(TokenIdent, "name", 9, 13),
		tok(TokenExprClose, "}}", 14, 16),
		tok(TokenText, "!", 16, 17),
		tok(TokenEOF, "", 17, 17),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
}

func TestExprTokens(t *testing.T) {
	got := lexBatch(t, `{{ user.name_2 + 1.5 * (x, "s") }}`)
	var kinds []TokenType
	for _, tk := range got {
		kinds = append(kinds, tk.Type)
	}
	want := []TokenType{
		TokenExprOpen,
		TokenIdent, TokenPunct, TokenIdent, // user . name_2
		TokenPunct,                                                  // +
		TokenNumber,                                                 // 1.5
		TokenPunct,                                                  // *
		TokenPunct, TokenIdent, TokenPunct, TokenString, TokenPunct, // ( x , "s" )
		TokenExprClose,
		TokenEOF,
	}
	if !slices.Equal(kinds, want) {
		t.Fatalf("got  %v\nwant %v", kinds, want)
	}
}

// Escapes are mode-specific: the same bytes mean different things in text,
// string and raw modes.
func TestModeSpecificEscapes(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`a\{b`, `Text:"a{b" EOF:""`},   // \{ escapes the brace in text
		{`a\\b`, `Text:"a\\b" EOF:""`},  // \\ is one backslash in text
		{`x\ny`, `Text:"x\\ny" EOF:""`}, // \n is NOT an escape in text
		{`{{ "a\nb" }}`, `ExprOpen:"{{" String:"a\nb" ExprClose:"}}" EOF:""`},
		{`{{ "q\"t\\" }}`, `ExprOpen:"{{" String:"q\"t\\" ExprClose:"}}" EOF:""`},
		// raw mode: backslash is literal, \n stays two bytes
		{`{{ [[ a\nb\{ ]] }}`, `ExprOpen:"{{" RawString:" a\\nb\\{ " ExprClose:"}}" EOF:""`},
	}
	for _, c := range cases {
		if got := sig(lexBatch(t, c.in)); got != c.want {
			t.Errorf("input %q:\n got %s\nwant %s", c.in, got, c.want)
		}
	}
}

// Delimiters share prefixes with single characters and with each other:
// "{" vs "{{", "}" vs "}}", "[" vs "[[", "]" vs "]]".
func TestDelimiterPrefixOverlap(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"{", `Text:"{" EOF:""`},
		{"a{b", `Text:"a{b" EOF:""`},
		{"{{", `ExprOpen:"{{" Error:"unterminated expression" EOF:""`},
		{"{{{", `ExprOpen:"{{" Punct:"{" Error:"unterminated expression" EOF:""`},
		{"{{}}", `ExprOpen:"{{" ExprClose:"}}" EOF:""`},
		{"{{ }}}", `ExprOpen:"{{" ExprClose:"}}" Text:"}" EOF:""`},
		{"{{ } }}", `ExprOpen:"{{" Punct:"}" ExprClose:"}}" EOF:""`},
		{"{{ }", `ExprOpen:"{{" Punct:"}" Error:"unterminated expression" EOF:""`},
		{"{{ [ ] }}", `ExprOpen:"{{" Punct:"[" Punct:"]" ExprClose:"}}" EOF:""`},
		{"{{ [[ ]] }}", `ExprOpen:"{{" RawString:" " ExprClose:"}}" EOF:""`},
		{"{{ [[]] }}", `ExprOpen:"{{" RawString:"" ExprClose:"}}" EOF:""`},
		{"{{ [[ ] ]] }}", `ExprOpen:"{{" RawString:" ] " ExprClose:"}}" EOF:""`},
		{"{{ [[ ]]] }}", `ExprOpen:"{{" RawString:" " Punct:"]" ExprClose:"}}" EOF:""`},
		{"{{ [[[]] }}", `ExprOpen:"{{" RawString:"[" ExprClose:"}}" EOF:""`},
		{"a{{b}}c{", `Text:"a" ExprOpen:"{{" Ident:"b" ExprClose:"}}" Text:"c{" EOF:""`},
	}
	for _, c := range cases {
		if got := sig(lexBatch(t, c.in)); got != c.want {
			t.Errorf("input %q:\n got %s\nwant %s", c.in, got, c.want)
		}
	}
}

func TestNestedRawString(t *testing.T) {
	got := lexBatch(t, `{{ [[ a [[ b {{ c }} d ]] e ]] }}`)
	want := []Token{
		tok(TokenExprOpen, "{{", 0, 2),
		tok(TokenRawString, " a [[ b {{ c }} d ]] e ", 3, 30),
		tok(TokenExprClose, "}}", 31, 33),
		tok(TokenEOF, "", 33, 33),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
}

// Byte-by-byte arrival must produce exactly the same tokens and spans as
// batch feeding.
func TestByteByByte(t *testing.T) {
	inputs := []string{
		"",
		"hello",
		"Hello {{ name }}!",
		`a\{b \\ c{{ x }}`,
		`{{ "a\nb\"c" }}`,
		`{{ [[ raw {{ not expr }} ]] }}`,
		`{{ [[ a [[ b ]] c ]] }}`,
		"{{ x + 1.5 }}",
		"{{ [1] }}",
		"{{ [[ ]] }}",
		"trailing {",
		`trailing \`,
		"{{ unterminated",
		`{{ "unterminated`,
		`{{ "unterminated\`,
		"{{ [[ unterminated",
		"{{ [[ unterminated ]",
		"{{ [[ unterminated [",
		"{{ }}}",
		"a{b}}c",
		"{{ x }}{{ y }}",
		"mix {{ a [[ z ]] b }} tail \\{ {{ [[ [[ ]] ]] }}",
	}
	for _, in := range inputs {
		batch := lexBatch(t, in)

		lx := New()
		var drip []Token
		for i := 0; i < len(in); i++ {
			lx.Feed([]byte{in[i]})
			drip = append(drip, run(t, lx)...)
		}
		lx.Close()
		drip = append(drip, run(t, lx)...)

		if !slices.Equal(batch, drip) {
			t.Errorf("input %q:\n batch %v\n drip  %v", in, batch, drip)
		}
	}
}

// EOF must be handled correctly no matter which mode is active.
func TestEOFInEachMode(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []Token
	}{
		{"text", "abc", []Token{
			tok(TokenText, "abc", 0, 3),
			tok(TokenEOF, "", 3, 3),
		}},
		{"text dangling brace", "ab{", []Token{
			tok(TokenText, "ab{", 0, 3),
			tok(TokenEOF, "", 3, 3),
		}},
		{"text dangling backslash", `ab\`, []Token{
			tok(TokenText, `ab\`, 0, 3),
			tok(TokenEOF, "", 3, 3),
		}},
		{"expr", "{{ x", []Token{
			tok(TokenExprOpen, "{{", 0, 2),
			tok(TokenIdent, "x", 3, 4),
			tok(TokenError, "unterminated expression", 4, 4),
			tok(TokenEOF, "", 4, 4),
		}},
		{"expr mid delimiter", "{{ x }", []Token{
			tok(TokenExprOpen, "{{", 0, 2),
			tok(TokenIdent, "x", 3, 4),
			tok(TokenPunct, "}", 5, 6),
			tok(TokenError, "unterminated expression", 6, 6),
			tok(TokenEOF, "", 6, 6),
		}},
		{"string", `{{ "ab`, []Token{
			tok(TokenExprOpen, "{{", 0, 2),
			tok(TokenError, "unterminated string", 3, 6),
			tok(TokenEOF, "", 6, 6),
		}},
		{"string dangling escape", `{{ "ab\`, []Token{
			tok(TokenExprOpen, "{{", 0, 2),
			tok(TokenError, "unterminated string", 3, 7),
			tok(TokenEOF, "", 7, 7),
		}},
		{"raw", "{{ [[ ab", []Token{
			tok(TokenExprOpen, "{{", 0, 2),
			tok(TokenError, "unterminated raw string", 3, 8),
			tok(TokenEOF, "", 8, 8),
		}},
		{"raw dangling bracket", "{{ [[ ab ]", []Token{
			tok(TokenExprOpen, "{{", 0, 2),
			tok(TokenError, "unterminated raw string", 3, 10),
			tok(TokenEOF, "", 10, 10),
		}},
		{"raw nested", "{{ [[ a [[ b", []Token{
			tok(TokenExprOpen, "{{", 0, 2),
			tok(TokenError, "unterminated raw string", 3, 12),
			tok(TokenEOF, "", 12, 12),
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := lexBatch(t, c.in); !slices.Equal(got, c.want) {
				t.Errorf("got  %v\nwant %v", got, c.want)
			}
		})
	}
}

// A saved restore point must allow re-reading the same suffix with
// identical tokens and raw spans, from any split point of the input,
// including splits in the middle of tokens, escapes and delimiters.
func TestSaveRestore(t *testing.T) {
	input := `Hi {{ user.name }}, raw={{ [[ a [[b]] c ]] }} s={{ "q\n" }} \{lit`
	full := lexBatch(t, input)

	for split := 0; split <= len(input); split++ {
		lx := New()
		lx.Feed([]byte(input[:split]))
		before := run(t, lx)

		st := lx.Save()

		// First pass: continue reading after the save point.
		lx.Feed([]byte(input[split:]))
		lx.Close()
		a := run(t, lx)

		// Restore and re-read the same suffix twice: the state must be
		// reusable and both passes must match the first.
		for pass := 0; pass < 2; pass++ {
			lx.Restore(st)
			lx.Feed([]byte(input[split:]))
			lx.Close()
			b := run(t, lx)
			if !slices.Equal(a, b) {
				t.Fatalf("split %d pass %d:\n first %v\n again %v", split, pass, a, b)
			}
		}

		// Tokens read before the save plus tokens read after it must
		// equal a fresh tokenization of the whole input.
		got := append(slices.Clone(before), a...)
		if !slices.Equal(full, got) {
			t.Fatalf("split %d:\n full %v\n got  %v", split, full, got)
		}
	}
}

// Raw spans must be consistent: monotonic, non-overlapping, inside the
// input, and EOF (or a trailing error) must land on the input end.
func TestRawSpanConsistency(t *testing.T) {
	inputs := []string{
		"Hello {{ name }}!",
		`a\{b \\ c{{ x }}`,
		`{{ "a\nb" }} {{ [[ r [[ s ]] t ]] }}`,
		"{{ unterminated",
		"{{ [[ unterminated",
	}
	for _, in := range inputs {
		toks := lexBatch(t, in)
		prev := 0
		for _, tk := range toks {
			if tk.Pos < prev || tk.End < tk.Pos {
				t.Errorf("%q: bad span %+v after %d", in, tk, prev)
			}
			if tk.End > len(in) {
				t.Errorf("%q: span %+v past end %d", in, tk, len(in))
			}
			prev = tk.End
			// Without escapes, the raw span is the token value.
			raw := in[tk.Pos:tk.End]
			if !strings.Contains(raw, "\\") {
				switch tk.Type {
				case TokenText, TokenIdent, TokenNumber, TokenPunct:
					if raw != tk.Val {
						t.Errorf("%q: raw %q != val %q for %v", in, raw, tk.Val, tk.Type)
					}
				}
			}
		}
		last := toks[len(toks)-1]
		if last.Type != TokenEOF || last.Pos != len(in) || last.End != len(in) {
			t.Errorf("%q: final token %+v, want EOF at %d", in, last, len(in))
		}
	}
}

// The scanner must not rescan the input prefix: total scanner steps must
// stay linear in the input size even when input arrives one byte at a time
// and every other byte forces a delimiter lookahead.
func TestNoPrefixRescan(t *testing.T) {
	lx := New()
	const n = 20000
	// "{x" pairs: every '{' needs a lookahead byte before it can be
	// classified, the worst case for re-examination.
	for i := 0; i < n/2; i++ {
		lx.Feed([]byte("{x"))
		run(t, lx)
	}
	lx.Close()
	run(t, lx)

	if lx.steps > 3*n {
		t.Fatalf("scanner took %d steps for %d input bytes; want linear (<= %d)", lx.steps, n, 3*n)
	}
}

// Consumed input must be dropped from the internal buffer.
func TestBufferCompaction(t *testing.T) {
	lx := New()
	lx.Feed([]byte(strings.Repeat("{{ x }}", 1000)))
	lx.Close()
	toks := run(t, lx)
	if got := lx.Buffered(); got != 0 {
		t.Fatalf("buffered %d bytes after draining, want 0", got)
	}
	if len(toks) != 1000*3+1 {
		t.Fatalf("got %d tokens, want %d", len(toks), 1000*3+1)
	}
}
