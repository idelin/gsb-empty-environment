package netsec

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
)

type parser struct {
	src    []byte
	pos    int
	schema *schema
}

func newParser(src []byte) *parser {
	return &parser{src: src, schema: newSchema()}
}

func (p *parser) parse() error {
	for {
		p.skipSpace()
		if p.eof() {
			return nil
		}

		switch p.src[p.pos] {
		case '\n', '\r':
			if err := p.skipLineBreak(); err != nil {
				return err
			}
		case '#':
			p.skipComment()
		case '[':
			if err := p.parseHeader(); err != nil {
				return err
			}
		default:
			if err := p.parseAssignment(); err != nil {
				return err
			}
		}
	}
}

func (p *parser) eof() bool {
	return p.pos >= len(p.src)
}

func (p *parser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.src[p.pos]
}

func (p *parser) peekAt(offset int) byte {
	at := p.pos + offset
	if at >= len(p.src) {
		return 0
	}
	return p.src[at]
}

func (p *parser) skipSpace() {
	for !p.eof() {
		switch p.src[p.pos] {
		case ' ', '\t':
			p.pos++
		default:
			return
		}
	}
}

func (p *parser) skipComment() {
	for !p.eof() && p.src[p.pos] != '\n' && p.src[p.pos] != '\r' {
		p.pos++
	}
}

func (p *parser) skipLineBreak() error {
	if p.peek() == '\n' {
		p.pos++
		return nil
	}
	if p.peek() == '\r' && p.peekAt(1) == '\n' {
		p.pos += 2
		return nil
	}
	return p.syntaxError("expected LF or CRLF")
}

func (p *parser) finishLine() error {
	p.skipSpace()
	if p.peek() == '#' {
		p.skipComment()
	}
	if p.eof() {
		return nil
	}
	if p.peek() == '\n' || p.peek() == '\r' {
		return p.skipLineBreak()
	}
	return p.syntaxError("unexpected content after statement")
}

func (p *parser) lineNumber() int {
	return 1 + bytes.Count(p.src[:p.pos], []byte{'\n'})
}

func (p *parser) syntaxError(format string, args ...any) error {
	return fmt.Errorf("%w at line %d: %s", ErrInvalidFragment, p.lineNumber(), fmt.Sprintf(format, args...))
}

func (p *parser) admissionError(base error, format string, args ...any) error {
	return fmt.Errorf("%w at line %d: %s", base, p.lineNumber(), fmt.Sprintf(format, args...))
}

func (p *parser) parseHeader() error {
	p.pos++
	array := false
	if p.peek() == '[' {
		array = true
		p.pos++
	}

	p.skipSpace()
	parts, err := p.parseKeyPath()
	if err != nil {
		return err
	}
	p.skipSpace()

	if array {
		if p.peek() != ']' || p.peekAt(1) != ']' {
			return p.syntaxError("expected closing ]] for array of tables")
		}
		p.pos += 2
	} else {
		if p.peek() != ']' {
			return p.syntaxError("expected closing ] for table header")
		}
		p.pos++
	}

	if parts[0] != "user" {
		return p.admissionError(ErrForbiddenRoot, "key %q does not start with user", pathString(parts))
	}
	if err := p.schema.applyHeader(parts, array); err != nil {
		return p.admissionError(err, "cannot define table %q", pathString(parts))
	}
	if err := p.finishLine(); err != nil {
		return err
	}
	return nil
}

func (p *parser) parseAssignment() error {
	parts, err := p.parseKeyPath()
	if err != nil {
		return err
	}
	p.skipSpace()
	if p.peek() != '=' {
		return p.syntaxError("expected = after key %q", pathString(parts))
	}
	p.pos++
	p.skipSpace()

	value, err := p.parseValue(topLevelContext)
	if err != nil {
		return err
	}
	if p.schema.current == p.schema.root && parts[0] != "user" {
		return p.admissionError(ErrForbiddenRoot, "key %q does not start with user", pathString(parts))
	}
	if err := p.schema.applyKey(p.schema.current, parts, value); err != nil {
		return p.admissionError(err, "cannot assign key %q", pathString(parts))
	}
	if err := p.finishLine(); err != nil {
		return err
	}
	return nil
}

func (p *parser) parseKeyPath() ([]string, error) {
	p.skipSpace()
	first, err := p.parseKeyPart()
	if err != nil {
		return nil, err
	}
	parts := []string{first}

	for {
		p.skipSpace()
		if p.peek() != '.' {
			return parts, nil
		}
		p.pos++
		p.skipSpace()
		if p.eof() || p.peek() == '=' || p.peek() == ']' || p.peek() == '\n' || p.peek() == '\r' {
			return nil, p.syntaxError("expected key after dot in %q", pathString(parts))
		}
		part, err := p.parseKeyPart()
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
}

func (p *parser) parseKeyPart() (string, error) {
	switch p.peek() {
	case '"':
		value, err := p.parseBasicString(false, true)
		if err != nil {
			return "", err
		}
		return string(value), nil
	case '\'':
		value, err := p.parseLiteralString(false)
		if err != nil {
			return "", err
		}
		return string(value), nil
	default:
		start := p.pos
		for !p.eof() && isBareKeyChar(p.src[p.pos]) {
			p.pos++
		}
		if p.pos == start {
			return "", p.syntaxError("expected a TOML key")
		}
		return string(p.src[start:p.pos]), nil
	}
}

func isBareKeyChar(b byte) bool {
	return b >= 'a' && b <= 'z' ||
		b >= 'A' && b <= 'Z' ||
		b >= '0' && b <= '9' ||
		b == '_' || b == '-'
}

type valueContext struct {
	terminators string
}

var (
	topLevelContext = valueContext{terminators: "\r\n#"}
	arrayContext    = valueContext{terminators: ",]#\r\n"}
	inlineContext   = valueContext{terminators: ",}#\r\n"}
)

func (p *parser) parseValue(ctx valueContext) (*schemaNode, error) {
	if p.eof() {
		return nil, p.syntaxError("expected a value")
	}

	switch p.peek() {
	case '"':
		if bytes.HasPrefix(p.src[p.pos:], []byte(`"""`)) {
			if _, err := p.parseBasicString(true, false); err != nil {
				return nil, err
			}
		} else if _, err := p.parseBasicString(false, false); err != nil {
			return nil, err
		}
		return newNode(valueNode), nil
	case '\'':
		if bytes.HasPrefix(p.src[p.pos:], []byte(`'''`)) {
			if _, err := p.parseLiteralString(true); err != nil {
				return nil, err
			}
		} else if _, err := p.parseLiteralString(false); err != nil {
			return nil, err
		}
		return newNode(valueNode), nil
	case '{':
		return p.parseInlineTable()
	case '[':
		if err := p.parseArray(); err != nil {
			return nil, err
		}
		return newNode(valueNode), nil
	default:
		if err := p.parseScalar(ctx); err != nil {
			return nil, err
		}
		return newNode(valueNode), nil
	}
}

func (p *parser) parseScalar(ctx valueContext) error {
	start := p.pos
	for !p.eof() && !bytes.ContainsRune([]byte(ctx.terminators), rune(p.src[p.pos])) {
		p.pos++
	}
	raw := bytes.TrimRight(p.src[start:p.pos], " \t")
	if len(raw) == 0 {
		return p.syntaxError("expected a value")
	}
	if bytes.ContainsAny(raw, "[]{}=\"'\\,;") {
		return p.syntaxError("invalid bare value %q", raw)
	}
	for _, b := range raw {
		if b < 0x20 && b != '\t' || b == 0x7f {
			return p.syntaxError("control character in value")
		}
	}
	if !validScalar(raw) {
		return p.syntaxError("invalid bare value %q", raw)
	}
	return nil
}

var (
	decimalDigits = `[0-9](?:_?[0-9])*`
	integerValue  = regexp.MustCompile(`^[+-]?(?:0|[1-9](?:_?[0-9])*|0x[0-9A-Fa-f](?:_?[0-9A-Fa-f])*|0o[0-7](?:_?[0-7])*|0b[01](?:_?[01])*)$`)
	floatValue    = regexp.MustCompile(`^[+-]?(?:` + decimalDigits + `(?:\.[0-9](?:_?[0-9])*)?(?:[eE][+-]?` + decimalDigits + `)?|inf|nan)$`)
	dateValue     = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	timeValue     = regexp.MustCompile(`^[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?$`)
	dateTimeValue = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}[Tt ][0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?(?:[Zz]|[+-][0-9]{2}:[0-9]{2})?$`)
)

func validScalar(raw []byte) bool {
	value := string(raw)
	if value == "true" || value == "false" {
		return true
	}
	return integerValue.MatchString(value) ||
		floatValue.MatchString(value) ||
		dateValue.MatchString(value) ||
		timeValue.MatchString(value) ||
		dateTimeValue.MatchString(value)
}

func (p *parser) parseArray() error {
	p.pos++

	for {
		if err := p.skipValueSpace(); err != nil {
			return err
		}
		if p.eof() {
			return p.syntaxError("unterminated array")
		}
		if p.peek() == ']' {
			p.pos++
			return nil
		}

		if _, err := p.parseValue(arrayContext); err != nil {
			return err
		}
		if err := p.skipValueSpace(); err != nil {
			return err
		}
		if p.eof() {
			return p.syntaxError("unterminated array")
		}
		switch p.peek() {
		case ',':
			p.pos++
		case ']':
			p.pos++
			return nil
		default:
			return p.syntaxError("expected , or ] in array")
		}
	}
}

func (p *parser) skipValueSpace() error {
	for {
		p.skipSpace()
		if p.peek() == '#' {
			p.skipComment()
		}
		if p.peek() == '\n' || p.peek() == '\r' {
			if err := p.skipLineBreak(); err != nil {
				return err
			}
			continue
		}
		return nil
	}
}

func (p *parser) parseInlineTable() (*schemaNode, error) {
	p.pos++
	table := newTable(inlineTable)
	p.skipSpace()
	if p.peek() == '}' {
		p.pos++
		return table, nil
	}

	for {
		parts, err := p.parseKeyPath()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.peek() != '=' {
			return nil, p.syntaxError("expected = in inline table")
		}
		p.pos++
		p.skipSpace()

		value, err := p.parseValue(inlineContext)
		if err != nil {
			return nil, err
		}
		if err := p.schema.applyKey(table, parts, value); err != nil {
			return nil, p.admissionError(err, "cannot assign inline table key %q", pathString(parts))
		}

		p.skipSpace()
		if p.eof() {
			return nil, p.syntaxError("unterminated inline table")
		}
		switch p.peek() {
		case '}':
			p.pos++
			return table, nil
		case ',':
			p.pos++
			p.skipSpace()
			if p.peek() == '}' {
				return nil, p.syntaxError("trailing comma in inline table")
			}
		default:
			return nil, p.syntaxError("expected , or } in inline table")
		}
	}
}

func (p *parser) parseBasicString(multiline bool, decode bool) ([]byte, error) {
	quoteCount := 1
	if multiline {
		quoteCount = 3
	}
	for i := 0; i < quoteCount; i++ {
		if p.peekAt(i) != '"' {
			return nil, p.syntaxError("expected basic string")
		}
	}
	p.pos += quoteCount

	if multiline && p.peek() == '\n' {
		p.pos++
	} else if multiline && p.peek() == '\r' && p.peekAt(1) == '\n' {
		p.pos += 2
	}

	var out []byte
	for {
		if p.eof() {
			return nil, p.syntaxError("unterminated basic string")
		}
		if multiline && p.peek() == '"' {
			count := p.countRepeated('"')
			if count >= 3 {
				if count > 5 {
					return nil, p.syntaxError("too many quotation marks before multiline string delimiter")
				}
				if decode {
					out = append(out, bytes.Repeat([]byte{'"'}, count-3)...)
				}
				p.pos += count
				return out, nil
			}
		}
		if !multiline && p.peek() == '"' {
			p.pos++
			return out, nil
		}

		switch p.peek() {
		case '\\':
			if err := p.parseEscape(multiline, decode, &out); err != nil {
				return nil, err
			}
		case '\n':
			if !multiline {
				return nil, p.syntaxError("newline in basic key or string")
			}
			p.pos++
		case '\r':
			if !multiline {
				return nil, p.syntaxError("newline in basic key or string")
			}
			if err := p.skipLineBreak(); err != nil {
				return nil, err
			}
		default:
			if isForbiddenControl(p.peek()) {
				return nil, p.syntaxError("control character in basic string")
			}
			if decode {
				out = append(out, p.peek())
			}
			p.pos++
		}
	}
}

func (p *parser) parseEscape(multiline bool, decode bool, out *[]byte) error {
	p.pos++
	if p.eof() {
		return p.syntaxError("unterminated escape sequence")
	}

	switch p.peek() {
	case 'b':
		if decode {
			*out = append(*out, '\b')
		}
		p.pos++
	case 't':
		if decode {
			*out = append(*out, '\t')
		}
		p.pos++
	case 'n':
		if decode {
			*out = append(*out, '\n')
		}
		p.pos++
	case 'f':
		if decode {
			*out = append(*out, '\f')
		}
		p.pos++
	case 'r':
		if decode {
			*out = append(*out, '\r')
		}
		p.pos++
	case '"':
		if decode {
			*out = append(*out, '"')
		}
		p.pos++
	case '\\':
		if decode {
			*out = append(*out, '\\')
		}
		p.pos++
	case 'u':
		return p.parseUnicodeEscape(4, decode, out)
	case 'U':
		return p.parseUnicodeEscape(8, decode, out)
	case ' ', '\t', '\n', '\r':
		if !multiline {
			return p.syntaxError("invalid escape sequence")
		}
		return p.skipEscapedLineEnding()
	default:
		return p.syntaxError("invalid escape sequence \\%c", p.peek())
	}
	return nil
}

func (p *parser) parseUnicodeEscape(digits int, decode bool, out *[]byte) error {
	p.pos++
	if p.pos+digits > len(p.src) {
		return p.syntaxError("short Unicode escape")
	}
	raw := p.src[p.pos : p.pos+digits]
	for _, b := range raw {
		if !isHexDigit(b) {
			return p.syntaxError("invalid Unicode escape %q", raw)
		}
	}
	value, err := strconv.ParseUint(string(raw), 16, 32)
	if err != nil || value > 0x10ffff || value >= 0xd800 && value <= 0xdfff {
		return p.syntaxError("invalid Unicode scalar in escape")
	}
	p.pos += digits
	if decode {
		*out = append(*out, []byte(string(rune(value)))...)
	}
	return nil
}

func (p *parser) skipEscapedLineEnding() error {
	sawLineBreak := false
	for !p.eof() {
		switch p.peek() {
		case ' ', '\t':
			p.pos++
		case '\n', '\r':
			sawLineBreak = true
			if err := p.skipLineBreak(); err != nil {
				return err
			}
		default:
			if !sawLineBreak {
				return p.syntaxError("invalid escape sequence")
			}
			return nil
		}
	}
	if !sawLineBreak {
		return p.syntaxError("unterminated escape sequence")
	}
	return nil
}

func isHexDigit(b byte) bool {
	return b >= '0' && b <= '9' ||
		b >= 'a' && b <= 'f' ||
		b >= 'A' && b <= 'F'
}

func (p *parser) parseLiteralString(multiline bool) ([]byte, error) {
	quoteCount := 1
	if multiline {
		quoteCount = 3
	}
	for i := 0; i < quoteCount; i++ {
		if p.peekAt(i) != '\'' {
			return nil, p.syntaxError("expected literal string")
		}
	}
	p.pos += quoteCount
	contentStart := p.pos

	if multiline && p.peek() == '\n' {
		p.pos++
		contentStart = p.pos
	} else if multiline && p.peek() == '\r' && p.peekAt(1) == '\n' {
		p.pos += 2
		contentStart = p.pos
	}

	for {
		if p.eof() {
			return nil, p.syntaxError("unterminated literal string")
		}
		if multiline && p.peek() == '\'' {
			count := p.countRepeated('\'')
			if count >= 3 {
				if count > 5 {
					return nil, p.syntaxError("too many single quotes before multiline string delimiter")
				}
				value := append([]byte(nil), p.src[contentStart:p.pos+count-3]...)
				p.pos += count
				return value, nil
			}
		}
		if !multiline && p.peek() == '\'' {
			value := append([]byte(nil), p.src[contentStart:p.pos]...)
			p.pos++
			return value, nil
		}

		switch p.peek() {
		case '\n':
			if !multiline {
				return nil, p.syntaxError("newline in literal key or string")
			}
			p.pos++
		case '\r':
			if !multiline {
				return nil, p.syntaxError("newline in literal key or string")
			}
			if err := p.skipLineBreak(); err != nil {
				return nil, err
			}
		default:
			if isForbiddenControl(p.peek()) {
				return nil, p.syntaxError("control character in literal string")
			}
			p.pos++
		}
	}
}

func (p *parser) countRepeated(b byte) int {
	count := 0
	for p.pos+count < len(p.src) && p.src[p.pos+count] == b {
		count++
	}
	return count
}

func isForbiddenControl(b byte) bool {
	return b < 0x20 && b != '\t' || b == 0x7f
}
