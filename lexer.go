// Package tpllex implements a streaming lexer for a template language that
// switches between plain text, expressions, and nestable raw strings.
//
// Mode switches are driven exclusively by explicit delimiters. The escape
// byte is only special in the modes that define escapes (text and
// expression); inside raw strings every byte is literal.
//
// The lexer is incremental: bytes fed in are consumed exactly once, so
// feeding input byte-by-byte yields the same token stream as feeding it in
// one chunk, and no already-consumed prefix is ever rescanned.
//
// A Lexer supports save/restore points (Mark/Reset). After a Reset, the
// tokens re-emitted and their raw source spans are identical to the ones
// produced before the reset.
package tpllex

import "strings"

// Mode is the lexical state of the lexer.
type Mode int

const (
	ModeText Mode = iota // plain text
	ModeExpr             // inside an expression {{ ... }}
	ModeRaw              // inside a raw string [[ ... ]] (nestable)
)

func (m Mode) String() string {
	switch m {
	case ModeText:
		return "text"
	case ModeExpr:
		return "expr"
	case ModeRaw:
		return "raw"
	}
	return "?"
}

// TokenKind classifies tokens.
type TokenKind int

const (
	KindText      TokenKind = iota // run of plain text (escapes resolved in Val)
	KindExprOpen                   // expression open delimiter
	KindExpr                       // expression content (escapes resolved in Val)
	KindExprClose                  // expression close delimiter
	KindRawOpen                    // raw-string open delimiter (emitted per nesting level)
	KindRaw                        // raw-string content, fully literal
	KindRawClose                   // raw-string close delimiter (emitted per nesting level)
	KindEOF                        // end of input
	KindError                      // lexical error (e.g. unterminated expression/raw string)
)

func (k TokenKind) String() string {
	switch k {
	case KindText:
		return "Text"
	case KindExprOpen:
		return "ExprOpen"
	case KindExpr:
		return "Expr"
	case KindExprClose:
		return "ExprClose"
	case KindRawOpen:
		return "RawOpen"
	case KindRaw:
		return "Raw"
	case KindRawClose:
		return "RawClose"
	case KindEOF:
		return "EOF"
	case KindError:
		return "Error"
	}
	return "?"
}

// Token is a lexical unit. Start and End are absolute byte offsets into the
// input stream; the raw source interval is input[Start:End]. For content
// tokens (Text/Expr) Val holds the escape-resolved text, which may differ
// from the raw interval; for all other kinds Val mirrors the raw bytes (or
// the error message for KindError).
type Token struct {
	Kind  TokenKind
	Start int
	End   int
	Val   string
}

// Config configures the delimiters and escape byte. Zero values fall back
// to the defaults: {{ }} for expressions, [[ ]] for raw strings, '\' as the
// escape byte.
type Config struct {
	ExprOpen  string
	ExprClose string
	RawOpen   string
	RawClose  string
	Escape    byte
}

func (c Config) withDefaults() Config {
	if c.ExprOpen == "" {
		c.ExprOpen = "{{"
	}
	if c.ExprClose == "" {
		c.ExprClose = "}}"
	}
	if c.RawOpen == "" {
		c.RawOpen = "[["
	}
	if c.RawClose == "" {
		c.RawClose = "]]"
	}
	if c.Escape == 0 {
		c.Escape = '\\'
	}
	return c
}

// markState is the full restorable state of a Lexer.
type markState struct {
	off       int
	mode      Mode
	depth     int
	tokActive bool
	tokKind   TokenKind
	tokStart  int
	tokVal    []byte
	qlen      int // absolute count of emitted tokens at mark time
	released  bool
}

// Mark is a save point. It stays valid until Release is called on it or
// until the lexer is reset to an older mark.
type Mark struct {
	id int
	st markState
}

// Lexer is a streaming template lexer. Feed input with Feed, signal end of
// input with Close, and pull tokens with Next.
type Lexer struct {
	cfg        Config
	textDelims []string // delimiters active in text mode
	rawDelims  []string // delimiters active in raw mode

	buf  []byte // raw bytes from absolute offset base
	base int    // absolute offset of buf[0]
	off  int    // absolute offset of the next byte to consume

	mode  Mode
	depth int // raw-string nesting depth

	tokActive bool      // a content token is being accumulated
	tokKind   TokenKind // kind of the accumulating token
	tokStart  int       // absolute offset where it started
	tokVal    []byte    // escape-resolved value accumulated so far

	queue       []Token // emitted tokens from absolute index emittedBase
	emittedBase int     // absolute index of queue[0]
	emitted     int     // total tokens ever emitted
	nextRead    int     // absolute index of the next token to hand out

	marks []markState // live marks, for buffer/queue retention

	eof     bool
	eofDone bool // EOF handling (trailing flush + EOF token) performed

	scanned int // total bytes consumed; test hook proving O(n) scanning
}

// New returns a Lexer for the given config.
func New(cfg Config) *Lexer {
	cfg = cfg.withDefaults()
	if cfg.ExprOpen == cfg.RawOpen {
		panic("tpllex: expression and raw open delimiters must differ")
	}
	return &Lexer{
		cfg:        cfg,
		textDelims: []string{cfg.ExprOpen, cfg.RawOpen},
		rawDelims:  []string{cfg.RawOpen, cfg.RawClose},
		mode:       ModeText,
	}
}

// Feed appends input bytes and lexes as far as possible. It panics if
// called after Close.
func (l *Lexer) Feed(p []byte) {
	if l.eof {
		panic("tpllex: Feed after Close")
	}
	l.buf = append(l.buf, p...)
	l.pump()
}

// Close signals end of input and flushes any trailing token.
func (l *Lexer) Close() {
	if l.eof {
		return
	}
	l.eof = true
	l.pump()
}

// Next returns the next token. ok is false when more input is needed
// (call Feed or Close first). After Close, the stream ends with a KindEOF
// token; further calls keep returning KindEOF.
func (l *Lexer) Next() (tok Token, ok bool) {
	for l.nextRead >= l.emitted {
		if l.eofDone {
			return Token{Kind: KindEOF, Start: l.off, End: l.off}, true
		}
		before := l.emitted
		l.pump()
		if l.emitted == before {
			return Token{}, false
		}
	}
	t := l.queue[l.nextRead-l.emittedBase]
	l.nextRead++
	l.compact()
	return t, true
}

// Mark saves the current lexer state. Tokens already returned by Next are
// unaffected; tokens emitted after the mark are withdrawn on Reset.
func (l *Lexer) Mark() Mark {
	st := markState{
		off:       l.off,
		mode:      l.mode,
		depth:     l.depth,
		tokActive: l.tokActive,
		tokKind:   l.tokKind,
		tokStart:  l.tokStart,
		tokVal:    append([]byte(nil), l.tokVal...),
		qlen:      l.emitted,
	}
	l.marks = append(l.marks, st)
	return Mark{id: len(l.marks) - 1, st: st}
}

// Reset restores the lexer to a previously saved mark. Marks created after
// m are invalidated. It panics if m was released or its state was compacted.
func (l *Lexer) Reset(m Mark) {
	st := m.st
	if st.released || st.off < l.base || st.qlen < l.emittedBase {
		panic("tpllex: reset to a released or expired mark")
	}
	l.off = st.off
	l.mode = st.mode
	l.depth = st.depth
	l.tokActive = st.tokActive
	l.tokKind = st.tokKind
	l.tokStart = st.tokStart
	l.tokVal = append(l.tokVal[:0], st.tokVal...)
	l.emitted = st.qlen
	l.queue = l.queue[:st.qlen-l.emittedBase]
	if l.nextRead > st.qlen {
		l.nextRead = st.qlen
	}
	l.marks = l.marks[:m.id+1]
	l.eofDone = false
}

// Release invalidates a mark and frees the history it pins down.
func (l *Lexer) Release(m Mark) {
	l.marks[m.id].released = true
	l.marks[m.id].off = -1
	l.marks[m.id].qlen = 1<<62 - 1
	l.compact()
}

// pump runs the mode-specific scanners until no further progress can be
// made (need more input) and performs end-of-input handling once.
func (l *Lexer) pump() {
	for {
		var progressed bool
		switch l.mode {
		case ModeText:
			progressed = l.scanDelimited(l.textDelims, KindText, l.onTextDelim)
		case ModeExpr:
			progressed = l.scanDelimited([]string{l.cfg.ExprClose}, KindExpr, l.onExprDelim)
		case ModeRaw:
			progressed = l.scanRaw()
		}
		if progressed {
			continue
		}
		if l.eof && !l.eofDone && l.off-l.base == len(l.buf) {
			l.eofDone = true
			l.flush()
			switch l.mode {
			case ModeExpr:
				l.emit(Token{Kind: KindError, Start: l.off, End: l.off, Val: "unterminated expression"})
			case ModeRaw:
				l.emit(Token{Kind: KindError, Start: l.off, End: l.off, Val: "unterminated raw string"})
			}
			l.emit(Token{Kind: KindEOF, Start: l.off, End: l.off})
		}
		return
	}
}

// scanDelimited scans content in text or expression mode: bytes are literal
// except the escape byte and the given delimiters. onDelim is invoked when a
// delimiter is consumed. Returns true if any byte was consumed.
func (l *Lexer) scanDelimited(delims []string, kind TokenKind, onDelim func(d string)) bool {
	rest := l.buf[l.off-l.base:]
	i := 0
	for i < len(rest) {
		c := rest[i]
		if c == l.cfg.Escape {
			if i+1 >= len(rest) {
				if !l.eof {
					break // wait for the escaped byte
				}
				l.appendVal(kind, l.off+i, c) // trailing escape at EOF is literal
				i++
				continue
			}
			n := rest[i+1]
			if n == l.cfg.Escape || delimStartsWith(delims, n) {
				l.appendVal(kind, l.off+i, n) // escaped byte, taken literally
				i += 2
				continue
			}
			l.appendVal(kind, l.off+i, c) // escape before ordinary byte is literal
			i++
			continue
		}
		d, need := matchDelims(rest[i:], delims, l.eof)
		if need {
			break // proper prefix of a delimiter at end of buffer: wait
		}
		if d != "" {
			l.off += i
			l.scanned += i
			l.flush()
			onDelim(d)
			return true
		}
		l.appendVal(kind, l.off+i, c)
		i++
	}
	l.off += i
	l.scanned += i
	return i > 0
}

// scanRaw scans raw-string content: no escapes, only the raw open/close
// delimiters are special, and opens nest.
func (l *Lexer) scanRaw() bool {
	rest := l.buf[l.off-l.base:]
	i := 0
	for i < len(rest) {
		d, need := matchDelims(rest[i:], l.rawDelims, l.eof)
		if need {
			break
		}
		if d != "" {
			l.off += i
			l.scanned += i
			l.flush()
			start := l.off
			l.off += len(d)
			l.scanned += len(d)
			if d == l.cfg.RawOpen {
				l.depth++
				l.emit(Token{Kind: KindRawOpen, Start: start, End: l.off, Val: d})
			} else {
				l.depth--
				l.emit(Token{Kind: KindRawClose, Start: start, End: l.off, Val: d})
				if l.depth == 0 {
					l.mode = ModeText
				}
			}
			return true
		}
		l.appendVal(KindRaw, l.off+i, rest[i])
		i++
	}
	l.off += i
	l.scanned += i
	return i > 0
}

func (l *Lexer) onTextDelim(d string) {
	start := l.off
	l.off += len(d)
	l.scanned += len(d)
	switch d {
	case l.cfg.ExprOpen:
		l.emit(Token{Kind: KindExprOpen, Start: start, End: l.off, Val: d})
		l.mode = ModeExpr
	case l.cfg.RawOpen:
		l.emit(Token{Kind: KindRawOpen, Start: start, End: l.off, Val: d})
		l.mode = ModeRaw
		l.depth = 1
	}
}

func (l *Lexer) onExprDelim(d string) {
	start := l.off
	l.off += len(d)
	l.scanned += len(d)
	l.emit(Token{Kind: KindExprClose, Start: start, End: l.off, Val: d})
	l.mode = ModeText
}

// appendVal adds one escape-resolved byte to the accumulating content token
// that spans raw input starting at abs.
func (l *Lexer) appendVal(kind TokenKind, abs int, b byte) {
	if !l.tokActive {
		l.tokActive = true
		l.tokKind = kind
		l.tokStart = abs
	}
	l.tokVal = append(l.tokVal, b)
}

// flush emits the accumulating content token, if any, ending at l.off.
func (l *Lexer) flush() {
	if !l.tokActive {
		return
	}
	l.emit(Token{Kind: l.tokKind, Start: l.tokStart, End: l.off, Val: string(l.tokVal)})
	l.tokActive = false
	l.tokVal = l.tokVal[:0]
}

func (l *Lexer) emit(t Token) {
	l.queue = append(l.queue, t)
	l.emitted++
}

// compact drops consumed input bytes and consumed tokens that no live mark
// can still reference.
func (l *Lexer) compact() {
	retain := l.off
	if l.tokActive && l.tokStart < retain {
		retain = l.tokStart
	}
	qRetain := l.nextRead
	for _, m := range l.marks {
		if m.released {
			continue
		}
		if m.off < retain {
			retain = m.off
		}
		if m.qlen < qRetain {
			qRetain = m.qlen
		}
	}
	if retain > l.base {
		n := retain - l.base
		copy(l.buf, l.buf[n:])
		l.buf = l.buf[:len(l.buf)-n]
		l.base = retain
	}
	if qRetain > l.emittedBase {
		n := qRetain - l.emittedBase
		copy(l.queue, l.queue[n:])
		l.queue = l.queue[:len(l.queue)-n]
		l.emittedBase = qRetain
	}
}

// matchDelims finds the longest delimiter matching a prefix of rest. If no
// delimiter matches but rest itself is a proper prefix of some delimiter and
// more input may still arrive (eof is false), needMore is true.
func matchDelims(rest []byte, delims []string, eof bool) (match string, needMore bool) {
	for _, d := range delims {
		if len(d) <= len(rest) && string(rest[:len(d)]) == d && len(d) > len(match) {
			match = d
		}
	}
	if match != "" && len(match) < len(rest) {
		return match, false
	}
	// The buffer tail is fully consumed by the match (or nothing matched):
	// it could still grow into a longer delimiter once more input arrives.
	if !eof && len(rest) > 0 {
		tail := string(rest)
		for _, d := range delims {
			if len(d) > len(tail) && strings.HasPrefix(d, tail) {
				return "", true
			}
		}
	}
	return match, false
}

func delimStartsWith(delims []string, b byte) bool {
	for _, d := range delims {
		if d[0] == b {
			return true
		}
	}
	return false
}
