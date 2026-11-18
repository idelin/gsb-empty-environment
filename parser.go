package netsec

import (
	"regexp"
	"strings"
)

type parser struct {
	data        []byte
	i           int
	root        *tableNode
	current     *tableNode
	currentPath []string
}

func newParser(data []byte, root *tableNode) *parser {
	return &parser{data: data, root: root, current: root}
}

func (p *parser) parse() error {
	for {
		if err := p.skipBlankLines(); err != nil {
			return err
		}
		if p.eof() {
			return nil
		}

		var err error
		if p.peek() == '[' {
			err = p.parseHeader()
		} else {
			err = p.parseKeyValue()
		}
		if err != nil {
			return err
		}
	}
}

func (p *parser) parseHeader() error {
	start := p.i
	p.i++
	arrayTable := false
	if !p.eof() && p.peek() == '[' {
		arrayTable = true
		p.i++
	}

	p.skipHWS()
	path, err := p.parseKeyPath()
	if err != nil {
		return err
	}
	if len(path) == 0 {
		return p.syntax(start, "empty table path")
	}
	p.skipHWS()

	if arrayTable {
		if !p.consume("]]") {
			return p.syntax(p.i, "array table header must end with ]]")
		}
	} else if !p.consume("]") {
		return p.syntax(p.i, "table header must end with ]")
	}
	if err := p.finishStatement(); err != nil {
		return err
	}
	if path[0] != "user" {
		return newError(ErrForbiddenRoot, "", path, start)
	}

	current, err := p.root.declareHeader(path, arrayTable)
	if err != nil {
		return newError(err, "", path, start)
	}
	p.current = current
	p.currentPath = append(p.currentPath[:0], path...)
	return nil
}

func (p *parser) parseKeyValue() error {
	start := p.i
	keyPath, err := p.parseKeyPath()
	if err != nil {
		return err
	}
	if len(keyPath) == 0 {
		return p.syntax(start, "empty key")
	}
	p.skipHWS()
	if !p.consume("=") {
		return p.syntax(p.i, "expected = after key")
	}
	p.skipHWS()
	if p.eof() || p.peek() == '\n' || p.peek() == '#' {
		return p.syntax(p.i, "missing value")
	}

	valueKind, err := p.parseValue()
	if err != nil {
		return err
	}
	if err := p.finishStatement(); err != nil {
		return err
	}

	fullPath := make([]string, 0, len(p.currentPath)+len(keyPath))
	fullPath = append(fullPath, p.currentPath...)
	fullPath = append(fullPath, keyPath...)
	if fullPath[0] != "user" {
		return newError(ErrForbiddenRoot, "", fullPath, start)
	}
	if err := assignValue(p.current, keyPath, valueKind); err != nil {
		return newError(err, "", fullPath, start)
	}
	return nil
}

func (p *parser) parseValue() (nodeKind, error) {
	if p.eof() {
		return kindValue, p.syntax(p.i, "missing value")
	}
	switch p.peek() {
	case '"':
		if err := p.parseBasicStringValue(); err != nil {
			return kindValue, err
		}
		return kindValue, nil
	case '\'':
		if err := p.parseLiteralStringValue(); err != nil {
			return kindValue, err
		}
		return kindValue, nil
	case '[':
		if err := p.parseArray(); err != nil {
			return kindValue, err
		}
		return kindValue, nil
	case '{':
		if err := p.parseInlineTable(); err != nil {
			return kindValue, err
		}
		return kindInlineTable, nil
	default:
		return p.parseBareValue()
	}
}

func (p *parser) parseKeyPath() ([]string, error) {
	p.skipHWS()
	segment, err := p.parseKeySegment()
	if err != nil {
		return nil, err
	}
	path := []string{segment}

	for {
		p.skipHWS()
		if p.eof() || p.peek() != '.' {
			return path, nil
		}
		dotOffset := p.i
		p.i++
		p.skipHWS()
		if p.eof() {
			return nil, p.syntax(dotOffset, "missing key after dot")
		}
		segment, err = p.parseKeySegment()
		if err != nil {
			return nil, err
		}
		path = append(path, segment)
	}
}

func (p *parser) parseKeySegment() (string, error) {
	if p.eof() {
		return "", p.syntax(p.i, "missing key segment")
	}
	switch p.peek() {
	case '"':
		return p.parseBasicKey()
	case '\'':
		return p.parseLiteralKey()
	default:
		return p.parseBareKey()
	}
}

func (p *parser) parseBareKey() (string, error) {
	start := p.i
	for !p.eof() && isBareKeyByte(p.peek()) {
		p.i++
	}
	if p.i == start {
		return "", p.syntax(start, "invalid bare key")
	}
	return string(p.data[start:p.i]), nil
}

func (p *parser) parseBasicKey() (string, error) {
	start := p.i
	p.i++
	var b strings.Builder
	for {
		if p.eof() {
			return "", p.syntax(start, "unterminated quoted key")
		}
		c := p.peek()
		switch {
		case c == '"':
			p.i++
			return b.String(), nil
		case c == '\\':
			r, skip, err := p.consumeEscape(false)
			if err != nil {
				return "", err
			}
			if !skip {
				b.WriteRune(r)
			}
		case c == '\n' || c == '\r':
			return "", p.syntax(p.i, "newline in quoted key")
		case isValidStringByte(c):
			b.WriteByte(c)
			p.i++
		default:
			return "", p.syntax(p.i, "control character in quoted key")
		}
	}
}

func (p *parser) parseLiteralKey() (string, error) {
	start := p.i
	p.i++
	var b strings.Builder
	for {
		if p.eof() {
			return "", p.syntax(start, "unterminated literal key")
		}
		c := p.peek()
		switch {
		case c == '\'':
			p.i++
			return b.String(), nil
		case c == '\n' || c == '\r':
			return "", p.syntax(p.i, "newline in literal key")
		case isValidStringByte(c):
			b.WriteByte(c)
			p.i++
		default:
			return "", p.syntax(p.i, "control character in literal key")
		}
	}
}

func (p *parser) parseBasicStringValue() error {
	if p.hasPrefix(`"""`) {
		p.i += 3
		return p.parseMultilineBasicString()
	}

	start := p.i
	p.i++
	for {
		if p.eof() {
			return p.syntax(start, "unterminated string")
		}
		c := p.peek()
		switch {
		case c == '"':
			p.i++
			return nil
		case c == '\\':
			if _, _, err := p.consumeEscape(false); err != nil {
				return err
			}
		case c == '\n' || c == '\r':
			return p.syntax(p.i, "newline in basic string")
		case isValidStringByte(c):
			p.i++
		default:
			return p.syntax(p.i, "control character in string")
		}
	}
}

func (p *parser) parseMultilineBasicString() error {
	if !p.eof() && p.peek() == '\n' {
		p.i++
	}
	for {
		if p.eof() {
			return p.syntax(p.i, "unterminated multiline string")
		}
		c := p.peek()
		switch {
		case c == '"':
			run := p.quoteRun()
			if run >= 3 {
				if run > 5 {
					return p.syntax(p.i, "too many closing quotes")
				}
				p.i += run
				return nil
			}
			p.i += run
		case c == '\\':
			if _, _, err := p.consumeEscape(true); err != nil {
				return err
			}
		case c == '\n' || c == '\t':
			p.i++
		case isControlByte(c):
			return p.syntax(p.i, "control character in multiline string")
		default:
			p.i++
		}
	}
}

func (p *parser) parseLiteralStringValue() error {
	if p.hasPrefix("'''") {
		p.i += 3
		return p.parseMultilineLiteralString()
	}

	start := p.i
	p.i++
	for {
		if p.eof() {
			return p.syntax(start, "unterminated literal string")
		}
		c := p.peek()
		switch {
		case c == '\'':
			p.i++
			return nil
		case c == '\n' || c == '\r':
			return p.syntax(p.i, "newline in literal string")
		case isValidStringByte(c):
			p.i++
		default:
			return p.syntax(p.i, "control character in literal string")
		}
	}
}

func (p *parser) parseMultilineLiteralString() error {
	if !p.eof() && p.peek() == '\n' {
		p.i++
	}
	for {
		if p.eof() {
			return p.syntax(p.i, "unterminated multiline literal string")
		}
		c := p.peek()
		switch {
		case c == '\'':
			run := p.quoteRun()
			if run >= 3 {
				if run > 5 {
					return p.syntax(p.i, "too many closing quotes")
				}
				p.i += run
				return nil
			}
			p.i += run
		case c == '\n' || c == '\t':
			p.i++
		case isControlByte(c):
			return p.syntax(p.i, "control character in multiline literal string")
		default:
			p.i++
		}
	}
}

func (p *parser) consumeEscape(lineContinuation bool) (rune, bool, error) {
	start := p.i
	p.i++
	if p.eof() {
		return 0, false, p.syntax(start, "incomplete escape")
	}

	c := p.peek()
	if lineContinuation && c == '\n' {
		p.i++
		for !p.eof() {
			switch p.peek() {
			case ' ', '\t', '\n':
				p.i++
			default:
				return 0, true, nil
			}
		}
		return 0, true, nil
	}

	p.i++
	switch c {
	case '"':
		return '"', false, nil
	case '\\':
		return '\\', false, nil
	case 'b':
		return '\b', false, nil
	case 't':
		return '\t', false, nil
	case 'n':
		return '\n', false, nil
	case 'f':
		return '\f', false, nil
	case 'r':
		return '\r', false, nil
	case 'u':
		return p.consumeUnicodeEscape(start, 4)
	case 'U':
		return p.consumeUnicodeEscape(start, 8)
	default:
		return 0, false, p.syntax(start, "invalid escape sequence")
	}
}

func (p *parser) consumeUnicodeEscape(start, digits int) (rune, bool, error) {
	if p.i+digits > len(p.data) {
		return 0, false, p.syntax(start, "incomplete Unicode escape")
	}
	var value int64
	for j := 0; j < digits; j++ {
		digit, ok := hexValue(p.data[p.i+j])
		if !ok {
			return 0, false, p.syntax(start, "invalid Unicode escape")
		}
		value = value*16 + int64(digit)
	}
	p.i += digits
	if value > 0x10FFFF || (value >= 0xD800 && value <= 0xDFFF) {
		return 0, false, p.syntax(start, "Unicode escape is not a scalar value")
	}
	return rune(value), false, nil
}

func (p *parser) parseArray() error {
	start := p.i
	p.i++
	if err := p.skipArrayWhitespace(); err != nil {
		return err
	}
	if p.eof() {
		return p.syntax(start, "unterminated array")
	}
	if p.peek() == ']' {
		p.i++
		return nil
	}

	for {
		if _, err := p.parseValue(); err != nil {
			return err
		}
		if err := p.skipArrayWhitespace(); err != nil {
			return err
		}
		if p.eof() {
			return p.syntax(start, "unterminated array")
		}

		switch p.peek() {
		case ',':
			p.i++
			if err := p.skipArrayWhitespace(); err != nil {
				return err
			}
			if p.eof() {
				return p.syntax(start, "unterminated array")
			}
			if p.peek() == ']' {
				p.i++
				return nil
			}
		case ']':
			p.i++
			return nil
		default:
			return p.syntax(p.i, "expected , or ] in array")
		}
	}
}

func (p *parser) parseInlineTable() error {
	start := p.i
	p.i++
	local := newTable()
	p.skipHWS()
	if p.eof() {
		return p.syntax(start, "unterminated inline table")
	}
	if p.peek() == '}' {
		p.i++
		return nil
	}

	for {
		if p.eof() || p.peek() == '\n' || p.peek() == '\r' {
			return p.syntax(p.i, "newline in inline table")
		}
		keyPath, err := p.parseKeyPath()
		if err != nil {
			return err
		}
		if len(keyPath) == 0 {
			return p.syntax(p.i, "empty key in inline table")
		}
		p.skipHWS()
		if !p.consume("=") {
			return p.syntax(p.i, "expected = in inline table")
		}
		p.skipHWS()
		if p.eof() || p.peek() == '\n' || p.peek() == '\r' || p.peek() == ',' || p.peek() == '}' {
			return p.syntax(p.i, "missing inline table value")
		}
		valueKind, err := p.parseValue()
		if err != nil {
			return err
		}
		if err := defineInlineValue(local, keyPath, valueKind); err != nil {
			return newError(err, "inside inline table", keyPath, start)
		}

		p.skipHWS()
		if p.eof() {
			return p.syntax(start, "unterminated inline table")
		}
		switch p.peek() {
		case ',':
			p.i++
			p.skipHWS()
			if p.eof() {
				return p.syntax(start, "unterminated inline table")
			}
			if p.peek() == '}' {
				return p.syntax(p.i, "trailing comma in inline table")
			}
		case '}':
			p.i++
			return nil
		default:
			return p.syntax(p.i, "expected , or } in inline table")
		}
	}
}

func (p *parser) parseBareValue() (nodeKind, error) {
	start := p.i
	for !p.eof() {
		switch p.peek() {
		case '\n', '#', ',', ']', '}':
			goto done
		}
		p.i++
	}

done:
	end := p.i
	for end > start && (p.data[end-1] == ' ' || p.data[end-1] == '\t') {
		end--
	}
	value := string(p.data[start:end])
	if value == "" || !tomlBareValueRE.MatchString(value) {
		return kindValue, p.syntax(start, "invalid value")
	}
	p.i = end
	return kindValue, nil
}

func (p *parser) skipBlankLines() error {
	for {
		p.skipHWS()
		if p.eof() {
			return nil
		}
		switch p.peek() {
		case '\n':
			p.i++
		case '#':
			if err := p.skipComment(); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

func (p *parser) skipArrayWhitespace() error {
	for {
		p.skipHWS()
		if p.eof() {
			return nil
		}
		switch p.peek() {
		case '\n':
			p.i++
		case '#':
			if err := p.skipComment(); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

func (p *parser) skipComment() error {
	start := p.i
	p.i++
	for !p.eof() && p.peek() != '\n' {
		if isControlByte(p.peek()) {
			return p.syntax(start, "control character in comment")
		}
		p.i++
	}
	return nil
}

func (p *parser) finishStatement() error {
	p.skipHWS()
	if p.eof() {
		return nil
	}
	if p.peek() == '#' {
		if err := p.skipComment(); err != nil {
			return err
		}
	}
	if p.eof() {
		return nil
	}
	if p.peek() != '\n' {
		return p.syntax(p.i, "unexpected content after statement")
	}
	p.i++
	return nil
}

func (p *parser) quoteRun() int {
	c := p.peek()
	run := 0
	for p.i+run < len(p.data) && p.data[p.i+run] == c {
		run++
	}
	return run
}

func (p *parser) skipHWS() {
	for !p.eof() && (p.peek() == ' ' || p.peek() == '\t') {
		p.i++
	}
}

func (p *parser) consume(s string) bool {
	if p.i+len(s) > len(p.data) || string(p.data[p.i:p.i+len(s)]) != s {
		return false
	}
	p.i += len(s)
	return true
}

func (p *parser) hasPrefix(s string) bool {
	return p.i+len(s) <= len(p.data) && string(p.data[p.i:p.i+len(s)]) == s
}

func (p *parser) peek() byte { return p.data[p.i] }

func (p *parser) eof() bool { return p.i >= len(p.data) }

func (p *parser) syntax(offset int, detail string) error {
	return newError(ErrInvalidTOML, detail, nil, offset)
}

func isBareKeyByte(c byte) bool {
	return c >= 'A' && c <= 'Z' ||
		c >= 'a' && c <= 'z' ||
		c >= '0' && c <= '9' ||
		c == '_' || c == '-'
}

func isControlByte(c byte) bool {
	return c == 0x7F || (c < 0x20 && c != '\t' && c != '\n')
}

func isValidStringByte(c byte) bool {
	return c == '\t' || !isControlByte(c)
}

func hexValue(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	default:
		return 0, false
	}
}

const (
	tomlDate     = `[0-9]{4}-[0-9]{2}-[0-9]{2}`
	tomlTime     = `[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?`
	tomlDateTime = tomlDate + `(?:[Tt ]` + tomlTime + `(?:[Zz]|[+-][0-9]{2}:[0-9]{2})?)?`
	tomlInteger  = `[+-]?(?:0|[1-9](?:_?[0-9])*)|0x[0-9A-Fa-f](?:_?[0-9A-Fa-f])*|0o[0-7](?:_?[0-7])*|0b[01](?:_?[01])*`
	tomlFloat    = `[+-]?(?:0|[1-9](?:_?[0-9])*)(?:(?:\.[0-9](?:_?[0-9])*)(?:[eE][+-]?[0-9](?:_?[0-9])*)?|[eE][+-]?[0-9](?:_?[0-9])*)`
)

var tomlBareValueRE = regexp.MustCompile(
	`^(?:true|false|[+-]?(?:inf|nan)|` +
		tomlDateTime + `|` + tomlTime + `|` + tomlFloat + `|` + tomlInteger + `)$`,
)
