package sqlifinger

import (
	"reflect"
	"testing"
)

func types(tokens []Token) []TokenType {
	out := make([]TokenType, len(tokens))
	for i, t := range tokens {
		out[i] = t.Type
	}
	return out
}

func TestTokenizeComments(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []TokenType
	}{
		{"line comment dash", "1-- x\n2", []TokenType{TokenNumber, TokenNumber}},
		{"dash not comment without space", "1--2", []TokenType{TokenNumber, TokenOperator, TokenOperator, TokenNumber}},
		{"hash comment", "1#x\n2", []TokenType{TokenNumber, TokenNumber}},
		{"block comment", "1/* hello */2", []TokenType{TokenNumber, TokenNumber}},
		{"unterminated block comment", "1/* hello", []TokenType{TokenNumber}},
		{"versioned comment executes", "1/*!AND*/1", []TokenType{TokenNumber, TokenKeyword, TokenNumber}},
		{"versioned comment with number", "1/*!50000AND*/1", []TokenType{TokenNumber, TokenKeyword, TokenNumber}},
		{"unterminated versioned comment", "1/*!AND 1", []TokenType{TokenNumber, TokenKeyword, TokenNumber}},
		{"comment between tokens", "OR/**/1=1", []TokenType{TokenKeyword, TokenNumber, TokenOperator, TokenNumber}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := types(Tokenize(tc.input, ContextUnquoted))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Tokenize(%q) types = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestTokenizeStrings(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []Token
	}{
		{"single", `'abc'`, []Token{{TokenString, "abc"}}},
		{"double", `"abc"`, []Token{{TokenString, "abc"}}},
		{"backslash escape", `'it\'s'`, []Token{{TokenString, `it\'s`}}},
		{"doubled quote escape", `'a''b'`, []Token{{TokenString, "a''b"}}},
		{"double quote doubled", `"a""b"`, []Token{{TokenString, `a""b`}}},
		{"unterminated", `'abc`, []Token{{TokenString, "abc"}}},
		{"string then sql", `'a' OR 1`, []Token{{TokenString, "a"}, {TokenKeyword, "OR"}, {TokenNumber, "1"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Tokenize(tc.input, ContextUnquoted)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Tokenize(%q) = %+v, want %+v", tc.input, got, tc.want)
			}
		})
	}
}

func TestTokenizeBacktickAndLiterals(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []Token
	}{
		{"backtick ident", "`users`", []Token{{TokenQuotedIdentifier, "users"}}},
		{"backtick escaped", "`a``b`", []Token{{TokenQuotedIdentifier, "a``b"}}},
		{"hex number", "0x1F", []Token{{TokenNumber, "0x1F"}}},
		{"hex string literal", "x'1f'", []Token{{TokenNumber, "1f"}}},
		{"hex string literal upper", "X'1F'", []Token{{TokenNumber, "1F"}}},
		{"bit literal", "b'01'", []Token{{TokenNumber, "01"}}},
		{"float", "3.14", []Token{{TokenNumber, "3.14"}}},
		{"exponent", "1e5", []Token{{TokenNumber, "1e5"}}},
		{"leading dot", ".5", []Token{{TokenNumber, ".5"}}},
		{"variable", "@@version", []Token{{TokenVariable, "@@version"}}},
		{"function", "SLEEP(5)", []Token{{TokenFunction, "SLEEP"}, {TokenPunctuation, "("}, {TokenNumber, "5"}, {TokenPunctuation, ")"}}},
		{"keyword not function", "SELECT 1", []Token{{TokenKeyword, "SELECT"}, {TokenNumber, "1"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Tokenize(tc.input, ContextUnquoted)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Tokenize(%q) = %+v, want %+v", tc.input, got, tc.want)
			}
		})
	}
}

func TestTokenizeContexts(t *testing.T) {
	input := `1' OR '1`

	unq := Tokenize(input, ContextUnquoted)
	if unq[0].Type != TokenNumber {
		t.Errorf("unquoted first token = %v, want number", unq[0])
	}

	sq := Tokenize(input, ContextSingleQuote)
	want := []Token{
		{TokenString, "1"},
		{TokenKeyword, "OR"},
		{TokenString, "1"},
	}
	if !reflect.DeepEqual(sq, want) {
		t.Errorf("single-quote ctx = %+v, want %+v", sq, want)
	}

	dq := Tokenize(`1" OR "1`, ContextDoubleQuote)
	wantD := []Token{
		{TokenString, "1"},
		{TokenKeyword, "OR"},
		{TokenString, "1"},
	}
	if !reflect.DeepEqual(dq, wantD) {
		t.Errorf("double-quote ctx = %+v, want %+v", dq, wantD)
	}
}

func TestFold(t *testing.T) {
	cases := []struct {
		input string
		ctx   Context
		want  string
	}{
		{`1 OR 1=1`, ContextUnquoted, "1&1o1"},
		{`1' OR '1'='1`, ContextSingleQuote, "s&sos"},
		{`1' UNioN SeLEct 1,2`, ContextSingleQuote, "sUk1,1"},
		{`1' AND SLEEP(5)-- -`, ContextSingleQuote, "s&f(1)"},
		{"1' OR `1`=`1`-- -", ContextSingleQuote, "s&non"},
		{`admin'-- `, ContextSingleQuote, "s"},
		{`O'Brien`, ContextSingleQuote, "sv"},
		{`Tom & Jerry`, ContextUnquoted, "vov"},
	}
	for _, tc := range cases {
		got := Fold(Tokenize(tc.input, tc.ctx))
		if got != tc.want {
			t.Errorf("Fold(%q, %v) = %q, want %q", tc.input, tc.ctx, got, tc.want)
		}
	}
}
