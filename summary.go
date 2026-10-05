// Package bracketdiag provides an incremental bracket-diagnostic layer over
// an editable text buffer. Brackets inside strings or comments are ignored,
// escapes and comment/string state may span block boundaries, and each block
// caches a mergeable summary so edits only recompute the affected path.
package bracketdiag

// state is the lexical state of the byte-level DFA. Cross-block states such
// as sDqEsc (escape pending inside a string) and sSlash (a lone '/' seen in
// normal code) let escapes and comment openers span block boundaries.
type state int

const (
	sNormal state = iota
	sSlash
	sDq
	sDqEsc
	sSq
	sSqEsc
	sLine
	sBlock
	sBlockStar
	numStates
)

func isOpen(c byte) bool  { return c == '(' || c == '[' || c == '{' }
func isClose(c byte) bool { return c == ')' || c == ']' || c == '}' }

func closeOf(c byte) byte {
	switch c {
	case '(':
		return ')'
	case '[':
		return ']'
	case '{':
		return '}'
	}
	return 0
}

// brk is a bracket occurrence; off is relative to the start of the segment
// the enclosing summary describes.
type brk struct {
	ch  byte
	off int
}

// segSummary is the result of running one segment of text from one entry
// state: the exit state plus the reduced bracket sequence. The sequence is
// fully reduced (no opening bracket is immediately followed by its matching
// closing bracket), which makes segment composition associative.
type segSummary struct {
	exit state
	seq  []brk
}

// summary maps every possible entry state to the resulting segSummary.
type summary struct {
	by [numStates]segSummary
}

func identitySummary() summary {
	var s summary
	for i := range s.by {
		s.by[i].exit = state(i)
	}
	return s
}

// scanSeg runs the DFA over text starting in entry. Brackets at offsets
// below minPos advance the lexical state but are not recorded (used when a
// query range starts inside a block).
func scanSeg(text string, entry state, minPos int) segSummary {
	st := entry
	var seq []brk
	record := func(c byte, i int) {
		if isOpen(c) {
			seq = append(seq, brk{c, i})
			return
		}
		if n := len(seq); n > 0 && isOpen(seq[n-1].ch) && closeOf(seq[n-1].ch) == c {
			seq = seq[:n-1]
		} else {
			seq = append(seq, brk{c, i})
		}
	}
	i := 0
	for i < len(text) {
		c := text[i]
		advance := true
		switch st {
		case sNormal:
			switch c {
			case '/':
				st = sSlash
			case '"':
				st = sDq
			case '\'':
				st = sSq
			default:
				if (isOpen(c) || isClose(c)) && i >= minPos {
					record(c, i)
				}
			}
		case sSlash:
			switch c {
			case '/':
				st = sLine
			case '*':
				st = sBlock
			default:
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
	return segSummary{exit: st, seq: seq}
}

func leafSummary(text string) summary {
	var s summary
	for st := 0; st < int(numStates); st++ {
		s.by[st] = scanSeg(text, state(st), 0)
	}
	return s
}

// mergeSeq reduces a++b given that a and b are already reduced; bOff is the
// absolute offset of b's first byte.
func mergeSeq(a, b []brk, bOff int) []brk {
	i, j := len(a), 0
	for i > 0 && j < len(b) && isOpen(a[i-1].ch) && closeOf(a[i-1].ch) == b[j].ch {
		i--
		j++
	}
	out := make([]brk, 0, i+len(b)-j)
	out = append(out, a[:i]...)
	for k := j; k < len(b); k++ {
		out = append(out, brk{b[k].ch, b[k].off + bOff})
	}
	return out
}

// mergeSumm composes two adjacent segment summaries; bOff is the length of
// the segment a describes.
func mergeSumm(a, b summary, bOff int) summary {
	var r summary
	for s := 0; s < int(numStates); s++ {
		as := a.by[s]
		bs := b.by[as.exit]
		r.by[s].exit = bs.exit
		r.by[s].seq = mergeSeq(as.seq, bs.seq, bOff)
	}
	return r
}
