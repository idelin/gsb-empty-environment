package bracketdiag

import "fmt"

// Diag describes the first bracket error found in a queried range.
//
// Pos is the byte offset of the error. For a closing bracket that matches
// nothing, Found is that bracket and Expected is the closing bracket the
// innermost open opener needs (0 if the opener stack was empty). For an
// opening bracket never closed inside the range, Found is 0, Pos is the
// opener's offset and Expected is its matching closing bracket.
type Diag struct {
	Pos      int
	Found    byte
	Expected byte
}

func (d *Diag) String() string {
	if d == nil {
		return "no error"
	}
	if d.Found == 0 {
		return fmt.Sprintf("byte %d: unclosed opener, expected %q", d.Pos, d.Expected)
	}
	if d.Expected == 0 {
		return fmt.Sprintf("byte %d: unexpected %q", d.Pos, d.Found)
	}
	return fmt.Sprintf("byte %d: found %q, expected %q", d.Pos, d.Found, d.Expected)
}

type undoRec struct {
	pos     int
	text    string
	deleted bool // true: text was deleted (undo re-inserts); false: text was inserted (undo deletes)
}

// Buffer is an editable text buffer with incremental bracket diagnostics.
type Buffer struct {
	root *node
	undo []undoRec
}

// New returns a Buffer holding text.
func New(text string) *Buffer {
	return &Buffer{root: insertText(nil, 0, text)}
}

// Len returns the buffer length in bytes.
func (b *Buffer) Len() int { return b.root.sz() }

// String returns the full buffer contents.
func (b *Buffer) String() string { return inorderString(b.root) }

// Insert inserts text at byte offset pos.
func (b *Buffer) Insert(pos int, text string) {
	if pos < 0 || pos > b.Len() {
		panic("bracketdiag: Insert out of range")
	}
	if text == "" {
		return
	}
	b.root = insertText(b.root, pos, text)
	b.undo = append(b.undo, undoRec{pos: pos, text: text})
}

// Delete removes n bytes starting at pos.
func (b *Buffer) Delete(pos, n int) {
	if pos < 0 || n < 0 || pos+n > b.Len() {
		panic("bracketdiag: Delete out of range")
	}
	if n == 0 {
		return
	}
	_, bc := splitNode(b.root, pos)
	mid, _ := splitNode(bc, n)
	b.undo = append(b.undo, undoRec{pos: pos, text: inorderString(mid), deleted: true})
	b.root = deleteRange(b.root, pos, pos+n)
}

// Undo reverts the most recent Insert or Delete. It reports whether there
// was anything to undo.
func (b *Buffer) Undo() bool {
	n := len(b.undo)
	if n == 0 {
		return false
	}
	rec := b.undo[n-1]
	b.undo = b.undo[:n-1]
	if rec.deleted {
		b.root = insertText(b.root, rec.pos, rec.text)
	} else {
		b.root = deleteRange(b.root, rec.pos, rec.pos+len(rec.text))
	}
	return true
}

// FirstError returns the first bracket error in [start, end), or nil. The
// lexical state (string/comment/escape) is inherited from the text before
// start; bracket matching is scoped to the range itself.
func (b *Buffer) FirstError(start, end int) *Diag {
	if start < 0 {
		start = 0
	}
	if end > b.Len() {
		end = b.Len()
	}
	if start >= end {
		return nil
	}
	st := sNormal
	var stack []brk
	if d := walk(b.root, 0, start, end, &st, &stack); d != nil {
		return d
	}
	if len(stack) > 0 {
		return &Diag{Pos: stack[0].off, Expected: closeOf(stack[0].ch)}
	}
	return nil
}

// absorb merges one reduced bracket sequence (at absolute offset base) into
// the running opener stack, returning a diagnostic at the first closing
// bracket that cannot be matched.
func absorb(seq []brk, base int, stack *[]brk) *Diag {
	i := 0
	for i < len(seq) && len(*stack) > 0 && isClose(seq[i].ch) && closeOf((*stack)[len(*stack)-1].ch) == seq[i].ch {
		*stack = (*stack)[:len(*stack)-1]
		i++
	}
	for j := i; j < len(seq); j++ {
		if isClose(seq[j].ch) {
			d := &Diag{Pos: base + seq[j].off, Found: seq[j].ch}
			if j > i {
				d.Expected = closeOf(seq[j-1].ch)
			} else if len(*stack) > 0 {
				d.Expected = closeOf((*stack)[len(*stack)-1].ch)
			}
			return d
		}
	}
	for ; i < len(seq); i++ {
		*stack = append(*stack, brk{seq[i].ch, base + seq[i].off})
	}
	return nil
}

// walk sweeps t's subtree (rooted at absolute offset base) from left to
// right. Subtrees before start only advance the lexical state via cached
// summaries; brackets are matched inside [start, end).
func walk(t *node, base, start, end int, st *state, stack *[]brk) *Diag {
	if t == nil || base >= end {
		return nil
	}
	if base+t.size <= start {
		*st = t.sum.by[*st].exit
		return nil
	}
	if base >= start && base+t.size <= end {
		seg := t.sum.by[*st]
		*st = seg.exit
		return absorb(seg.seq, base, stack)
	}
	ls := t.left.sz()
	if d := walk(t.left, base, start, end, st, stack); d != nil {
		return d
	}
	tb := base + ls
	te := tb + len(t.text)
	if te <= start {
		*st = leafSummary(t.text).by[*st].exit
	} else if tb < end {
		lo, hi := start-tb, end-tb
		if lo < 0 {
			lo = 0
		}
		if hi > len(t.text) {
			hi = len(t.text)
		}
		seg := scanSeg(t.text[:hi], *st, lo)
		*st = seg.exit
		if d := absorb(seg.seq, tb, stack); d != nil {
			return d
		}
	}
	return walk(t.right, te, start, end, st, stack)
}
