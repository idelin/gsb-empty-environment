package ionfilter

import (
	"strconv"
	"unicode/utf8"
)

type textAnnotation struct {
	text   string
	sid    int
	hasSID bool
}

type textNode struct {
	start       int
	end         int
	annotations []textAnnotation

	isSymbol bool
	symbol   string
	symbolID int
	isString bool

	isStruct    bool
	fields      []textField
	isContainer bool
	children    []*textNode
}

type textField struct {
	name string
	node *textNode
}

type textScanner struct {
	data []byte
	pos  int
	ctx  *scanContext
}

func (c *scanContext) scanText(data []byte) error {
	scanner := &textScanner{data: data, ctx: c}
	scanner.skipWhitespace()
	if scanner.matchIVM() {
		scanner.skipWhitespace()
	}
	for scanner.pos < len(scanner.data) {
		node, err := scanner.parseValue()
		if err != nil {
			return err
		}
		if err := c.checkTextNode(node); err != nil {
			return err
		}
		scanner.pos = node.end
		scanner.skipWhitespace()
	}
	return nil
}

func (s *textScanner) skipWhitespace() {
	for s.pos < len(s.data) {
		b := s.data[s.pos]
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			s.pos++
			continue
		}
		if b == '/' && s.pos+1 < len(s.data) {
			if s.data[s.pos+1] == '/' {
				for s.pos < len(s.data) && s.data[s.pos] != '\n' {
					s.pos++
				}
				continue
			}
			if s.data[s.pos+1] == '*' {
				s.pos += 2
				for s.pos+1 < len(s.data) && !(s.data[s.pos] == '*' && s.data[s.pos+1] == '/') {
					s.pos++
				}
				if s.pos+1 >= len(s.data) {
					s.pos = len(s.data)
					return
				}
				s.pos += 2
				continue
			}
		}
		return
	}
}

func (s *textScanner) matchIVM() bool {
	const ivm = "$ion_1_0"
	if s.pos+len(ivm) <= len(s.data) && string(s.data[s.pos:s.pos+len(ivm)]) == ivm {
		next := s.pos + len(ivm)
		if next == len(s.data) || !isSymbolContinuation(s.data[next]) {
			s.pos = next
			return true
		}
	}
	return false
}

func isSymbolContinuation(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	}
	switch b {
	case '_', '$', '.':
		return true
	}
	return false
}

func isIdentifierStart(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}

func (s *textScanner) peekNonSpaceAt(pos int) byte {
	for pos < len(s.data) {
		b := s.data[pos]
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			pos++
			continue
		}
		return b
	}
	return 0
}

func readDollarToken(data []byte, pos int) (textAnnotation, int, bool) {
	if pos >= len(data) || data[pos] != '$' {
		return textAnnotation{}, pos, false
	}
	end := pos + 1
	allDigits := true
	for end < len(data) && isSymbolContinuation(data[end]) {
		if !(data[end] >= '0' && data[end] <= '9') {
			allDigits = false
		}
		end++
	}
	token := string(data[pos:end])
	if end > pos+1 && allDigits {
		sid, err := strconv.Atoi(token[1:])
		if err != nil {
			return textAnnotation{}, pos, false
		}
		return textAnnotation{sid: sid, hasSID: true}, end, true
	}
	return textAnnotation{text: token}, end, true
}

func (s *textScanner) parseValue() (*textNode, error) {
	s.skipWhitespace()
	start := s.pos

	var annotations []textAnnotation
	for {
		save := s.pos
		if s.pos < len(s.data) && s.data[s.pos] == '\'' {
			text, end, err := s.readQuoted('\'')
			if err != nil {
				return nil, err
			}
			if s.peekNonSpaceAt(end) == ':' && s.peekNonSpaceAt(end+1) == ':' {
				s.pos = end + 2
				annotations = append(annotations, textAnnotation{text: text})
				continue
			}
			s.pos = save
			break
		}
		if s.pos < len(s.data) && s.data[s.pos] == '$' {
			if token, end, ok := readDollarToken(s.data, s.pos); ok {
				if s.peekNonSpaceAt(end) == ':' && s.peekNonSpaceAt(end+1) == ':' {
					s.pos = end + 2
					annotations = append(annotations, token)
					continue
				}
			}
			s.pos = save
			break
		}
		if s.pos < len(s.data) && isIdentifierStart(s.data[s.pos]) {
			end := s.pos
			for end < len(s.data) && isSymbolContinuation(s.data[end]) {
				end++
			}
			if s.peekNonSpaceAt(end) == ':' && s.peekNonSpaceAt(end+1) == ':' {
				annotations = append(annotations, textAnnotation{text: string(s.data[s.pos:end])})
				s.pos = end + 2
				continue
			}
			s.pos = save
			break
		}
		break
	}

	s.skipWhitespace()
	if s.pos >= len(s.data) {
		return nil, malformed(start, "expected a value after annotation")
	}

	node := &textNode{start: start, annotations: annotations}
	b := s.data[s.pos]

	switch {
	case b == '{':
		return s.parseStruct(node)
	case b == '[':
		return s.parseSequence(node, false)
	case b == '(':
		return s.parseSequence(node, true)
	case b == '"':
		text, end, err := s.readQuoted('"')
		if err != nil {
			return nil, err
		}
		node.isString = true
		node.symbol = text
		node.end = end
		return node, nil
	case b == '\'':
		text, end, err := s.readQuoted('\'')
		if err != nil {
			return nil, err
		}
		node.isSymbol = true
		node.symbol = text
		node.end = end
		return node, nil
	case b == '$':
		token, end, ok := readDollarToken(s.data, s.pos)
		if !ok {
			return nil, malformed(s.pos, "invalid symbol token")
		}
		if token.text == "$ion_1_0" {
			return nil, policyError(KindIVM, s.pos, "IVM is only permitted at the start of the logical stream")
		}
		node.end = end
		node.isSymbol = true
		if token.hasSID {
			node.symbolID = token.sid
		} else {
			node.symbol = token.text
		}
		return node, nil
	default:
		end, err := s.scanSimpleToken()
		if err != nil {
			return nil, err
		}
		node.end = end
		return node, nil
	}
}

func (s *textScanner) readQuoted(quote byte) (string, int, error) {
	start := s.pos
	if s.data[s.pos] != quote {
		return "", s.pos, malformed(s.pos, "expected quoted literal")
	}
	if s.pos+2 < len(s.data) && s.data[s.pos+1] == quote && s.data[s.pos+2] == quote {
		end := s.pos + 3
		for end+2 < len(s.data) && !(s.data[end] == quote && s.data[end+1] == quote && s.data[end+2] == quote) {
			end++
		}
		if end+2 >= len(s.data) {
			return "", s.pos, malformed(start, "unterminated long quoted literal")
		}
		return string(s.data[s.pos+3 : end]), end + 3, nil
	}

	var out []byte
	pos := s.pos + 1
	for pos < len(s.data) {
		b := s.data[pos]
		if b == quote {
			return string(out), pos + 1, nil
		}
		if b == '\\' {
			pos++
			if pos >= len(s.data) {
				return "", s.pos, malformed(start, "unterminated escape sequence")
			}
			escaped, next, err := decodeEscape(s.data, pos)
			if err != nil {
				return "", s.pos, err
			}
			out = append(out, escaped...)
			pos = next
			continue
		}
		if b == '\n' {
			return "", s.pos, malformed(start, "newline inside quoted literal")
		}
		out = append(out, b)
		pos++
	}
	return "", s.pos, malformed(start, "unterminated quoted literal")
}

func decodeEscape(data []byte, pos int) ([]byte, int, error) {
	b := data[pos]
	switch b {
	case 'n':
		return []byte{'\n'}, pos + 1, nil
	case 'r':
		return []byte{'\r'}, pos + 1, nil
	case 't':
		return []byte{'\t'}, pos + 1, nil
	case 'b':
		return []byte{0x08}, pos + 1, nil
	case 'f':
		return []byte{0x0C}, pos + 1, nil
	case 'v':
		return []byte{0x0B}, pos + 1, nil
	case 'a':
		return []byte{0x07}, pos + 1, nil
	case '0':
		return []byte{0}, pos + 1, nil
	case '\\', '"', '\'', '/':
		return []byte{b}, pos + 1, nil
	case '\n':
		return nil, pos + 1, nil
	case '\r':
		if pos+1 < len(data) && data[pos+1] == '\n' {
			return nil, pos + 2, nil
		}
		return nil, pos + 1, nil
	case 'x', 'u', 'U':
		width := 2
		if b == 'u' {
			width = 4
		}
		if b == 'U' {
			width = 8
		}
		if pos+width >= len(data) {
			return nil, pos, malformed(pos, "truncated unicode escape")
		}
		value, err := strconv.ParseUint(string(data[pos+1:pos+1+width]), 16, 32)
		if err != nil {
			return nil, pos, malformed(pos, "invalid unicode escape")
		}
		var buf [4]byte
		n := utf8.EncodeRune(buf[:], rune(value))
		return buf[:n], pos + 1 + width, nil
	default:
		return nil, pos, malformed(pos, "unknown escape sequence \\%c", b)
	}
}

// scanSimpleToken skips a scalar token (number, keyword, bare symbol, blob,
// clob). Only framing is validated; the downstream Ion decoder keeps the
// responsibility for scalar semantics.
func (s *textScanner) scanSimpleToken() (int, error) {
	start := s.pos
	b := s.data[s.pos]
	if b == '{' && s.pos+1 < len(s.data) && s.data[s.pos+1] == '{' {
		end := s.pos + 2
		for end+1 < len(s.data) && !(s.data[end] == '}' && s.data[end+1] == '}') {
			end++
		}
		if end+1 >= len(s.data) {
			return s.pos, malformed(start, "unterminated blob")
		}
		return end + 2, nil
	}
	for s.pos < len(s.data) {
		c := s.data[s.pos]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',' || c == '}' || c == ']' || c == ')' {
			break
		}
		s.pos++
	}
	if s.pos == start {
		return s.pos, malformed(start, "unexpected byte 0x%02x", b)
	}
	return s.pos, nil
}

func (s *textScanner) parseStruct(node *textNode) (*textNode, error) {
	node.isStruct = true
	s.pos++
	s.skipWhitespace()
	for s.pos < len(s.data) && s.data[s.pos] != '}' {
		s.skipWhitespace()
		fieldName := ""
		if s.data[s.pos] == '\'' {
			text, end, err := s.readQuoted('\'')
			if err != nil {
				return nil, err
			}
			fieldName = text
			s.pos = end
		} else {
			end := s.pos
			for end < len(s.data) && isSymbolContinuation(s.data[end]) {
				end++
			}
			if end == s.pos {
				return nil, malformed(s.pos, "expected struct field name")
			}
			fieldName = string(s.data[s.pos:end])
			s.pos = end
		}
		s.skipWhitespace()
		if s.pos >= len(s.data) || s.data[s.pos] != ':' {
			return nil, malformed(s.pos, "expected ':' after struct field name")
		}
		s.pos++
		child, err := s.parseValue()
		if err != nil {
			return nil, err
		}
		node.fields = append(node.fields, textField{name: fieldName, node: child})
		s.pos = child.end
		s.skipWhitespace()
		if s.pos < len(s.data) && s.data[s.pos] == ',' {
			s.pos++
			s.skipWhitespace()
		}
	}
	if s.pos >= len(s.data) {
		return nil, malformed(node.start, "unterminated struct")
	}
	s.pos++
	node.end = s.pos
	return node, nil
}

func (s *textScanner) parseSequence(node *textNode, isSexp bool) (*textNode, error) {
	node.isContainer = true
	closing := byte(']')
	if isSexp {
		closing = ')'
	}
	s.pos++
	s.skipWhitespace()
	for s.pos < len(s.data) && s.data[s.pos] != closing {
		child, err := s.parseValue()
		if err != nil {
			return nil, err
		}
		node.children = append(node.children, child)
		s.pos = child.end
		s.skipWhitespace()
		if s.pos < len(s.data) && s.data[s.pos] == ',' {
			s.pos++
			s.skipWhitespace()
		}
	}
	if s.pos >= len(s.data) {
		return nil, malformed(node.start, "unterminated container")
	}
	s.pos++
	node.end = s.pos
	return node, nil
}

func (c *scanContext) checkTextNode(node *textNode) error {
	trusted := c.trust.trustedAt(node.start)
	directive := false
	for _, ann := range node.annotations {
		text := ann.text
		if ann.hasSID {
			resolved, err := c.table.resolve(ann.sid)
			if err != nil {
				return policyError(KindAnnotation, node.start, "annotation SID %d cannot be resolved", ann.sid)
			}
			text = resolved
		}
		if isDirectiveAnnotation(text) {
			if !trusted {
				return policyError(KindSymbolTable, node.start,
					"symbol table directive %q is forbidden in user data", text)
			}
			directive = true
			continue
		}
		if !c.allowed(text) {
			return policyError(KindAnnotation, node.start, "annotation %q is not in the allow list", text)
		}
	}
	if directive {
		if err := c.applyTextDirective(node); err != nil {
			return err
		}
	}
	for _, field := range node.fields {
		if err := c.checkTextNode(field.node); err != nil {
			return err
		}
	}
	for _, child := range node.children {
		if err := c.checkTextNode(child); err != nil {
			return err
		}
	}
	return nil
}

// applyTextDirective consumes a trusted $ion_symbol_table struct written in
// text Ion. Symbols declared in the "symbols" list become available for
// subsequent values, matching the semantics of a local symbol table.
func (c *scanContext) applyTextDirective(node *textNode) error {
	for _, ann := range node.annotations {
		text := ann.text
		if ann.hasSID {
			resolved, err := c.table.resolve(ann.sid)
			if err != nil {
				return malformed(node.start, "directive annotation SID %d cannot be resolved", ann.sid)
			}
			text = resolved
		}
		if text == annSharedSymbolTable {
			continue
		}
		if text != annSymbolTable {
			continue
		}
		if !node.isStruct {
			return malformed(node.start, "local symbol table directive must annotate a struct")
		}
		var symbolField *textNode
		for _, field := range node.fields {
			if field.name == "imports" {
				if field.node.isSymbol && field.node.symbol == annSymbolTable {
					c.table.reset()
				}
			}
			if field.name == "symbols" {
				symbolField = field.node
			}
		}
		if symbolField == nil {
			continue
		}
		if !symbolField.isContainer {
			return malformed(node.start, "symbols directive must be a list")
		}
		names := make([]string, 0, len(symbolField.children))
		for _, child := range symbolField.children {
			if !child.isSymbol && !child.isString {
				return malformed(node.start, "symbols list must contain symbol values")
			}
			if child.symbolID != 0 {
				resolved, err := c.table.resolve(child.symbolID)
				if err != nil {
					return malformed(node.start, "symbol in symbols list cannot be resolved")
				}
				names = append(names, resolved)
				continue
			}
			names = append(names, child.symbol)
		}
		c.table.appendLocal(names)
	}
	return nil
}
