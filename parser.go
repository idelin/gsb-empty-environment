package streamdec

import (
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// maxDepth bounds object nesting so hostile input cannot exhaust the stack.
const maxDepth = 128

type pstate int

const (
	psValue      pstate = iota // expecting a value
	psKeyOrEnd                 // just after '{': expecting '"' or '}'
	psKeyNext                  // just after ',': expecting '"'
	psColon                    // expecting ':'
	psString                   // inside a string (key or value)
	psEscape                   // just after '\' in a string
	psUnicode                  // reading 4 hex digits of \uXXXX
	psNumber                   // inside a number token
	psLiteral                  // inside true / false / null
	psCommaOrEnd               // after a value: expecting ',' or '}'
	psDone                     // top-level object closed; only whitespace allowed
)

// numState is a DFA for the JSON number grammar:
// -? (0 | [1-9][0-9]*) ('.' [0-9]+)? ([eE] [+-]? [0-9]+)?
type numState int

const (
	nsBegin numState = iota
	nsMinus
	nsZero
	nsInt
	nsDot
	nsFrac
	nsExp
	nsExpSign
	nsExpD
)

type objFrame struct {
	fieldPending bool   // a key+colon was seen, its value is being parsed
	field        string // most recent key in this object
}

// parser is an incremental parser for the supported serialization subset.
// It can be suspended after any byte and resumed later; all state lives in
// the struct. Parsing never executes host code: bytes only drive the state
// machine and fill buffers.
type parser struct {
	state  pstate
	stack  []objFrame
	events []Event // buffered until the top-level object completes

	buf      []byte // current string or number token
	inKey    bool   // buf holds a key (vs a string value)
	tokStart int64  // absolute offset of the current token

	high    rune // pending high surrogate from \uXXXX
	needLow bool // next escape must be a low surrogate
	uni     rune // accumulator for \uXXXX
	uniRem  int  // hex digits remaining

	num    numState
	lit    string // literal being matched ("true"/"false"/"null")
	litIdx int
	litVal any

	started  bool // any value begun (used to reject top-level scalars)
	complete bool // top-level object just closed
}

func (p *parser) reset() {
	p.state = psValue
	p.stack = p.stack[:0]
	p.events = nil
	p.buf = p.buf[:0]
	p.inKey = false
	p.tokStart = 0
	p.high = 0
	p.needLow = false
	p.uni = 0
	p.uniRem = 0
	p.num = nsBegin
	p.lit = ""
	p.litIdx = 0
	p.litVal = nil
	p.started = false
	p.complete = false
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }
func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func hexVal(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	default:
		return -1
	}
}

func (p *parser) emit(ev Event) { p.events = append(p.events, ev) }

func (p *parser) emitValue(v any, off int64) {
	ev := Event{Kind: EventValue, Value: v, Offset: off}
	if len(p.stack) > 0 {
		ev.Field = p.stack[len(p.stack)-1].field
	}
	p.emit(ev)
}

// valueComplete is called when any value (scalar or closed object) ends.
func (p *parser) valueComplete(off int64) {
	top := &p.stack[len(p.stack)-1]
	if top.fieldPending {
		p.emit(Event{Kind: EventFieldEnd, Field: top.field, Offset: off})
		top.fieldPending = false
	}
	p.state = psCommaOrEnd
}

func (p *parser) closeObject(off int64) {
	p.emit(Event{Kind: EventObjectEnd, Offset: off})
	p.stack = p.stack[:len(p.stack)-1]
	if len(p.stack) == 0 {
		p.complete = true
		p.state = psDone
		return
	}
	p.valueComplete(off)
}

func (p *parser) beginString(inKey bool, off int64) {
	p.buf = p.buf[:0]
	p.inKey = inKey
	p.tokStart = off
	p.needLow = false
	p.state = psString
}

func (p *parser) beginLiteral(word string, val any, off int64) {
	p.state = psLiteral
	p.lit = word
	p.litIdx = 1 // first byte already consumed
	p.litVal = val
	p.tokStart = off
}

// feed consumes one byte at absolute offset off.
func (p *parser) feed(b byte, off int64) error {
	switch p.state {
	case psValue:
		return p.feedValue(b, off)
	case psKeyOrEnd:
		switch {
		case isSpace(b):
			return nil
		case b == '}':
			p.closeObject(off)
			return nil
		case b == '"':
			p.beginString(true, off)
			return nil
		default:
			return errf(off, "expected object key or '}', got %q", b)
		}
	case psKeyNext:
		switch {
		case isSpace(b):
			return nil
		case b == '"':
			p.beginString(true, off)
			return nil
		default:
			return errf(off, "expected object key after ',', got %q", b)
		}
	case psColon:
		switch {
		case isSpace(b):
			return nil
		case b == ':':
			p.stack[len(p.stack)-1].fieldPending = true
			p.state = psValue
			return nil
		default:
			return errf(off, "expected ':', got %q", b)
		}
	case psString:
		return p.feedString(b, off)
	case psEscape:
		return p.feedEscape(b, off)
	case psUnicode:
		return p.feedUnicode(b, off)
	case psNumber:
		if isSpace(b) || b == ',' || b == '}' {
			if err := p.finalizeNumber(off); err != nil {
				return err
			}
			return p.feed(b, off) // reprocess the delimiter
		}
		return p.feedNumber(b, off)
	case psLiteral:
		if b != p.lit[p.litIdx] {
			return errf(off, "invalid literal, expected %q", p.lit)
		}
		p.litIdx++
		if p.litIdx == len(p.lit) {
			p.emitValue(p.litVal, p.tokStart)
			p.valueComplete(off)
		}
		return nil
	case psCommaOrEnd:
		switch {
		case isSpace(b):
			return nil
		case b == ',':
			p.state = psKeyNext
			return nil
		case b == '}':
			p.closeObject(off)
			return nil
		default:
			return errf(off, "expected ',' or '}', got %q", b)
		}
	case psDone:
		if isSpace(b) {
			return nil
		}
		return errf(off, "unexpected data after top-level object")
	}
	return errf(off, "internal error: bad parser state")
}

func (p *parser) feedValue(b byte, off int64) error {
	if isSpace(b) {
		return nil
	}
	if len(p.stack) == 0 && p.started {
		return errf(off, "unexpected data after top-level value")
	}
	p.started = true
	switch {
	case b == '{':
		if len(p.stack) >= maxDepth {
			return errf(off, "nesting depth exceeds %d", maxDepth)
		}
		ev := Event{Kind: EventObjectStart, Offset: off}
		if len(p.stack) > 0 {
			ev.Field = p.stack[len(p.stack)-1].field
		}
		p.emit(ev)
		p.stack = append(p.stack, objFrame{})
		p.state = psKeyOrEnd
		return nil
	case b == '"':
		if len(p.stack) == 0 {
			return errf(off, "top-level value must be an object")
		}
		p.beginString(false, off)
		return nil
	case b == 't':
		if len(p.stack) == 0 {
			return errf(off, "top-level value must be an object")
		}
		p.beginLiteral("true", true, off)
		return nil
	case b == 'f':
		if len(p.stack) == 0 {
			return errf(off, "top-level value must be an object")
		}
		p.beginLiteral("false", false, off)
		return nil
	case b == 'n':
		if len(p.stack) == 0 {
			return errf(off, "top-level value must be an object")
		}
		p.beginLiteral("null", nil, off)
		return nil
	case b == '-' || isDigit(b):
		if len(p.stack) == 0 {
			return errf(off, "top-level value must be an object")
		}
		p.state = psNumber
		p.num = nsBegin
		p.buf = p.buf[:0]
		p.tokStart = off
		return p.feedNumber(b, off)
	default:
		return errf(off, "unexpected byte %q while expecting a value", b)
	}
}

func (p *parser) feedString(b byte, off int64) error {
	switch {
	case b == '"':
		if p.needLow {
			return errf(off, "unterminated surrogate pair in string")
		}
		if !utf8.Valid(p.buf) {
			return errf(off, "invalid UTF-8 in string")
		}
		s := string(p.buf)
		if p.inKey {
			p.stack[len(p.stack)-1].field = s
			p.emit(Event{Kind: EventFieldStart, Field: s, Offset: p.tokStart})
			p.state = psColon
		} else {
			p.emitValue(s, p.tokStart)
			p.valueComplete(off)
		}
		return nil
	case b == '\\':
		p.state = psEscape
		return nil
	case b < 0x20:
		return errf(off, "unescaped control character in string")
	default:
		p.buf = append(p.buf, b)
		return nil
	}
}

func (p *parser) feedEscape(b byte, off int64) error {
	if p.needLow && b != 'u' {
		return errf(off, "expected low-surrogate escape after high surrogate")
	}
	var c byte
	switch b {
	case '"':
		c = '"'
	case '\\':
		c = '\\'
	case '/':
		c = '/'
	case 'b':
		c = '\b'
	case 'f':
		c = '\f'
	case 'n':
		c = '\n'
	case 'r':
		c = '\r'
	case 't':
		c = '\t'
	case 'u':
		p.state = psUnicode
		p.uni = 0
		p.uniRem = 4
		return nil
	default:
		return errf(off, "invalid escape sequence \\%c", b)
	}
	p.buf = append(p.buf, c)
	p.state = psString
	return nil
}

func (p *parser) feedUnicode(b byte, off int64) error {
	v := hexVal(b)
	if v < 0 {
		return errf(off, "invalid hex digit %q in \\u escape", b)
	}
	p.uni = p.uni*16 + rune(v)
	p.uniRem--
	if p.uniRem > 0 {
		return nil
	}
	p.state = psString
	u := p.uni
	switch {
	case p.needLow:
		if u < 0xDC00 || u > 0xDFFF {
			return errf(off, "invalid low surrogate \\u%04X", u)
		}
		p.buf = utf8.AppendRune(p.buf, utf16.DecodeRune(p.high, u))
		p.needLow = false
	case u >= 0xD800 && u <= 0xDBFF:
		p.high = u
		p.needLow = true
	case u >= 0xDC00 && u <= 0xDFFF:
		return errf(off, "unexpected low surrogate \\u%04X", u)
	default:
		p.buf = utf8.AppendRune(p.buf, u)
	}
	return nil
}

func (p *parser) feedNumber(b byte, off int64) error {
	ns := p.num
	switch {
	case ns == nsBegin && b == '-':
		p.num = nsMinus
	case (ns == nsBegin || ns == nsMinus) && b == '0':
		p.num = nsZero
	case (ns == nsBegin || ns == nsMinus) && b >= '1' && b <= '9':
		p.num = nsInt
	case ns == nsInt && isDigit(b):
	case (ns == nsZero || ns == nsInt) && b == '.':
		p.num = nsDot
	case ns == nsDot && isDigit(b):
		p.num = nsFrac
	case ns == nsFrac && isDigit(b):
	case (ns == nsZero || ns == nsInt || ns == nsFrac) && (b == 'e' || b == 'E'):
		p.num = nsExp
	case ns == nsExp && (b == '+' || b == '-'):
		p.num = nsExpSign
	case (ns == nsExp || ns == nsExpSign) && isDigit(b):
		p.num = nsExpD
	case ns == nsExpD && isDigit(b):
	default:
		return errf(off, "invalid character %q in number", b)
	}
	p.buf = append(p.buf, b)
	return nil
}

func (p *parser) finalizeNumber(off int64) error {
	switch p.num {
	case nsZero, nsInt, nsFrac, nsExpD:
	default:
		return errf(off, "truncated number %q", p.buf)
	}
	f, err := strconv.ParseFloat(string(p.buf), 64)
	if err != nil {
		return errf(off, "invalid number %q", p.buf)
	}
	p.emitValue(f, p.tokStart)
	p.valueComplete(off)
	return nil
}
