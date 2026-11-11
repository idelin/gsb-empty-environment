package pdfactions

import (
	"bytes"
	"fmt"
	"strconv"
)

// parser is a recursive-descent reader for the PDF object syntax.
type parser struct {
	data []byte
	pos  int
}

func isWhitespace(b byte) bool {
	switch b {
	case 0, 9, 10, 12, 13, 32:
		return true
	}
	return false
}

func isDelimiter(b byte) bool {
	switch b {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func isRegular(b byte) bool {
	return !isWhitespace(b) && !isDelimiter(b)
}

// skipSpace advances over whitespace and comments.
func (p *parser) skipSpace() {
	for p.pos < len(p.data) {
		b := p.data[p.pos]
		if isWhitespace(b) {
			p.pos++
			continue
		}
		if b == '%' {
			for p.pos < len(p.data) && p.data[p.pos] != '\n' && p.data[p.pos] != '\r' {
				p.pos++
			}
			continue
		}
		return
	}
}

// parseValue reads a single PDF value. Indirect references are recognized
// only by parseRef via lookahead; the generic path returns bare numbers.
func (p *parser) parseValue() (Value, error) {
	p.skipSpace()
	if p.pos >= len(p.data) {
		return nil, fmt.Errorf("pdfactions: unexpected end of data")
	}
	switch b := p.data[p.pos]; {
	case b == '(':
		return p.parseLiteralString()
	case b == '<' && p.pos+1 < len(p.data) && p.data[p.pos+1] == '<':
		return p.parseDict()
	case b == '<':
		return p.parseHexString()
	case b == '[':
		return p.parseArray()
	case b == '/':
		return p.parseName(), nil
	case b >= '0' && b <= '9', b == '+' || b == '-', b == '.':
		if ref, ok := p.parseRef(); ok {
			return ref, nil
		}
		return p.parseNumberOrKeyword()
	default:
		return p.parseKeyword()
	}
}

func (p *parser) parseLiteralString() (String, error) {
	p.pos++ // consume (
	var out bytes.Buffer
	depth := 1
	for p.pos < len(p.data) {
		b := p.data[p.pos]
		switch {
		case b == '\\':
			if p.pos+1 >= len(p.data) {
				return "", fmt.Errorf("pdfactions: unterminated string escape")
			}
			esc := p.data[p.pos+1]
			switch esc {
			case 'n':
				out.WriteByte('\n')
				p.pos += 2
			case 'r':
				out.WriteByte('\r')
				p.pos += 2
			case 't':
				out.WriteByte('\t')
				p.pos += 2
			case 'b':
				out.WriteByte('\b')
				p.pos += 2
			case 'f':
				out.WriteByte('\f')
				p.pos += 2
			case '(':
				out.WriteByte('(')
				p.pos += 2
			case ')':
				out.WriteByte(')')
				p.pos += 2
			case '\\':
				out.WriteByte('\\')
				p.pos += 2
			case '\r':
				p.pos += 2
				if p.pos < len(p.data) && p.data[p.pos] == '\n' {
					p.pos++
				}
			case '\n':
				p.pos += 2
			case '0', '1', '2', '3', '4', '5', '6', '7':
				octal := []byte{esc}
				p.pos += 2
				for i := 0; i < 2 && p.pos < len(p.data) && p.data[p.pos] >= '0' && p.data[p.pos] <= '7'; i++ {
					octal = append(octal, p.data[p.pos])
					p.pos++
				}
				value, _ := strconv.ParseUint(string(octal), 8, 16)
				out.WriteByte(byte(value))
			default:
				out.WriteByte(esc)
				p.pos += 2
			}
		case b == '(':
			depth++
			out.WriteByte(b)
			p.pos++
		case b == ')':
			depth--
			p.pos++
			if depth == 0 {
				return String(out.String()), nil
			}
			out.WriteByte(b)
		default:
			out.WriteByte(b)
			p.pos++
		}
	}
	return "", fmt.Errorf("pdfactions: unterminated literal string")
}

func (p *parser) parseHexString() (String, error) {
	p.pos++ // consume <
	var digits []byte
	for p.pos < len(p.data) && p.data[p.pos] != '>' {
		b := p.data[p.pos]
		if !isWhitespace(b) {
			digits = append(digits, b)
		}
		p.pos++
	}
	if p.pos >= len(p.data) {
		return "", fmt.Errorf("pdfactions: unterminated hex string")
	}
	p.pos++ // consume >
	if len(digits)%2 == 1 {
		digits = append(digits, '0')
	}
	out := make([]byte, 0, len(digits)/2)
	for i := 0; i+1 < len(digits); i += 2 {
		hi, errHi := strconv.ParseUint(string(digits[i:i+1]), 16, 8)
		lo, errLo := strconv.ParseUint(string(digits[i+1:i+2]), 16, 8)
		if errHi != nil || errLo != nil {
			return "", fmt.Errorf("pdfactions: invalid hex string")
		}
		out = append(out, byte(hi<<4|lo))
	}
	return String(out), nil
}

func (p *parser) parseName() Name {
	p.pos++ // consume /
	start := p.pos
	var out bytes.Buffer
	for p.pos < len(p.data) && isRegular(p.data[p.pos]) {
		b := p.data[p.pos]
		if b == '#' && p.pos+2 < len(p.data) {
			if hi, errHi := strconv.ParseUint(string(p.data[p.pos+1:p.pos+2]), 16, 8); errHi == nil {
				if lo, errLo := strconv.ParseUint(string(p.data[p.pos+2:p.pos+3]), 16, 8); errLo == nil {
					out.Write(p.data[start:p.pos])
					out.WriteByte(byte(hi<<4 | lo))
					p.pos += 3
					start = p.pos
					continue
				}
			}
		}
		p.pos++
	}
	out.Write(p.data[start:p.pos])
	return Name(out.String())
}

func (p *parser) parseArray() (Array, error) {
	p.pos++ // consume [
	array := Array{}
	for {
		p.skipSpace()
		if p.pos >= len(p.data) {
			return nil, fmt.Errorf("pdfactions: unterminated array")
		}
		if p.data[p.pos] == ']' {
			p.pos++
			return array, nil
		}
		value, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		array = append(array, value)
	}
}

func (p *parser) parseDict() (Value, error) {
	p.pos += 2 // consume <<
	dict := NewDict()
	for {
		p.skipSpace()
		if p.pos+1 >= len(p.data) {
			return nil, fmt.Errorf("pdfactions: unterminated dictionary")
		}
		if p.data[p.pos] == '>' && p.data[p.pos+1] == '>' {
			p.pos += 2
			p.skipSpace()
			if p.pos+6 <= len(p.data) && string(p.data[p.pos:p.pos+6]) == "stream" &&
				(p.pos+6 == len(p.data) || !isRegular(p.data[p.pos+6])) {
				return p.parseStreamBody(dict)
			}
			return dict, nil
		}
		if p.data[p.pos] != '/' {
			return nil, fmt.Errorf("pdfactions: malformed dictionary at offset %d", p.pos)
		}
		key := p.parseName()
		value, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		dict.Set(key, value)
	}
}

func (p *parser) parseStreamBody(dict *Dict) (*Stream, error) {
	p.pos += 6 // consume stream
	if p.pos < len(p.data) && p.data[p.pos] == '\r' {
		p.pos++
	}
	if p.pos < len(p.data) && p.data[p.pos] == '\n' {
		p.pos++
	}
	end := bytes.Index(p.data[p.pos:], []byte("endstream"))
	if end < 0 {
		return nil, fmt.Errorf("pdfactions: stream without endstream")
	}
	raw := p.data[p.pos : p.pos+end]
	p.pos += end + len("endstream")
	return &Stream{Dict: dict, Raw: raw}, nil
}

func (p *parser) parseNumberOrKeyword() (Value, error) {
	start := p.pos
	if p.data[p.pos] == '+' || p.data[p.pos] == '-' {
		p.pos++
	}
	hasDot := false
	for p.pos < len(p.data) {
		b := p.data[p.pos]
		if b >= '0' && b <= '9' {
			p.pos++
			continue
		}
		if b == '.' && !hasDot {
			hasDot = true
			p.pos++
			continue
		}
		break
	}
	text := string(p.data[start:p.pos])
	if hasDot {
		real, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, fmt.Errorf("pdfactions: invalid number %q", text)
		}
		return Real(real), nil
	}
	integer, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("pdfactions: invalid number %q", text)
	}
	return Integer(integer), nil
}

func (p *parser) parseKeyword() (Value, error) {
	start := p.pos
	for p.pos < len(p.data) && isRegular(p.data[p.pos]) {
		p.pos++
	}
	switch text := string(p.data[start:p.pos]); text {
	case "true":
		return Boolean(true), nil
	case "false":
		return Boolean(false), nil
	case "null":
		return Null{}, nil
	default:
		return nil, fmt.Errorf("pdfactions: unexpected token %q", text)
	}
}

// parseRef recognizes an indirect reference N G R. The position is restored on
// failure so callers can interpret the leading numbers differently.
func (p *parser) parseRef() (Ref, bool) {
	mark := p.pos
	p.skipSpace()
	number, ok := p.readInteger()
	if !ok || number < 0 {
		p.pos = mark
		return Ref{}, false
	}
	p.skipSpace()
	generation, ok := p.readInteger()
	if !ok || generation < 0 {
		p.pos = mark
		return Ref{}, false
	}
	p.skipSpace()
	if p.pos >= len(p.data) || !bytes.HasPrefix(p.data[p.pos:], []byte("R")) ||
		(p.pos+1 < len(p.data) && isRegular(p.data[p.pos+1])) {
		p.pos = mark
		return Ref{}, false
	}
	p.pos++
	return Ref{Number: int(number), Generation: int(generation)}, true
}

// readInteger consumes a bare signed integer without going through
// parseValue, which keeps reference lookahead free of recursion.
func (p *parser) readInteger() (Integer, bool) {
	start := p.pos
	if p.pos < len(p.data) && (p.data[p.pos] == '+' || p.data[p.pos] == '-') {
		p.pos++
	}
	digits := p.pos
	for p.pos < len(p.data) && p.data[p.pos] >= '0' && p.data[p.pos] <= '9' {
		p.pos++
	}
	if p.pos == digits || (p.pos-start == 1 && (p.data[start] == '+' || p.data[start] == '-')) {
		p.pos = start
		return 0, false
	}
	value, err := strconv.ParseInt(string(p.data[start:p.pos]), 10, 64)
	if err != nil {
		p.pos = start
		return 0, false
	}
	return Integer(value), true
}

// parseIndirectHeader reads "N G obj" and returns the id; false means the
// cursor does not point at an indirect object header.
func (p *parser) parseIndirectHeader() (ObjectID, bool) {
	mark := p.pos
	p.skipSpace()
	num, err := p.parseValue()
	if err != nil {
		p.pos = mark
		return ObjectID{}, false
	}
	number, ok := num.(Integer)
	if !ok {
		p.pos = mark
		return ObjectID{}, false
	}
	p.skipSpace()
	gen, err := p.parseValue()
	if err != nil {
		p.pos = mark
		return ObjectID{}, false
	}
	generation, ok := gen.(Integer)
	if !ok {
		p.pos = mark
		return ObjectID{}, false
	}
	p.skipSpace()
	if p.pos+3 > len(p.data) || string(p.data[p.pos:p.pos+3]) != "obj" {
		p.pos = mark
		return ObjectID{}, false
	}
	if p.pos+3 < len(p.data) && isRegular(p.data[p.pos+3]) {
		p.pos = mark
		return ObjectID{}, false
	}
	p.pos += 3
	return ObjectID{Number: int(number), Generation: int(generation)}, true
}
