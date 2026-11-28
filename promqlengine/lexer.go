package promqlengine

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokIdent
	tokMetricIdent
	tokString
	tokNumber
	tokDuration
	tokOp
	tokLBrace
	tokRBrace
	tokLBracket
	tokRBracket
	tokLParen
	tokRParen
	tokComma
	tokColon
)

type token struct {
	kind tokenKind
	val  string
	pos  int
}

func (t token) String() string {
	if t.val == "" {
		return t.kind.String()
	}
	return fmt.Sprintf("%s(%q)", t.kind, t.val)
}

func (k tokenKind) String() string {
	switch k {
	case tokEOF:
		return "eof"
	case tokIdent:
		return "ident"
	case tokMetricIdent:
		return "metric"
	case tokString:
		return "string"
	case tokNumber:
		return "number"
	case tokDuration:
		return "duration"
	case tokOp:
		return "op"
	case tokLBrace:
		return "{"
	case tokRBrace:
		return "}"
	case tokLBracket:
		return "["
	case tokRBracket:
		return "]"
	case tokLParen:
		return "("
	case tokRParen:
		return ")"
	case tokComma:
		return ","
	case tokColon:
		return ":"
	default:
		return "?"
	}
}

// lexer turns PromQL text into tokens. It is deliberately small: only the
// subset needed for selectors, subqueries, rate/increase calls and the @ /
// offset modifiers is recognized.
type lexer struct {
	input string
	pos   int
}

func lex(input string) ([]token, error) {
	l := &lexer{input: input}
	var out []token
	for {
		l.skipSpace()
		if l.pos >= len(l.input) {
			out = append(out, token{kind: tokEOF, pos: l.pos})
			return out, nil
		}
		start := l.pos
		c := l.input[l.pos]
		switch {
		case c == '{':
			l.pos++
			out = append(out, token{kind: tokLBrace, pos: start})
		case c == '}':
			l.pos++
			out = append(out, token{kind: tokRBrace, pos: start})
		case c == '[':
			l.pos++
			out = append(out, token{kind: tokLBracket, pos: start})
		case c == ']':
			l.pos++
			out = append(out, token{kind: tokRBracket, pos: start})
		case c == '(':
			l.pos++
			out = append(out, token{kind: tokLParen, pos: start})
		case c == ')':
			l.pos++
			out = append(out, token{kind: tokRParen, pos: start})
		case c == ',':
			l.pos++
			out = append(out, token{kind: tokComma, pos: start})
		case c == ':':
			l.pos++
			out = append(out, token{kind: tokColon, pos: start})
		case c == '@':
			l.pos++
			out = append(out, token{kind: tokIdent, val: "@", pos: start})
		case c == '=' || c == '!':
			tok, err := l.scanOperator(start)
			if err != nil {
				return nil, err
			}
			out = append(out, tok)
		case c == '"' || c == '\'':
			s, err := l.scanString(c)
			if err != nil {
				return nil, err
			}
			out = append(out, token{kind: tokString, val: s, pos: start})
		case c == '`':
			s, err := l.scanRawString()
			if err != nil {
				return nil, err
			}
			out = append(out, token{kind: tokString, val: s, pos: start})
		case isDigit(c) || c == '.' || c == '+' || c == '-':
			tok, err := l.scanNumericOrSign(start)
			if err != nil {
				return nil, err
			}
			out = append(out, tok)
		case isIdentStart(c):
			word := l.scanIdent()
			out = append(out, token{kind: tokIdent, val: word, pos: start})
		default:
			return nil, fmt.Errorf("promqlengine: unexpected character %q at position %d", string(c), start)
		}
	}
}

func (l *lexer) skipSpace() {
	for l.pos < len(l.input) {
		r, size := utf8.DecodeRuneInString(l.input[l.pos:])
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			l.pos += size
			continue
		}
		return
	}
}

func (l *lexer) scanString(quote byte) (string, error) {
	l.pos++ // opening quote
	var sb strings.Builder
	for l.pos < len(l.input) {
		c := l.input[l.pos]
		if c == quote {
			l.pos++
			return sb.String(), nil
		}
		if c == '\\' {
			l.pos++
			if l.pos >= len(l.input) {
				break
			}
			esc := l.input[l.pos]
			l.pos++
			switch esc {
			case 'n':
				sb.WriteByte('\n')
			case '\\':
				sb.WriteByte('\\')
			case '"':
				sb.WriteByte('"')
			case '\'':
				sb.WriteByte('\'')
			default:
				sb.WriteByte(esc)
			}
			continue
		}
		sb.WriteByte(c)
		l.pos++
	}
	return "", fmt.Errorf("promqlengine: unterminated string at position %d", l.pos)
}

func (l *lexer) scanRawString() (string, error) {
	l.pos++ // opening backtick
	start := l.pos
	for l.pos < len(l.input) {
		if l.input[l.pos] == '`' {
			s := l.input[start:l.pos]
			l.pos++
			return s, nil
		}
		l.pos++
	}
	return "", fmt.Errorf("promqlengine: unterminated raw string at position %d", l.pos)
}

// scanOperator handles the matcher operators "=", "!=", "=~", "!~".
func (l *lexer) scanOperator(start int) (token, error) {
	c := l.input[l.pos]
	if c == '=' {
		if l.pos+1 < len(l.input) && l.input[l.pos+1] == '~' {
			l.pos += 2
			return token{kind: tokOp, val: "=~", pos: start}, nil
		}
		l.pos++
		return token{kind: tokOp, val: "=", pos: start}, nil
	}
	// c == '!'
	if l.pos+1 < len(l.input) {
		switch l.input[l.pos+1] {
		case '=':
			l.pos += 2
			return token{kind: tokOp, val: "!=", pos: start}, nil
		case '~':
			l.pos += 2
			return token{kind: tokOp, val: "!~", pos: start}, nil
		}
	}
	return token{}, fmt.Errorf("promqlengine: unexpected '!' at position %d", start)
}

// scanNumericOrSign handles numbers, durations and a leading sign. A leading
// '-' followed by a digit starts a signed number/duration (used by negative
// offsets); a standalone '+'/'-' is emitted as an operator token.
func (l *lexer) scanNumericOrSign(start int) (token, error) {
	c := l.input[l.pos]
	if c == '+' || c == '-' {
		if l.pos+1 >= len(l.input) || !isDigit(l.input[l.pos+1]) {
			l.pos++
			return token{kind: tokOp, val: string(c), pos: start}, nil
		}
	}
	// Scan a number, optionally followed immediately by duration units.
	numStart := l.pos
	l.scanNumber()
	numText := l.input[numStart:l.pos]
	if l.pos < len(l.input) && isUnitStart(l.input[l.pos]) {
		durStart := l.pos
		for l.pos < len(l.input) {
			if isUnitStart(l.input[l.pos]) {
				l.scanUnitToken()
				continue
			}
			break
		}
		_ = durStart
		return token{kind: tokDuration, val: l.input[numStart:l.pos], pos: start}, nil
	}
	return token{kind: tokNumber, val: numText, pos: start}, nil
}

func (l *lexer) scanNumber() {
	if l.pos < len(l.input) && (l.input[l.pos] == '+' || l.input[l.pos] == '-') {
		l.pos++
	}
	seenDigit := false
	for l.pos < len(l.input) && isDigit(l.input[l.pos]) {
		l.pos++
		seenDigit = true
	}
	if l.pos < len(l.input) && l.input[l.pos] == '.' {
		l.pos++
		for l.pos < len(l.input) && isDigit(l.input[l.pos]) {
			l.pos++
			seenDigit = true
		}
	}
	// Exponent.
	if seenDigit && l.pos < len(l.input) && (l.input[l.pos] == 'e' || l.input[l.pos] == 'E') {
		save := l.pos
		l.pos++
		if l.pos < len(l.input) && (l.input[l.pos] == '+' || l.input[l.pos] == '-') {
			l.pos++
		}
		if l.pos >= len(l.input) || !isDigit(l.input[l.pos]) {
			l.pos = save
		}
		for l.pos < len(l.input) && isDigit(l.input[l.pos]) {
			l.pos++
		}
	}
}

func (l *lexer) scanUnitToken() {
	if l.pos+1 < len(l.input) && l.input[l.pos] == 'm' && l.input[l.pos+1] == 's' {
		l.pos += 2
		return
	}
	l.pos++
}

func (l *lexer) scanIdent() string {
	start := l.pos
	for l.pos < len(l.input) {
		c := l.input[l.pos]
		if isIdentPart(c) {
			l.pos++
			continue
		}
		break
	}
	return l.input[start:l.pos]
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isIdentStart(c byte) bool {
	return c == '_' || c == ':' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || isDigit(c)
}

func isUnitStart(c byte) bool {
	switch c {
	case 'm', 's', 'h', 'd', 'w', 'y':
		return true
	}
	return false
}
