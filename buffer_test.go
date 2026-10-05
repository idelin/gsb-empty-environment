package bracketdiag

import (
	"math/rand"
	"strings"
	"testing"
)

// naiveFirstError is an independent single-pass scanner used to verify the
// incremental implementation. It tracks lexical state from byte 0 and only
// matches brackets inside [start, end).
func naiveFirstError(text string, start, end int) *Diag {
	st := sNormal
	var stack []brk
	i := 0
	for i < end {
		c := text[i]
		advance := true
		switch st {
		case sNormal:
			switch {
			case c == '/':
				st = sSlash
			case c == '"':
				st = sDq
			case c == '\'':
				st = sSq
			case isOpen(c):
				if i >= start {
					stack = append(stack, brk{c, i})
				}
			case isClose(c):
				if i >= start {
					if len(stack) == 0 {
						return &Diag{Pos: i, Found: c}
					}
					top := stack[len(stack)-1]
					if closeOf(top.ch) == c {
						stack = stack[:len(stack)-1]
					} else {
						return &Diag{Pos: i, Found: c, Expected: closeOf(top.ch)}
					}
				}
			}
		case sSlash:
			if c == '/' {
				st = sLine
			} else if c == '*' {
				st = sBlock
			} else {
				st = sNormal
				advance = false
			}
		case sDq:
			if c == '\\' {
				st = sDqEsc
			} else if c == '"' {
				st = sNormal
			}
		case sDqEsc:
			st = sDq
		case sSq:
			if c == '\\' {
				st = sSqEsc
			} else if c == '\'' {
				st = sNormal
			}
		case sSqEsc:
			st = sSq
		case sLine:
			if c == '\n' {
				st = sNormal
			}
		case sBlock:
			if c == '*' {
				st = sBlockStar
			}
		case sBlockStar:
			if c == '/' {
				st = sNormal
			} else if c != '*' {
				st = sBlock
			}
		}
		if advance {
			i++
		}
	}
	if len(stack) > 0 {
		return &Diag{Pos: stack[0].off, Expected: closeOf(stack[0].ch)}
	}
	return nil
}

func checkRange(t *testing.T, buf *Buffer, start, end int) {
	t.Helper()
	text := buf.String()
	got := buf.FirstError(start, end)
	want := naiveFirstError(text, start, end)
	if (got == nil) != (want == nil) {
		t.Fatalf("range [%d,%d) of %q: got %v, want %v", start, end, text, got, want)
	}
	if got != nil && *got != *want {
		t.Fatalf("range [%d,%d) of %q: got %v, want %v", start, end, text, got, want)
	}
}

func TestBasicDiagnostics(t *testing.T) {
	cases := []struct {
		text string
		want *Diag
	}{
		{"(foo)", nil},
		{"a(b)c", nil},
		{"([{}])", nil},
		{"(", &Diag{Pos: 0, Expected: ')'}},
		{")", &Diag{Pos: 0, Found: ')'}},
		{"([)]", &Diag{Pos: 2, Found: ')', Expected: ']'}},
		{"[(]", &Diag{Pos: 2, Found: ']', Expected: ')'}},
		{"foo(bar", &Diag{Pos: 3, Expected: ')'}},
		{"([}", &Diag{Pos: 2, Found: '}', Expected: ']'}},
		// Brackets inside strings and comments do not count.
		{`"(]"`, nil},
		{"'['", nil},
		{"\"a\\\"]\"", nil}, // escaped quote keeps the string open
		{"// ]\n", nil},
		{"// ]\n)", &Diag{Pos: 5, Found: ')'}},
		{"/* ] */", nil},
		{"/* ] */ ]", &Diag{Pos: 8, Found: ']'}},
		{"{/* ( */}", nil},
		{"/* unterminated (", nil},
		{"a/b(c)", nil}, // lone slash is not a comment
		{"a/*b*/c(", &Diag{Pos: 7, Expected: ')'}},
	}
	for _, c := range cases {
		buf := New(c.text)
		got := buf.FirstError(0, buf.Len())
		if (got == nil) != (c.want == nil) {
			t.Errorf("%q: got %v, want %v", c.text, got, c.want)
			continue
		}
		if got != nil && *got != *c.want {
			t.Errorf("%q: got %v, want %v", c.text, got, c.want)
		}
	}
}

func TestRangeQuery(t *testing.T) {
	buf := New("a(b]c")
	// [0,3) = "a(b": unclosed opener.
	if d := buf.FirstError(0, 3); d == nil || *d != (Diag{Pos: 1, Expected: ')'}) {
		t.Fatalf("[0,3): got %v", d)
	}
	// [3,4) = "]": unexpected closer, empty stack.
	if d := buf.FirstError(3, 4); d == nil || *d != (Diag{Pos: 3, Found: ']'}) {
		t.Fatalf("[3,4): got %v", d)
	}
	// Full range: ']' mismatches '('.
	if d := buf.FirstError(0, 5); d == nil || *d != (Diag{Pos: 3, Found: ']', Expected: ')'}) {
		t.Fatalf("[0,5): got %v", d)
	}
	if d := buf.FirstError(2, 2); d != nil {
		t.Fatalf("empty range: got %v", d)
	}
}

func TestEscapeAcrossChunks(t *testing.T) {
	// The backslash lands at the end of one 512-byte chunk and the quote it
	// escapes starts the next chunk.
	text := `"` + strings.Repeat("a", 510) + `\"` + ")" + `"` + "]"
	buf := New(text)
	if buf.Len() != 516 {
		t.Fatalf("len = %d", buf.Len())
	}
	want := &Diag{Pos: 515, Found: ']'}
	got := buf.FirstError(0, buf.Len())
	if got == nil || *got != *want {
		t.Fatalf("got %v, want %v", got, want)
	}
	checkRange(t, buf, 0, buf.Len())
}

func TestCommentAcrossChunks(t *testing.T) {
	// '/' ends the first chunk, '*' starts the next: a block comment opens
	// across the boundary and swallows the first ')'.
	text := strings.Repeat("a", 511) + "/* ) */ )"
	buf := New(text)
	want := &Diag{Pos: 519, Found: ')'}
	got := buf.FirstError(0, buf.Len())
	if got == nil || *got != *want {
		t.Fatalf("got %v, want %v", got, want)
	}
	checkRange(t, buf, 0, buf.Len())
}

func TestQuoteTogglesFarSemantics(t *testing.T) {
	buf := New("]" + strings.Repeat("(ab) ", 100))
	// The stray ']' at byte 0 is an error.
	if d := buf.FirstError(0, buf.Len()); d == nil || *d != (Diag{Pos: 0, Found: ']'}) {
		t.Fatalf("initial: got %v", d)
	}
	// Inserting a quote at byte 0 pulls the rest of the buffer into a
	// string, so the far-away bracket error disappears.
	buf.Insert(0, `"`)
	if d := buf.FirstError(0, buf.Len()); d != nil {
		t.Fatalf("after opening quote: got %v", d)
	}
	// Closing the string at the very end keeps the ']' quoted: still no
	// bracket error, but the naive scanner must agree on every prefix.
	buf.Insert(buf.Len(), `"`)
	if d := buf.FirstError(0, buf.Len()); d != nil {
		t.Fatalf("after closing quote: got %v", d)
	}
	checkRange(t, buf, 0, buf.Len())
	// Undoing the closing quote keeps everything a string; undoing the
	// opening quote restores the original diagnostic at byte 0.
	buf.Undo()
	if d := buf.FirstError(0, buf.Len()); d != nil {
		t.Fatalf("after first undo: got %v", d)
	}
	buf.Undo()
	if d := buf.FirstError(0, buf.Len()); d == nil || *d != (Diag{Pos: 0, Found: ']'}) {
		t.Fatalf("after second undo: got %v", d)
	}
	checkRange(t, buf, 0, buf.Len())
}

func TestUndoSequence(t *testing.T) {
	buf := New("base (text) /* c */")
	var hist []string
	ref := buf.String()
	apply := func(pos int, s string, del int) {
		hist = append(hist, ref)
		if del > 0 {
			buf.Delete(pos, del)
			ref = ref[:pos] + ref[pos+del:]
		}
		if s != "" {
			buf.Insert(pos, s)
			ref = ref[:pos] + s + ref[pos:]
		}
	}
	apply(0, "(", 0)
	apply(buf.Len(), "]", 0)
	apply(2, `"`, 0)
	apply(0, "", 1)
	for i := 0; i < 4; i++ {
		if buf.String() != ref {
			t.Fatalf("step %d: buffer %q != ref %q", i, buf.String(), ref)
		}
		checkRange(t, buf, 0, buf.Len())
		if !buf.Undo() {
			t.Fatalf("step %d: undo failed", i)
		}
		ref = hist[len(hist)-1]
		hist = hist[:len(hist)-1]
	}
	if buf.String() != "base (text) /* c */" {
		t.Fatalf("after undos: %q", buf.String())
	}
	if buf.Undo() {
		t.Fatal("undo stack should be empty")
	}
}

func TestRandomEdits(t *testing.T) {
	for _, seed := range []int64{42, 7, 1999} {
		runRandomEdits(t, seed)
	}
}

func runRandomEdits(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	tokens := []string{
		"(", ")", "[", "]", "{", "}", `"`, "'", `\`, "/", "*",
		"a", "b", " ", "\n", "//", "/*", "*/", `\"`, "\\", "x)", "(y", "0",
	}
	buf := New("")
	ref := ""
	var hist []string
	for iter := 0; iter < 2000; iter++ {
		switch rng.Intn(10) {
		case 0, 1, 2, 3: // insert a random token burst
			n := 1 + rng.Intn(6)
			s := ""
			for k := 0; k < n; k++ {
				s += tokens[rng.Intn(len(tokens))]
			}
			if rng.Intn(20) == 0 {
				s = strings.Repeat(s, 200) // force multi-chunk inserts
			}
			pos := rng.Intn(len(ref) + 1)
			buf.Insert(pos, s)
			hist = append(hist, ref)
			ref = ref[:pos] + s + ref[pos:]
		case 4, 5, 6: // delete a random span
			if len(ref) > 0 {
				pos := rng.Intn(len(ref))
				n := 1 + rng.Intn(len(ref)-pos)
				if n > 400 {
					n = 400
				}
				buf.Delete(pos, n)
				hist = append(hist, ref)
				ref = ref[:pos] + ref[pos+n:]
			}
		case 7: // undo
			if len(hist) > 0 {
				if !buf.Undo() {
					t.Fatalf("seed %d iter %d: undo failed", seed, iter)
				}
				ref = hist[len(hist)-1]
				hist = hist[:len(hist)-1]
			}
		}
		if got := buf.String(); got != ref {
			t.Fatalf("seed %d iter %d: buffer mismatch (len %d != %d)", seed, iter, len(got), len(ref))
		}
		checkRange(t, buf, 0, len(ref))
		for q := 0; q < 4; q++ {
			s, e := 0, 0
			if len(ref) > 0 {
				s = rng.Intn(len(ref) + 1)
				e = s + rng.Intn(len(ref)-s+1)
			}
			checkRange(t, buf, s, e)
		}
	}
}
