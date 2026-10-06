package mask

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// rewriter walks a JSON document token by token, copying every byte
// verbatim except the literal tokens of selected values, which are
// replaced by their masked literals.
type rewriter struct {
	src []byte
	pos int
	out bytes.Buffer
	m   *Masker
	tx  *tx
}

func (m *Masker) rewrite(src []byte, t *tx) ([]byte, error) {
	r := &rewriter{src: src, m: m, tx: t}
	if err := r.value(false); err != nil {
		return nil, err
	}
	r.ws()
	if r.pos != len(r.src) {
		return nil, fmt.Errorf("mask: unexpected trailing data at byte %d", r.pos)
	}
	return r.out.Bytes(), nil
}

func (r *rewriter) ws() {
	for r.pos < len(r.src) {
		switch r.src[r.pos] {
		case ' ', '\t', '\n', '\r':
			r.out.WriteByte(r.src[r.pos])
			r.pos++
		default:
			return
		}
	}
}

func (r *rewriter) peek() (byte, bool) {
	if r.pos >= len(r.src) {
		return 0, false
	}
	return r.src[r.pos], true
}

func (r *rewriter) take() {
	r.out.WriteByte(r.src[r.pos])
	r.pos++
}

func (r *rewriter) value(masked bool) error {
	r.ws()
	c, ok := r.peek()
	if !ok {
		return errors.New("mask: unexpected end of input")
	}
	switch {
	case c == '{':
		return r.object(masked)
	case c == '[':
		return r.array(masked)
	case c == '"':
		raw, decoded, err := r.string()
		if err != nil {
			return err
		}
		if !masked {
			r.out.Write(raw)
			return nil
		}
		lit, err := r.m.maskedLiteral(kindString, decoded, r.tx)
		if err != nil {
			return err
		}
		r.out.WriteString(lit)
		return nil
	case c == 't' || c == 'f':
		raw, err := r.literal()
		if err != nil {
			return err
		}
		if !masked {
			r.out.Write(raw)
			return nil
		}
		lit, err := r.m.maskedLiteral(kindBool, string(raw), r.tx)
		if err != nil {
			return err
		}
		r.out.WriteString(lit)
		return nil
	case c == 'n':
		raw, err := r.literal()
		if err != nil {
			return err
		}
		if string(raw) != "null" {
			return fmt.Errorf("mask: invalid literal %q at byte %d", raw, r.pos)
		}
		r.out.Write(raw) // null is preserved, never mapped
		return nil
	case c == '-' || (c >= '0' && c <= '9'):
		raw, err := r.number()
		if err != nil {
			return err
		}
		if !masked {
			r.out.Write(raw)
			return nil
		}
		lit, err := r.m.maskedLiteral(kindNumber, string(raw), r.tx)
		if err != nil {
			return err
		}
		r.out.WriteString(lit)
		return nil
	}
	return fmt.Errorf("mask: unexpected character %q at byte %d", c, r.pos)
}

func (r *rewriter) object(masked bool) error {
	r.take() // '{'
	r.ws()
	if c, _ := r.peek(); c == '}' {
		r.take()
		return nil
	}
	for {
		r.ws()
		if c, _ := r.peek(); c != '"' {
			return fmt.Errorf("mask: expected object key at byte %d", r.pos)
		}
		raw, key, err := r.string()
		if err != nil {
			return err
		}
		r.out.Write(raw) // keys are never masked
		r.ws()
		if c, ok := r.peek(); !ok || c != ':' {
			return fmt.Errorf("mask: expected ':' at byte %d", r.pos)
		}
		r.take()
		if err := r.value(masked || r.m.fields[key]); err != nil {
			return err
		}
		r.ws()
		c, ok := r.peek()
		if !ok {
			return errors.New("mask: unexpected end of input in object")
		}
		if c == ',' {
			r.take()
			continue
		}
		if c == '}' {
			r.take()
			return nil
		}
		return fmt.Errorf("mask: expected ',' or '}' at byte %d", r.pos)
	}
}

func (r *rewriter) array(masked bool) error {
	r.take() // '['
	r.ws()
	if c, _ := r.peek(); c == ']' {
		r.take()
		return nil
	}
	for {
		if err := r.value(masked); err != nil {
			return err
		}
		r.ws()
		c, ok := r.peek()
		if !ok {
			return errors.New("mask: unexpected end of input in array")
		}
		if c == ',' {
			r.take()
			continue
		}
		if c == ']' {
			r.take()
			return nil
		}
		return fmt.Errorf("mask: expected ',' or ']' at byte %d", r.pos)
	}
}

// string scans a JSON string starting at the opening quote, returning
// its raw source span and decoded value.
func (r *rewriter) string() (raw []byte, decoded string, err error) {
	start := r.pos
	r.pos++ // opening quote
	for r.pos < len(r.src) {
		c := r.src[r.pos]
		if c == '"' {
			r.pos++
			raw = r.src[start:r.pos]
			if err := json.Unmarshal(raw, &decoded); err != nil {
				return nil, "", fmt.Errorf("mask: invalid string at byte %d: %v", start, err)
			}
			return raw, decoded, nil
		}
		if c == '\\' {
			r.pos += 2
			continue
		}
		r.pos++
	}
	return nil, "", fmt.Errorf("mask: unterminated string at byte %d", start)
}

// number scans a JSON number and returns its raw source span.
func (r *rewriter) number() ([]byte, error) {
	start := r.pos
	isDigit := func(c byte) bool { return c >= '0' && c <= '9' }
	if r.src[r.pos] == '-' {
		r.pos++
	}
	if r.pos >= len(r.src) || !isDigit(r.src[r.pos]) {
		return nil, fmt.Errorf("mask: invalid number at byte %d", start)
	}
	if r.src[r.pos] == '0' {
		r.pos++
	} else {
		for r.pos < len(r.src) && isDigit(r.src[r.pos]) {
			r.pos++
		}
	}
	if r.pos < len(r.src) && r.src[r.pos] == '.' {
		r.pos++
		if r.pos >= len(r.src) || !isDigit(r.src[r.pos]) {
			return nil, fmt.Errorf("mask: invalid number at byte %d", start)
		}
		for r.pos < len(r.src) && isDigit(r.src[r.pos]) {
			r.pos++
		}
	}
	if r.pos < len(r.src) && (r.src[r.pos] == 'e' || r.src[r.pos] == 'E') {
		r.pos++
		if r.pos < len(r.src) && (r.src[r.pos] == '+' || r.src[r.pos] == '-') {
			r.pos++
		}
		if r.pos >= len(r.src) || !isDigit(r.src[r.pos]) {
			return nil, fmt.Errorf("mask: invalid number at byte %d", start)
		}
		for r.pos < len(r.src) && isDigit(r.src[r.pos]) {
			r.pos++
		}
	}
	return r.src[start:r.pos], nil
}

// literal scans a run of lowercase letters and validates it as a JSON
// literal (true, false or null).
func (r *rewriter) literal() ([]byte, error) {
	start := r.pos
	for r.pos < len(r.src) {
		c := r.src[r.pos]
		if c < 'a' || c > 'z' {
			break
		}
		r.pos++
	}
	raw := r.src[start:r.pos]
	switch string(raw) {
	case "true", "false", "null":
		return raw, nil
	}
	return nil, fmt.Errorf("mask: invalid literal %q at byte %d", raw, start)
}
