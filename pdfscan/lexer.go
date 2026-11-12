package pdfscan

import (
	"errors"
	"fmt"
)

const (
	tokEOF     = iota
	tokKeyword // true/false/null/R/obj/endobj/stream/endstream/endobj and bare words
	tokNumber
	tokName
	tokString // literal string, value stored unescaped
	tokHex    // hex string, value stored as decoded bytes
	tokLBrace // <<
	tokRBrace // >>
	tokLBracket
	tokRBracket
	tokLParen
	tokRParen
	tokLt // single < (hex string opener; tokenizer reads whole hex string instead)
)

type token struct {
	kind int
	num  int64
	real float64
	text string
	pos  int
}

type lexer struct {
	data []byte
	pos  int
}

func isWhite(b byte) bool {
	switch b {
	case 0, 9, 10, 12, 13, 32:
		return true
	}
	return false
}

func isDelim(b byte) bool {
	switch b {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func isRegular(b byte) bool {
	return !isWhite(b) && !isDelim(b)
}

func isHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func hexVal(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	default:
		return int(b-'A') + 10
	}
}

// skipSpaceAndComments advances past whitespace and comments.
func (l *lexer) skipSpaceAndComments() {
	for l.pos < len(l.data) {
		b := l.data[l.pos]
		if isWhite(b) {
			l.pos++
			continue
		}
		if b == '%' {
			for l.pos < len(l.data) && l.data[l.pos] != '\n' && l.data[l.pos] != '\r' {
				l.pos++
			}
			continue
		}
		return
	}
}

func (l *lexer) next() (token, error) {
	l.skipSpaceAndComments()
	if l.pos >= len(l.data) {
		return token{kind: tokEOF, pos: l.pos}, nil
	}
	start := l.pos
	b := l.data[l.pos]

	switch b {
	case '[':
		l.pos++
		return token{kind: tokLBracket, pos: start}, nil
	case ']':
		l.pos++
		return token{kind: tokRBracket, pos: start}, nil
	case '(':
		s, err := l.readLiteralString()
		if err != nil {
			return token{}, err
		}
		return token{kind: tokString, text: string(s), pos: start}, nil
	case '<':
		if l.pos+1 < len(l.data) && l.data[l.pos+1] == '<' {
			l.pos += 2
			return token{kind: tokLBrace, pos: start}, nil
		}
		s, err := l.readHexString()
		if err != nil {
			return token{}, err
		}
		return token{kind: tokHex, text: string(s), pos: start}, nil
	case '>':
		if l.pos+1 < len(l.data) && l.data[l.pos+1] == '>' {
			l.pos += 2
			return token{kind: tokRBrace, pos: start}, nil
		}
		return token{}, fmt.Errorf("pdfscan: unexpected '>' at offset %d", start)
	case '/':
		name, err := l.readName()
		if err != nil {
			return token{}, err
		}
		return token{kind: tokName, text: name, pos: start}, nil
	}

	if b == '+' || b == '-' || b == '.' || (b >= '0' && b <= '9') {
		return l.readNumber(start), nil
	}

	if isRegular(b) {
		return l.readKeyword(start), nil
	}
	return token{}, fmt.Errorf("pdfscan: unexpected byte %q at offset %d", b, start)
}

func (l *lexer) readNumber(start int) token {
	hasDot := false
	for l.pos < len(l.data) {
		c := l.data[l.pos]
		if c >= '0' && c <= '9' {
			l.pos++
			continue
		}
		if c == '.' && !hasDot {
			hasDot = true
			l.pos++
			continue
		}
		break
	}
	text := string(l.data[start:l.pos])
	t := token{kind: tokNumber, pos: start}
	if hasDot {
		fmt.Sscanf(text, "%g", &t.real)
	} else {
		fmt.Sscanf(text, "%d", &t.num)
	}
	return t
}

func (l *lexer) readKeyword(start int) token {
	for l.pos < len(l.data) && isRegular(l.data[l.pos]) {
		l.pos++
	}
	return token{kind: tokKeyword, text: string(l.data[start:l.pos]), pos: start}
}

// readName consumes a name token and resolves #xx hex escapes. PDF name
// escapes are case-insensitive on the hex digits, so "/Open#4action" and
// "/Open#4Action" both decode to "/OpenAction".
func (l *lexer) readName() (string, error) {
	l.pos++ // consume '/'
	var out []byte
	for l.pos < len(l.data) {
		c := l.data[l.pos]
		if isWhite(c) || isDelim(c) {
			break
		}
		if c == '#' && l.pos+2 <= len(l.data) &&
			isHexDigit(l.data[l.pos+1]) && isHexDigit(l.data[l.pos+2]) {
			out = append(out, byte(hexVal(l.data[l.pos+1])<<4|hexVal(l.data[l.pos+2])))
			l.pos += 3
			continue
		}
		out = append(out, c)
		l.pos++
	}
	return string(out), nil
}

func (l *lexer) readHexString() ([]byte, error) {
	l.pos++ // consume '<'
	var nibbles []int
	for l.pos < len(l.data) && l.data[l.pos] != '>' {
		c := l.data[l.pos]
		if isWhite(c) {
			l.pos++
			continue
		}
		if !isHexDigit(c) {
			return nil, fmt.Errorf("pdfscan: invalid hex string byte %q at offset %d", c, l.pos)
		}
		nibbles = append(nibbles, hexVal(c))
		l.pos++
	}
	if l.pos >= len(l.data) {
		return nil, errors.New("pdfscan: unterminated hex string")
	}
	l.pos++ // consume '>'
	if len(nibbles)%2 == 1 {
		nibbles = append(nibbles, 0)
	}
	out := make([]byte, 0, len(nibbles)/2)
	for i := 0; i < len(nibbles); i += 2 {
		out = append(out, byte(nibbles[i]<<4|nibbles[i+1]))
	}
	return out, nil
}

func (l *lexer) readLiteralString() ([]byte, error) {
	l.pos++ // consume '('
	var out []byte
	depth := 1
	for l.pos < len(l.data) {
		c := l.data[l.pos]
		switch c {
		case '(':
			depth++
			out = append(out, c)
			l.pos++
		case ')':
			depth--
			l.pos++
			if depth == 0 {
				return out, nil
			}
			out = append(out, c)
		case '\\':
			l.pos++
			if l.pos >= len(l.data) {
				return nil, errors.New("pdfscan: unterminated literal string escape")
			}
			e := l.data[l.pos]
			switch e {
			case 'n':
				out = append(out, '\n')
				l.pos++
			case 'r':
				out = append(out, '\r')
				l.pos++
			case 't':
				out = append(out, '\t')
				l.pos++
			case 'b':
				out = append(out, '\b')
				l.pos++
			case 'f':
				out = append(out, '\f')
				l.pos++
			case '(':
				out = append(out, '(')
				l.pos++
			case ')':
				out = append(out, ')')
				l.pos++
			case '\\':
				out = append(out, '\\')
				l.pos++
			case '\r':
				l.pos++
				if l.pos < len(l.data) && l.data[l.pos] == '\n' {
					l.pos++
				}
			case '\n':
				l.pos++
			default:
				if e >= '0' && e <= '7' {
					val := int(e - '0')
					l.pos++
					for i := 0; i < 2 && l.pos < len(l.data) && l.data[l.pos] >= '0' && l.data[l.pos] <= '7'; i++ {
						val = val*8 + int(l.data[l.pos]-'0')
						l.pos++
					}
					out = append(out, byte(val&0xff))
				} else {
					out = append(out, e)
					l.pos++
				}
			}
		case '\r':
			l.pos++
			if l.pos < len(l.data) && l.data[l.pos] == '\n' {
				out = append(out, '\n')
			} else {
				out = append(out, '\n')
			}
		default:
			out = append(out, c)
			l.pos++
		}
	}
	return nil, errors.New("pdfscan: unterminated literal string")
}
