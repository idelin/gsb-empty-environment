package netsec

import (
	"encoding/hex"
	"fmt"
	"unicode/utf16"
	"unicode/utf8"
)

// handParser is a self-contained, byte-level recursive-descent JSON parser.
// It deliberately does not share any parsing code with the encoding/json path;
// only the later semantic validation is shared. It records every top-level key
// occurrence so duplicate keys cannot be silently overwritten.
type handParser struct {
	data []byte
	pos  int
}

// parseHand parses raw and returns the top-level object's fields in source
// order. Any grammar error rejects the whole message.
func parseHand(raw []byte) ([]rawField, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("input is not valid UTF-8")
	}
	p := &handParser{data: raw}
	p.skipSpace()
	val, err := p.parseValue()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.pos != len(p.data) {
		return nil, fmt.Errorf("unexpected trailing data at offset %d", p.pos)
	}
	if val.kind != kindObject {
		return nil, fmt.Errorf("top-level value must be a JSON object")
	}
	object, ok := val.text.([]rawField)
	if !ok {
		return nil, fmt.Errorf("internal: corrupted object value")
	}
	return object, nil
}

func (p *handParser) parseValue() (rawValue, error) {
	if p.pos >= len(p.data) {
		return rawValue{}, fmt.Errorf("unexpected end of input")
	}
	switch c := p.data[p.pos]; {
	case c == '{':
		return p.parseObject()
	case c == '[':
		return p.parseArray()
	case c == '"':
		s, err := p.readString()
		if err != nil {
			return rawValue{}, err
		}
		return rawValue{kind: kindString, text: s}, nil
	case c == 't':
		if err := p.readLiteral("true"); err != nil {
			return rawValue{}, err
		}
		return rawValue{kind: kindBool, text: "true"}, nil
	case c == 'f':
		if err := p.readLiteral("false"); err != nil {
			return rawValue{}, err
		}
		return rawValue{kind: kindBool, text: "false"}, nil
	case c == 'n':
		if err := p.readLiteral("null"); err != nil {
			return rawValue{}, err
		}
		return rawValue{kind: kindNull}, nil
	case c == '-' || (c >= '0' && c <= '9'):
		n, err := p.readNumber()
		if err != nil {
			return rawValue{}, err
		}
		return rawValue{kind: kindInteger, text: n}, nil
	default:
		return rawValue{}, fmt.Errorf("unexpected byte %q at offset %d", c, p.pos)
	}
}

func (p *handParser) parseObject() (rawValue, error) {
	p.pos++ // consume '{'
	var fields []rawField
	p.skipSpace()
	if p.pos < len(p.data) && p.data[p.pos] == '}' {
		p.pos++
		return rawValue{kind: kindObject, text: fields}, nil
	}
	for {
		p.skipSpace()
		if p.pos >= len(p.data) || p.data[p.pos] != '"' {
			return rawValue{}, fmt.Errorf("expected object key string at offset %d", p.pos)
		}
		key, err := p.readString()
		if err != nil {
			return rawValue{}, err
		}
		p.skipSpace()
		if p.pos >= len(p.data) || p.data[p.pos] != ':' {
			return rawValue{}, fmt.Errorf("expected ':' after key at offset %d", p.pos)
		}
		p.pos++
		p.skipSpace()
		val, err := p.parseValue()
		if err != nil {
			return rawValue{}, err
		}
		fields = append(fields, rawField{name: key, val: val})
		p.skipSpace()
		if p.pos >= len(p.data) {
			return rawValue{}, fmt.Errorf("unterminated object")
		}
		switch p.data[p.pos] {
		case ',':
			p.pos++
		case '}':
			p.pos++
			return rawValue{kind: kindObject, text: fields}, nil
		default:
			return rawValue{}, fmt.Errorf("expected ',' or '}' at offset %d", p.pos)
		}
	}
}

func (p *handParser) parseArray() (rawValue, error) {
	p.pos++ // consume '['
	count := 0
	p.skipSpace()
	if p.pos < len(p.data) && p.data[p.pos] == ']' {
		p.pos++
		return rawValue{kind: kindArray, text: count}, nil
	}
	for {
		p.skipSpace()
		if _, err := p.parseValue(); err != nil {
			return rawValue{}, err
		}
		count++
		p.skipSpace()
		if p.pos >= len(p.data) {
			return rawValue{}, fmt.Errorf("unterminated array")
		}
		switch p.data[p.pos] {
		case ',':
			p.pos++
		case ']':
			p.pos++
			return rawValue{kind: kindArray, text: count}, nil
		default:
			return rawValue{}, fmt.Errorf("expected ',' or ']' at offset %d", p.pos)
		}
	}
}

// readString consumes a JSON string and returns its decoded content so escapes
// like \u0041 and their literal forms map to identical semantics on both
// parser paths.
func (p *handParser) readString() (string, error) {
	p.pos++ // consume opening quote
	var out []byte
	for {
		if p.pos >= len(p.data) {
			return "", fmt.Errorf("unterminated string")
		}
		c := p.data[p.pos]
		if c == '"' {
			p.pos++
			if !utf8.Valid(out) {
				return "", fmt.Errorf("string is not valid UTF-8")
			}
			return string(out), nil
		}
		if c < 0x20 {
			return "", fmt.Errorf("unescaped control byte 0x%02x in string", c)
		}
		if c != '\\' {
			out = append(out, c)
			p.pos++
			continue
		}
		p.pos++
		if p.pos >= len(p.data) {
			return "", fmt.Errorf("unterminated escape sequence")
		}
		esc := p.data[p.pos]
		p.pos++
		switch esc {
		case '"':
			out = append(out, '"')
		case '\\':
			out = append(out, '\\')
		case '/':
			out = append(out, '/')
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'u':
			r, err := p.readUnicodeEscape()
			if err != nil {
				return "", err
			}
			var buf [4]byte
			n := utf8.EncodeRune(buf[:], r)
			out = append(out, buf[:n]...)
		default:
			return "", fmt.Errorf("invalid escape '\\%c'", esc)
		}
	}
}

// readUnicodeEscape reads \uXXXX, decoding surrogate pairs exactly like
// encoding/json; lone surrogates are rejected.
func (p *handParser) readUnicodeEscape() (rune, error) {
	r, err := p.readHex4()
	if err != nil {
		return 0, err
	}
	if utf16.IsSurrogate(r) {
		if p.pos+6 > len(p.data) || p.data[p.pos] != '\\' || p.data[p.pos+1] != 'u' {
			return 0, fmt.Errorf("lone UTF-16 surrogate U+%04X", r)
		}
		p.pos += 2
		low, err := p.readHex4()
		if err != nil {
			return 0, err
		}
		combined := utf16.DecodeRune(r, low)
		if combined == utf8.RuneError {
			return 0, fmt.Errorf("invalid surrogate pair U+%04X U+%04X", r, low)
		}
		return combined, nil
	}
	return r, nil
}

func (p *handParser) readHex4() (rune, error) {
	if p.pos+4 > len(p.data) {
		return 0, fmt.Errorf("truncated \\u escape")
	}
	decoded, err := hex.DecodeString(string(p.data[p.pos : p.pos+4]))
	if err != nil {
		return 0, fmt.Errorf("invalid \\u hex digits")
	}
	p.pos += 4
	return rune(decoded[0])<<8 | rune(decoded[1]), nil
}

// readNumber captures the full JSON number token without normalizing it.
// Canonical shape and range are enforced later in shared validation, so both
// parser paths judge numbers by the same rules.
func (p *handParser) readNumber() (string, error) {
	start := p.pos
	if p.data[p.pos] == '-' {
		p.pos++
	}
	if p.pos >= len(p.data) {
		return "", fmt.Errorf("truncated number")
	}
	switch p.data[p.pos] {
	case '0':
		p.pos++
	case '1', '2', '3', '4', '5', '6', '7', '8', '9':
		p.pos++
		for p.pos < len(p.data) && p.data[p.pos] >= '0' && p.data[p.pos] <= '9' {
			p.pos++
		}
	default:
		return "", fmt.Errorf("invalid number at offset %d", start)
	}
	if p.pos < len(p.data) && p.data[p.pos] == '.' {
		p.pos++
		if p.pos >= len(p.data) || p.data[p.pos] < '0' || p.data[p.pos] > '9' {
			return "", fmt.Errorf("invalid fraction at offset %d", start)
		}
		for p.pos < len(p.data) && p.data[p.pos] >= '0' && p.data[p.pos] <= '9' {
			p.pos++
		}
	}
	if p.pos < len(p.data) && (p.data[p.pos] == 'e' || p.data[p.pos] == 'E') {
		p.pos++
		if p.pos < len(p.data) && (p.data[p.pos] == '+' || p.data[p.pos] == '-') {
			p.pos++
		}
		if p.pos >= len(p.data) || p.data[p.pos] < '0' || p.data[p.pos] > '9' {
			return "", fmt.Errorf("invalid exponent at offset %d", start)
		}
		for p.pos < len(p.data) && p.data[p.pos] >= '0' && p.data[p.pos] <= '9' {
			p.pos++
		}
	}
	return string(p.data[start:p.pos]), nil
}

func (p *handParser) readLiteral(want string) error {
	if p.pos+len(want) > len(p.data) || string(p.data[p.pos:p.pos+len(want)]) != want {
		return fmt.Errorf("invalid literal at offset %d", p.pos)
	}
	p.pos += len(want)
	return nil
}

func (p *handParser) skipSpace() {
	for p.pos < len(p.data) {
		switch p.data[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}
