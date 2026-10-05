// Package lex implements a streaming lexer for a small template language.
//
// The lexer switches between three modes, driven purely by explicit
// delimiters:
//
//	text: plain text. "{{" opens an expression. The escapes "\{" and "\\"
//	      are recognized only in this mode.
//	expr: inside "{{ ... }}". Whitespace is skipped; identifiers, numbers,
//	      "..." strings and single-character punctuation are emitted.
//	      String escapes (\n, \t, \\, \") apply only inside "...".
//	      "[[" opens a raw string; "}}" closes the expression.
//	raw:  inside "[[ ... ]]". Completely literal: no escapes are
//	      recognized. "[[" increments the nesting depth and "]]"
//	      decrements it; the raw string ends when the depth reaches 0,
//	      so raw strings nest.
//
// Input is streamed: call Feed as bytes arrive (any chunking, including
// one byte at a time) and pull tokens with Next. Next returns ErrNeedMore
// when the current buffer is insufficient to decide the next token. Call
// Close when the input is finished; EOF is then reported in whatever mode
// the lexer happens to be in (an unterminated expression, string or raw
// string yields a TokenError followed by TokenEOF).
//
// Scanning is incremental: each incoming byte is examined O(1) times and
// already-consumed input is dropped from the internal buffer, so the lexer
// never rescans the prefix of the input.
//
// A snapshot of the full lexer state can be taken with Save and later
// reinstated with Restore. After Restore, feeding the same bytes produces
// exactly the same tokens with the same raw source spans.
package lex

import "errors"

// ErrNeedMore is returned by Next when more input is required to produce
// the next token. Feed more bytes (or Close) and call Next again.
var ErrNeedMore = errors.New("lex: more input required")

// TokenType identifies the kind of a Token.
type TokenType int

const (
	TokenEOF TokenType = iota
	TokenError
	TokenText
	TokenExprOpen  // {{
	TokenExprClose // }}
	TokenIdent
	TokenNumber
	TokenString
	TokenRawString
	TokenPunct
)

func (t TokenType) String() string {
	switch t {
	case TokenEOF:
		return "EOF"
	case TokenError:
		return "Error"
	case TokenText:
		return "Text"
	case TokenExprOpen:
		return "ExprOpen"
	case TokenExprClose:
		return "ExprClose"
	case TokenIdent:
		return "Ident"
	case TokenNumber:
		return "Number"
	case TokenString:
		return "String"
	case TokenRawString:
		return "RawString"
	case TokenPunct:
		return "Punct"
	}
	return "Unknown"
}

// Token is a lexical unit. Pos and End are absolute byte offsets into the
// input stream: source[Pos:End] is the raw span of the token. Val is the
// decoded value (escapes resolved; for raw strings the inner content
// without the outer [[ ]] delimiters).
type Token struct {
	Type TokenType
	Val  string
	Pos  int
	End  int
}

// Mode is the lexical mode the scanner is currently in.
type Mode int

const (
	ModeText Mode = iota
	ModeExpr
	ModeStr // inside "..." within an expression
	ModeRaw // inside [[ ... ]] within an expression
)

// Lexer is a streaming template lexer. The zero value is not ready for
// use; create one with New.
type Lexer struct {
	buf    []byte // buffered, not yet consumed input
	off    int    // consumed prefix of buf
	base   int    // absolute offset of buf[0]
	closed bool   // Close was called: no more input will arrive

	mode  Mode
	depth int // raw-string nesting depth (valid in ModeRaw)

	val       []byte    // decoded value of the token being assembled
	tokStart  int       // absolute offset where the current token started
	tokActive bool      // a token is being assembled
	tokKind   TokenType // kind of the token being assembled

	pending []Token // completed tokens not yet returned by Next

	steps int // instrumentation: number of scanner steps (tests only)
}

// New returns a Lexer ready to accept input via Feed.
func New() *Lexer {
	return &Lexer{mode: ModeText}
}

// Feed appends input bytes. It may be called any number of times with any
// chunking. Feed must not be called after Close.
func (l *Lexer) Feed(p []byte) {
	l.buf = append(l.buf, p...)
}

// Close signals that no more input will arrive. After Close, Next drains
// the remaining buffered input and then reports EOF (or an error token for
// a construct left unterminated in the current mode).
func (l *Lexer) Close() {
	l.closed = true
}

// Buffered returns the number of fed but not yet consumed bytes.
func (l *Lexer) Buffered() int {
	return len(l.buf) - l.off
}

// State is a snapshot of a Lexer, produced by Save.
type State struct {
	buf       []byte
	off       int
	base      int
	closed    bool
	mode      Mode
	depth     int
	val       []byte
	tokStart  int
	tokActive bool
	tokKind   TokenType
	pending   []Token
}

// Save captures the complete lexer state, including unconsumed buffered
// input and any partially assembled token. The returned State is
// independent of the Lexer and may be restored any number of times.
func (l *Lexer) Save() State {
	return State{
		buf:       append([]byte(nil), l.buf...),
		off:       l.off,
		base:      l.base,
		closed:    l.closed,
		mode:      l.mode,
		depth:     l.depth,
		val:       append([]byte(nil), l.val...),
		tokStart:  l.tokStart,
		tokActive: l.tokActive,
		tokKind:   l.tokKind,
		pending:   append([]Token(nil), l.pending...),
	}
}

// Restore reinstates a state previously captured with Save. All input fed
// after the Save is discarded and must be fed again; re-feeding the same
// bytes reproduces the same tokens with the same raw spans.
func (l *Lexer) Restore(s State) {
	l.buf = append([]byte(nil), s.buf...)
	l.off = s.off
	l.base = s.base
	l.closed = s.closed
	l.mode = s.mode
	l.depth = s.depth
	l.val = append([]byte(nil), s.val...)
	l.tokStart = s.tokStart
	l.tokActive = s.tokActive
	l.tokKind = s.tokKind
	l.pending = append([]Token(nil), s.pending...)
}

// Next returns the next token. It returns ErrNeedMore if the buffered
// input is insufficient and Close has not been called.
func (l *Lexer) Next() (Token, error) {
	for {
		if len(l.pending) > 0 {
			t := l.pending[0]
			l.pending = l.pending[1:]
			return t, nil
		}
		if l.off < len(l.buf) {
			l.steps++
			if l.step() {
				continue
			}
			// The scanner is waiting for a lookahead byte that has
			// not arrived yet.
			return Token{}, ErrNeedMore
		}
		l.compact()
		if !l.closed {
			return Token{}, ErrNeedMore
		}
		return l.atEOF()
	}
}

// abs returns the absolute offset of the next unconsumed byte.
func (l *Lexer) abs() int {
	return l.base + l.off
}

// compact drops the consumed prefix of buf. Consumed bytes are never
// needed again, so the buffer never holds more than the current token.
func (l *Lexer) compact() {
	if l.off == 0 {
		return
	}
	n := copy(l.buf, l.buf[l.off:])
	l.buf = l.buf[:n]
	l.base += l.off
	l.off = 0
}

// peek returns the lookahead byte after the current one.
func (l *Lexer) peek() (byte, bool) {
	if l.off+1 < len(l.buf) {
		return l.buf[l.off+1], true
	}
	return 0, false
}

// begin starts assembling a token of the given kind at the current
// position. It is a no-op if a token is already being assembled.
func (l *Lexer) begin(kind TokenType) {
	if !l.tokActive {
		l.tokActive = true
		l.tokKind = kind
		l.tokStart = l.abs()
	}
}

// emitTok completes the token being assembled and queues it.
func (l *Lexer) emitTok(kind TokenType) {
	l.pending = append(l.pending, Token{
		Type: kind,
		Val:  string(l.val),
		Pos:  l.tokStart,
		End:  l.abs(),
	})
	l.val = l.val[:0]
	l.tokActive = false
}

// emit queues a fully formed token.
func (l *Lexer) emit(t Token) {
	l.pending = append(l.pending, t)
}

// step consumes input for one scanner action. It reports whether progress
// was made; a false result means the scanner needs a lookahead byte that
// has not arrived yet (only possible before Close).
func (l *Lexer) step() bool {
	c := l.buf[l.off]
	switch l.mode {
	case ModeText:
		return l.stepText(c)
	case ModeExpr:
		return l.stepExpr(c)
	case ModeStr:
		return l.stepStr(c)
	case ModeRaw:
		return l.stepRaw(c)
	}
	panic("lex: invalid mode")
}

func (l *Lexer) stepText(c byte) bool {
	switch c {
	case '\\':
		n, ok := l.peek()
		if !ok {
			if !l.closed {
				return false
			}
			l.begin(TokenText)
			l.val = append(l.val, '\\')
			l.off++
			return true
		}
		l.begin(TokenText)
		if n == '{' || n == '\\' {
			// Text-mode escapes: "\{" and "\\".
			l.val = append(l.val, n)
			l.off += 2
			return true
		}
		// A backslash before any other byte is literal.
		l.val = append(l.val, '\\')
		l.off++
		return true
	case '{':
		n, ok := l.peek()
		if !ok {
			if !l.closed {
				return false
			}
			l.begin(TokenText)
			l.val = append(l.val, '{')
			l.off++
			return true
		}
		if n == '{' {
			if l.tokActive {
				l.emitTok(TokenText)
			}
			start := l.abs()
			l.off += 2
			l.mode = ModeExpr
			l.emit(Token{Type: TokenExprOpen, Val: "{{", Pos: start, End: l.abs()})
			return true
		}
		l.begin(TokenText)
		l.val = append(l.val, '{')
		l.off++
		return true
	default:
		l.begin(TokenText)
		l.val = append(l.val, c)
		l.off++
		return true
	}
}

func (l *Lexer) stepExpr(c byte) bool {
	if l.tokActive {
		// Continue an identifier or number, or terminate it at the
		// first byte that cannot extend it.
		var cont bool
		if l.tokKind == TokenIdent {
			cont = isIdent(c)
		} else {
			cont = isDigit(c) || c == '.'
		}
		if cont {
			l.val = append(l.val, c)
			l.off++
			return true
		}
		l.emitTok(l.tokKind)
		return true // c itself is re-examined on the next step
	}
	switch {
	case isSpace(c):
		l.off++
	case c == '}':
		n, ok := l.peek()
		if !ok {
			if !l.closed {
				return false
			}
			l.punct(c)
			return true
		}
		if n == '}' {
			start := l.abs()
			l.off += 2
			l.mode = ModeText
			l.emit(Token{Type: TokenExprClose, Val: "}}", Pos: start, End: l.abs()})
			return true
		}
		l.punct(c)
	case c == '[':
		n, ok := l.peek()
		if !ok {
			if !l.closed {
				return false
			}
			l.punct(c)
			return true
		}
		if n == '[' {
			l.begin(TokenRawString)
			l.off += 2
			l.mode = ModeRaw
			l.depth = 1
			return true
		}
		l.punct(c)
	case c == '"':
		l.begin(TokenString)
		l.off++
		l.mode = ModeStr
	case isAlpha(c):
		l.begin(TokenIdent)
		l.val = append(l.val, c)
		l.off++
	case isDigit(c):
		l.begin(TokenNumber)
		l.val = append(l.val, c)
		l.off++
	default:
		l.punct(c)
	}
	return true
}

func (l *Lexer) stepStr(c byte) bool {
	switch c {
	case '\\':
		n, ok := l.peek()
		if !ok {
			if !l.closed {
				return false
			}
			// Dangling backslash at EOF.
			l.off++
			l.failUnterminated("unterminated string")
			return true
		}
		l.val = append(l.val, unescape(n))
		l.off += 2
		return true
	case '"':
		l.off++
		l.emitTok(TokenString)
		l.mode = ModeExpr
		return true
	default:
		l.val = append(l.val, c)
		l.off++
		return true
	}
}

func (l *Lexer) stepRaw(c byte) bool {
	switch c {
	case '[':
		n, ok := l.peek()
		if !ok {
			if !l.closed {
				return false
			}
			l.val = append(l.val, '[')
			l.off++
			return true
		}
		if n == '[' {
			l.val = append(l.val, '[', '[')
			l.off += 2
			l.depth++
			return true
		}
		l.val = append(l.val, '[')
		l.off++
		return true
	case ']':
		n, ok := l.peek()
		if !ok {
			if !l.closed {
				return false
			}
			l.val = append(l.val, ']')
			l.off++
			return true
		}
		if n == ']' {
			l.off += 2
			l.depth--
			if l.depth == 0 {
				l.emitTok(TokenRawString)
				l.mode = ModeExpr
			} else {
				l.val = append(l.val, ']', ']')
			}
			return true
		}
		l.val = append(l.val, ']')
		l.off++
		return true
	default:
		// Raw mode: no escapes, every byte is literal.
		l.val = append(l.val, c)
		l.off++
		return true
	}
}

func (l *Lexer) punct(c byte) {
	start := l.abs()
	l.off++
	l.emit(Token{Type: TokenPunct, Val: string(c), Pos: start, End: l.abs()})
}

// failUnterminated queues an error token for a construct cut off by EOF
// and returns the lexer to text mode so the next Next call yields EOF.
func (l *Lexer) failUnterminated(msg string) {
	l.pending = append(l.pending, Token{
		Type: TokenError,
		Val:  msg,
		Pos:  l.tokStart,
		End:  l.abs(),
	})
	l.val = l.val[:0]
	l.tokActive = false
	l.mode = ModeText
}

// atEOF produces the final token(s) once the input is closed and drained.
func (l *Lexer) atEOF() (Token, error) {
	pos := l.abs()
	if l.tokActive {
		switch l.tokKind {
		case TokenText, TokenIdent, TokenNumber:
			t := Token{Type: l.tokKind, Val: string(l.val), Pos: l.tokStart, End: pos}
			l.val = l.val[:0]
			l.tokActive = false
			return t, nil
		}
	}
	switch l.mode {
	case ModeText:
		return Token{Type: TokenEOF, Pos: pos, End: pos}, nil
	case ModeExpr:
		l.mode = ModeText
		return Token{Type: TokenError, Val: "unterminated expression", Pos: pos, End: pos}, nil
	case ModeStr:
		l.failUnterminated("unterminated string")
	case ModeRaw:
		l.failUnterminated("unterminated raw string")
	}
	t := l.pending[0]
	l.pending = l.pending[1:]
	return t, nil
}

func unescape(c byte) byte {
	switch c {
	case 'n':
		return '\n'
	case 't':
		return '\t'
	case '\\':
		return '\\'
	case '"':
		return '"'
	}
	return c
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
func isDigit(c byte) bool { return '0' <= c && c <= '9' }
func isAlpha(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || c == '_' }
func isIdent(c byte) bool { return isAlpha(c) || isDigit(c) }
