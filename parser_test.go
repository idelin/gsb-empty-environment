package errmerge

import (
	"reflect"
	"strings"
	"testing"
)

// --- a tiny lexer for the test grammar ---

func lex(src string) []Token {
	var toks []Token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			i++
		case strings.IndexByte(":=()+;", c) >= 0:
			toks = append(toks, Token{Kind: string(c), Text: string(c), Off: i})
			i++
		case c >= '0' && c <= '9':
			j := i
			for j < len(src) && src[j] >= '0' && src[j] <= '9' {
				j++
			}
			toks = append(toks, Token{Kind: "num", Text: src[i:j], Off: i})
			i = j
		case c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			j := i
			for j < len(src) && (src[j] == '_' ||
				(src[j] >= 'a' && src[j] <= 'z') ||
				(src[j] >= 'A' && src[j] <= 'Z') ||
				(src[j] >= '0' && src[j] <= '9')) {
				j++
			}
			word := src[i:j]
			kind := "ident"
			if word == "let" {
				kind = "let"
			}
			toks = append(toks, Token{Kind: kind, Text: word, Off: i})
			i = j
		default:
			toks = append(toks, Token{Kind: "unknown", Text: string(c), Off: i})
			i++
		}
	}
	return toks
}

// --- the ambiguous-prefix grammar ---
//
//	stmt        := assign | typedAssign          (both start with: let ident)
//	assign      := "let" ident "=" expr
//	typedAssign := "let" ident ":" ident "=" expr
//	expr        := term ("+" term)*
//	term        := ident | num | "(" expr ")"
func stmtParser() Node {
	var expr Node
	term := Rule("term", Alt(
		Tok("ident"),
		Tok("num"),
		Seq(Tok("("), Lazy(&expr), Tok(")")),
	))
	expr = Rule("expr", Seq(term, Many(Seq(Tok("+"), term))))
	assign := Rule("assign",
		Seq(Tok("let"), Tok("ident"), Tok("="), Lazy(&expr)))
	typedAssign := Rule("typedAssign",
		Seq(Tok("let"), Tok("ident"), Tok(":"), Tok("ident"), Tok("="), Lazy(&expr)))
	return Rule("stmt", Alt(assign, typedAssign))
}

func parse(t *testing.T, src string, maxDepth int) Result {
	t.Helper()
	p := &Parser{Tokens: lex(src), MaxDepth: maxDepth}
	return p.Parse(stmtParser())
}

// Backtracking: the first alternative consumes "let x" and fails at ":";
// the second must restart from position 0, not from where the first stopped.
func TestBacktrackAmbiguousPrefix(t *testing.T) {
	r := parse(t, "let x : int = 42", 0)
	if !r.OK {
		t.Fatalf("expected success, got %s", r.Diag)
	}
	if r.Pos != 6 {
		t.Fatalf("consumed %d tokens, want 6", r.Pos)
	}
}

// The abandoned branch's successful consumption must not leak: after
// backtracking, a trailing-junk error is reported at the committed parse's
// own failure point, and the failed alternative's shallower diag loses.
func TestAbandonedBranchConsumptionNotCounted(t *testing.T) {
	r := parse(t, "let x = 1 ;", 0)
	if r.OK {
		t.Fatal("expected failure on trailing ';'")
	}
	// committed parse stopped at token 4 (";"); typedAssign failed earlier
	// at token 2 expecting ":". Farthest failure wins: token 4. The "+"
	// comes from expr's Many, whose final failed attempt is also at token 4.
	if r.Diag.Pos != 4 {
		t.Fatalf("diag pos = %d, want 4", r.Diag.Pos)
	}
	if !reflect.DeepEqual(r.Diag.Expected, []string{"+", "<EOF>"}) {
		t.Fatalf("expected = %v, want [+ <EOF>]", r.Diag.Expected)
	}
}

// Both alternatives fail at the same position: expected sets merge.
func TestExpectedSetsMergedAtSamePosition(t *testing.T) {
	r := parse(t, "let x", 0)
	if r.OK {
		t.Fatal("expected failure")
	}
	if r.Diag.Pos != 2 {
		t.Fatalf("diag pos = %d, want 2", r.Diag.Pos)
	}
	want := []string{":", "="}
	if !reflect.DeepEqual(r.Diag.Expected, want) {
		t.Fatalf("expected = %v, want %v", r.Diag.Expected, want)
	}
	// both calling contexts are preserved
	var rules []string
	for _, s := range r.Diag.Sources {
		rules = append(rules, s.Rule)
	}
	if !reflect.DeepEqual(rules, []string{"assign", "typedAssign"}) {
		t.Fatalf("source rules = %v, want [assign typedAssign]", rules)
	}
}

// EOF: the farthest failure is at end of input; the deep branch's
// expectations beat the shallower abandoned alternative.
func TestEOFFarthestPosition(t *testing.T) {
	r := parse(t, "let x : int = (1", 0)
	if r.OK {
		t.Fatal("expected failure")
	}
	if r.Diag.Pos != 7 { // == len(tokens): EOF
		t.Fatalf("diag pos = %d, want 7 (EOF)", r.Diag.Pos)
	}
	want := []string{")", "+"}
	if !reflect.DeepEqual(r.Diag.Expected, want) {
		t.Fatalf("expected = %v, want %v", r.Diag.Expected, want)
	}
}

// EOF right where an expression should start.
func TestEOFAtExpressionStart(t *testing.T) {
	r := parse(t, "let x =", 0)
	if r.OK {
		t.Fatal("expected failure")
	}
	if r.Diag.Pos != 3 {
		t.Fatalf("diag pos = %d, want 3 (EOF)", r.Diag.Pos)
	}
	want := []string{"(", "ident", "num"}
	if !reflect.DeepEqual(r.Diag.Expected, want) {
		t.Fatalf("expected = %v, want %v", r.Diag.Expected, want)
	}
}

// Recursion depth limit: deeply nested parens fail cleanly with a
// depth-limit diagnostic instead of overflowing the stack.
func TestRecursionDepthLimit(t *testing.T) {
	deep := "let x = " + strings.Repeat("( ", 100) + "1" + strings.Repeat(" )", 100)
	r := parse(t, deep, 32)
	if r.OK {
		t.Fatal("expected depth-limit failure")
	}
	if !r.Diag.DepthLimit {
		t.Fatalf("expected DepthLimit diag, got %s", r.Diag)
	}
	if !strings.Contains(r.Diag.String(), "recursion depth limit") {
		t.Fatalf("diag should mention depth limit: %s", r.Diag)
	}

	// the same input parses fine with a generous limit
	r = parse(t, deep, 0)
	if !r.OK {
		t.Fatalf("expected success without depth limit, got %s", r.Diag)
	}
}

// A failed speculative branch must not prevent an overall successful parse.
func TestFailedBranchDoesNotBreakSuccess(t *testing.T) {
	manyA := Seq(Tok("a"), Many(Tok("a")))
	s := Alt(Seq(manyA, Tok("!")), manyA)
	p := &Parser{Tokens: []Token{{Kind: "a"}, {Kind: "a"}, {Kind: "a"}}}
	r := p.Parse(s)
	if !r.OK || r.Pos != 3 {
		t.Fatalf("expected success consuming 3, got %+v (%s)", r, r.Diag)
	}
}

// When the committed parse later fails, failures (not consumption) from the
// abandoned branch merge at the farthest position.
func TestAbandonedFailureMergesAtFrontier(t *testing.T) {
	manyA := Seq(Tok("a"), Many(Tok("a")))
	s := Alt(Seq(manyA, Tok("!")), manyA)
	p := &Parser{Tokens: []Token{
		{Kind: "a"}, {Kind: "a"}, {Kind: "a"}, {Kind: "b"},
	}}
	r := p.Parse(s)
	if r.OK {
		t.Fatal("expected failure on trailing 'b'")
	}
	if r.Diag.Pos != 3 {
		t.Fatalf("diag pos = %d, want 3", r.Diag.Pos)
	}
	want := []string{"!", "<EOF>", "a"}
	if !reflect.DeepEqual(r.Diag.Expected, want) {
		t.Fatalf("expected = %v, want %v", r.Diag.Expected, want)
	}
}

// --- Merge unit tests ---

func TestMergeFarthestWins(t *testing.T) {
	near := expectAt(2, "=", []string{"stmt", "assign"})
	far := expectAt(5, ")", []string{"stmt", "assign", "expr", "term"})
	m := Merge(near, far)
	if m.Pos != 5 || !reflect.DeepEqual(m.Expected, []string{")"}) {
		t.Fatalf("got %v @%d, want [)] @5", m.Expected, m.Pos)
	}
	if len(m.Sources) != 1 || m.Sources[0].Rule != "term" {
		t.Fatalf("sources = %+v, want only term", m.Sources)
	}
}

func TestMergeKeepsShortestChainPerContext(t *testing.T) {
	short := Diag{
		Pos:      3,
		Expected: []string{"num"},
		Sources: []Source{
			{Rule: "term", Chain: []string{"stmt", "expr", "term"}},
		},
	}
	long := Diag{
		Pos:      3,
		Expected: []string{"ident"},
		Sources: []Source{
			{Rule: "term", Chain: []string{"stmt", "expr", "term", "expr", "term"}},
			{Rule: "assign", Chain: []string{"stmt", "assign"}},
		},
	}
	m := Merge(short, long)
	if !reflect.DeepEqual(m.Expected, []string{"ident", "num"}) {
		t.Fatalf("expected = %v, want [ident num]", m.Expected)
	}
	if len(m.Sources) != 2 {
		t.Fatalf("sources = %+v, want 2 contexts", m.Sources)
	}
	for _, s := range m.Sources {
		if s.Rule == "term" && !reflect.DeepEqual(s.Chain, []string{"stmt", "expr", "term"}) {
			t.Fatalf("term chain = %v, want shortest [stmt expr term]", s.Chain)
		}
	}
}

func TestMergeEmpty(t *testing.T) {
	if Merge().Valid() {
		t.Fatal("merge of nothing should be invalid")
	}
	d := expectAt(1, "x", nil)
	if got := Merge(Empty(), d); !reflect.DeepEqual(got, d) {
		t.Fatalf("got %+v, want %+v", got, d)
	}
}
