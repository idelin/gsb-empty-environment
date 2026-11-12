package yaml

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

type sourceLine struct {
	num    int
	indent int
	full   string
	text   string
	blank  bool
}

type parser struct {
	lines   []sourceLine
	pos     int
	anchors map[string]*Node
}

func parseDocuments(data []byte) ([]*Node, error) {
	lines := splitSource(data)
	p := &parser{lines: lines, anchors: make(map[string]*Node)}
	var docs []*Node

	for {
		p.skipBlank()
		if p.eof() {
			break
		}
		line := p.lines[p.pos]
		if strings.HasPrefix(strings.TrimSpace(line.text), "%") {
			p.pos++
			continue
		}
		if line.text == "..." {
			p.pos++
			continue
		}
		if line.text == "---" {
			p.pos++
			p.anchors = make(map[string]*Node)
			continue
		}
		if strings.HasPrefix(line.text, "--- ") {
			rest := strings.TrimSpace(strings.TrimPrefix(line.text, "---"))
			col := line.indent + len(line.text) - len(rest) + 1
			p.pos++
			root, err := p.parseInline(rest, line.num, col)
			if err != nil {
				return nil, err
			}
			docs = append(docs, documentFor(root))
			continue
		}

		root, err := p.parseBlock(line.indent)
		if err != nil {
			return nil, err
		}
		if root == nil {
			return nil, fmt.Errorf("yaml: line %d: empty document", line.num)
		}
		docs = append(docs, documentFor(root))

		p.skipBlank()
		if p.eof() {
			break
		}
		next := p.lines[p.pos]
		if next.text == "..." || next.text == "---" || strings.HasPrefix(next.text, "--- ") {
			continue
		}
		return nil, fmt.Errorf("yaml: line %d: unexpected content", next.num)
	}
	return docs, nil
}

func documentFor(root *Node) *Node {
	return &Node{
		Kind:    DocumentNode,
		Content: []*Node{root},
		Line:    root.Line,
		Column:  root.Column,
	}
}

func splitSource(data []byte) []sourceLine {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	parts := strings.Split(text, "\n")
	lines := make([]sourceLine, 0, len(parts))
	for i, full := range parts {
		full = strings.TrimRight(full, " \t")
		indent := countIndent(full)
		content := full[indent:]
		uncommented := stripComment(content)
		uncommented = strings.TrimRight(uncommented, " \t")
		lines = append(lines, sourceLine{
			num:    i + 1,
			indent: indent,
			full:   full,
			text:   uncommented,
			blank:  strings.TrimSpace(uncommented) == "",
		})
	}
	joinFlowLines(lines)
	return lines
}

// joinFlowLines folds a flow collection that spans multiple physical lines into
// the line where it starts. The consumed lines become blank, so the block
// parser never mistakes their indentation for a new block.
func joinFlowLines(lines []sourceLine) {
	blockIndent := -1
	for i := 0; i < len(lines); i++ {
		if lines[i].blank {
			continue
		}
		if blockIndent >= 0 {
			if lines[i].indent > blockIndent {
				continue
			}
			blockIndent = -1
		}
		if startsBlockScalar(lines[i].text) {
			blockIndent = lines[i].indent
			continue
		}
		depth := flowDepth(lines[i].text)
		if depth <= 0 {
			continue
		}
		var builder strings.Builder
		builder.WriteString(lines[i].text)
		j := i + 1
		for j < len(lines) && depth > 0 {
			if !lines[j].blank {
				builder.WriteByte(' ')
				builder.WriteString(strings.TrimSpace(lines[j].text))
				depth += flowDepth(lines[j].text)
			}
			lines[j].blank = true
			lines[j].text = ""
			j++
		}
		lines[i].text = builder.String()
		i = j - 1
	}
}

func startsBlockScalar(text string) bool {
	if isBlockScalarHeader(strings.TrimSpace(text)) {
		return true
	}
	if strings.HasPrefix(strings.TrimSpace(text), "- ") {
		item := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "- "))
		if isBlockScalarHeader(item) {
			return true
		}
	}
	if colon := findMapColon(text); colon >= 0 {
		return isBlockScalarHeader(strings.TrimSpace(text[colon+1:]))
	}
	return false
}

func flowDepth(s string) int {
	quote := byte(0)
	escaped := false
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote == '"' {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				quote = 0
			}
			continue
		}
		if quote == '\'' {
			if c == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					i++
				} else {
					quote = 0
				}
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		}
	}
	return depth
}

func countIndent(s string) int {
	n := 0
	for n < len(s) && s[n] == ' ' {
		n++
	}
	return n
}

func stripComment(s string) string {
	quote := byte(0)
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote == '"' {
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				quote = 0
			}
			continue
		}
		if quote == '\'' {
			if c == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					i++
					continue
				}
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '#':
			if i == 0 || s[i-1] == ' ' || s[i-1] == '\t' {
				return s[:i]
			}
		}
	}
	return s
}

func (p *parser) eof() bool {
	return p.pos >= len(p.lines)
}

func (p *parser) skipBlank() {
	for p.pos < len(p.lines) && p.lines[p.pos].blank {
		p.pos++
	}
}

func (p *parser) current() sourceLine {
	return p.lines[p.pos]
}

func isDocumentMarker(text string) bool {
	return text == "---" || text == "..." || strings.HasPrefix(text, "--- ")
}

func isDashItem(text string) bool {
	return text == "-" || strings.HasPrefix(text, "- ")
}

func (p *parser) parseBlock(indent int) (*Node, error) {
	p.skipBlank()
	if p.eof() || p.current().indent < indent || isDocumentMarker(p.current().text) {
		return nil, nil
	}
	line := p.current()
	indent = line.indent
	if isDashItem(line.text) {
		return p.parseSequence(indent)
	}
	if findMapColon(line.text) >= 0 {
		return p.parseMapping(indent)
	}
	node, err := p.parseInline(line.text, line.num, line.indent+1)
	if err != nil {
		return nil, err
	}
	p.pos++
	return node, nil
}

func (p *parser) parseSequence(indent int) (*Node, error) {
	first := p.current()
	seq := &Node{Kind: SequenceNode, Tag: "!!seq", Line: first.num, Column: indent + 1}
	for {
		p.skipBlank()
		if p.eof() || p.current().indent != indent || !isDashItem(p.current().text) {
			break
		}
		line := p.current()
		rest := line.text[1:]
		trimmed := strings.TrimLeft(rest, " ")
		col := indent + 1 + 1 + (len(rest) - len(trimmed))
		p.pos++

		var child *Node
		var err error
		switch {
		case trimmed == "":
			child, err = p.parseChildBlock(indent, line.num, col)
		case isBlockScalarHeader(trimmed):
			child, err = p.parseBlockScalar(trimmed, line.num, col, indent)
		case propertiesOnly(trimmed):
			child, err = p.parsePropertiesAndNested(trimmed, line.num, col, indent)
		case findMapColon(trimmed) >= 0 && !startsFlowOrAliasOrProperty(trimmed):
			child, err = p.parseMappingInline(trimmed, line.num, col, col-1)
		default:
			child, err = p.parseInline(trimmed, line.num, col)
		}
		if err != nil {
			return nil, err
		}
		if child == nil {
			child = nullNode(line.num, col)
		}
		seq.Content = append(seq.Content, child)
	}
	return seq, nil
}

func (p *parser) parseMapping(indent int) (*Node, error) {
	first := p.current()
	mapping := &Node{Kind: MappingNode, Tag: "!!map", Line: first.num, Column: indent + 1}
	for {
		p.skipBlank()
		if p.eof() || p.current().indent != indent || isDashItem(p.current().text) ||
			isDocumentMarker(p.current().text) {
			break
		}
		line := p.current()
		colon := findMapColon(line.text)
		if colon < 0 {
			return nil, fmt.Errorf("yaml: line %d: expected a mapping key", line.num)
		}
		p.pos++
		if err := p.appendMappingPair(mapping, line.text, line.num, line.indent+1, indent); err != nil {
			return nil, err
		}
	}
	return mapping, nil
}

func (p *parser) parseMappingInline(text string, line, col, indent int) (*Node, error) {
	mapping := &Node{Kind: MappingNode, Tag: "!!map", Line: line, Column: col}
	if err := p.appendMappingPair(mapping, text, line, col, indent); err != nil {
		return nil, err
	}
	for {
		p.skipBlank()
		if p.eof() || p.current().indent != indent || isDashItem(p.current().text) ||
			isDocumentMarker(p.current().text) {
			break
		}
		current := p.current()
		if findMapColon(current.text) < 0 {
			return nil, fmt.Errorf("yaml: line %d: expected a mapping key", current.num)
		}
		p.pos++
		if err := p.appendMappingPair(mapping, current.text, current.num, current.indent+1, indent); err != nil {
			return nil, err
		}
	}
	return mapping, nil
}

func (p *parser) appendMappingPair(mapping *Node, text string, line, col, indent int) error {
	colon := findMapColon(text)
	if colon < 0 {
		return fmt.Errorf("yaml: line %d: expected a mapping key", line)
	}
	keyText := strings.TrimSpace(text[:colon])
	keyOffset := len(text[:colon]) - len(strings.TrimLeft(text[:colon], " "))
	key, err := p.parseInline(keyText, line, col+keyOffset)
	if err != nil {
		return err
	}
	if key == nil {
		return fmt.Errorf("yaml: line %d: empty mapping key", line)
	}

	rest := text[colon+1:]
	valueText := strings.TrimSpace(rest)
	valueOffset := colon + 1 + (len(rest) - len(strings.TrimLeft(rest, " ")))
	valueCol := col + valueOffset

	var value *Node
	switch {
	case valueText == "":
		value, err = p.parseValueAfterKey(line, valueCol, indent)
	case isBlockScalarHeader(valueText):
		value, err = p.parseBlockScalar(valueText, line, valueCol, indent)
	case propertiesOnly(valueText):
		value, err = p.parsePropertiesAndNested(valueText, line, valueCol, indent)
	default:
		value, err = p.parseInline(valueText, line, valueCol)
	}
	if err != nil {
		return err
	}
	if value == nil {
		value = nullNode(line, valueCol)
	}
	mapping.Content = append(mapping.Content, key, value)
	return nil
}

func (p *parser) parseValueAfterKey(line, col, indent int) (*Node, error) {
	p.skipBlank()
	if !p.eof() && p.current().indent > indent && !isDocumentMarker(p.current().text) {
		return p.parseBlock(p.current().indent)
	}
	return nullNode(line, col), nil
}

func (p *parser) parseChildBlock(parentIndent, line, col int) (*Node, error) {
	p.skipBlank()
	if !p.eof() && p.current().indent > parentIndent && !isDocumentMarker(p.current().text) {
		return p.parseBlock(p.current().indent)
	}
	return nullNode(line, col), nil
}

func (p *parser) parsePropertiesAndNested(text string, line, col, indent int) (*Node, error) {
	tag, anchor, rest, restCol, err := splitProperties(text, col)
	if err != nil {
		return nil, err
	}
	if rest != "" {
		return p.parseInline(text, line, col)
	}
	var placeholder *Node
	if anchor != "" {
		placeholder = &Node{Anchor: anchor, Line: line, Column: col}
		p.anchors[anchor] = placeholder
	}
	child, err := p.parseChildBlock(indent, line, restCol)
	if err != nil {
		return nil, err
	}
	if placeholder != nil {
		*placeholder = *child
		placeholder.Anchor = anchor
		child = placeholder
	}
	if tag != "" {
		child.Tag = normalizeTag(tag)
		child.Style |= TaggedStyle
	}
	if anchor != "" {
		p.anchors[anchor] = child
	}
	return child, nil
}

func (p *parser) registerAnchor(node *Node, anchor string) {
	if node == nil {
		return
	}
	node.Anchor = anchor
	p.anchors[anchor] = node
}

func (p *parser) parseInline(text string, line, col int) (*Node, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	tag, anchor, rest, restCol, err := splitProperties(text, col)
	if err != nil {
		return nil, err
	}

	var node *Node
	switch {
	case rest == "":
		node = nullNode(line, restCol)
	case rest[0] == '*':
		name := strings.TrimSpace(rest[1:])
		target, ok := p.anchors[name]
		if !ok {
			return nil, fmt.Errorf("yaml: line %d: unknown anchor %q referenced", line, name)
		}
		node = &Node{Kind: AliasNode, Value: name, Alias: target, Line: line, Column: restCol}
	case rest[0] == '[':
		node = &Node{Kind: SequenceNode, Tag: "!!seq", Style: FlowStyle, Line: line, Column: restCol}
		if anchor != "" {
			p.registerAnchor(node, anchor)
			anchor = ""
		}
		f := &flowParser{p: p, s: rest, line: line, col: restCol}
		if err := f.parseSequence(node); err != nil {
			return nil, err
		}
	case rest[0] == '{':
		node = &Node{Kind: MappingNode, Tag: "!!map", Style: FlowStyle, Line: line, Column: restCol}
		if anchor != "" {
			p.registerAnchor(node, anchor)
			anchor = ""
		}
		f := &flowParser{p: p, s: rest, line: line, col: restCol}
		if err := f.parseMapping(node); err != nil {
			return nil, err
		}
	case rest[0] == '\'' || rest[0] == '"':
		value, style, err := parseQuoted(rest, line, restCol)
		if err != nil {
			return nil, err
		}
		node = &Node{Kind: ScalarNode, Tag: "!!str", Value: value, Style: style, Line: line, Column: restCol}
	default:
		value := strings.TrimSpace(rest)
		node = &Node{Kind: ScalarNode, Value: value, Tag: resolveScalarTag(value, 0), Line: line, Column: restCol}
	}

	if tag != "" {
		node.Tag = normalizeTag(tag)
		node.Style |= TaggedStyle
	}
	if anchor != "" {
		p.registerAnchor(node, anchor)
	}
	return node, nil
}

func nullNode(line, col int) *Node {
	return &Node{Kind: ScalarNode, Tag: "!!null", Line: line, Column: col}
}

func normalizeTag(tag string) string {
	if strings.HasPrefix(tag, "!!") {
		return tag
	}
	return tag
}

func resolveScalarTag(value string, style Style) string {
	if style&DoubleQuotedStyle != 0 || style&SingleQuotedStyle != 0 ||
		style&LiteralStyle != 0 || style&FoldedStyle != 0 {
		return "!!str"
	}
	lower := strings.ToLower(value)
	switch lower {
	case "", "~", "null":
		return "!!null"
	case "true", "false", "yes", "no", "on", "off":
		return "!!bool"
	case "<<":
		return "!!merge"
	}
	if _, err := strconv.ParseInt(cleanNumber(value), 0, 64); err == nil {
		return "!!int"
	}
	if _, err := strconv.ParseUint(cleanNumber(value), 0, 64); err == nil {
		return "!!int"
	}
	if _, err := strconv.ParseFloat(cleanNumber(value), 64); err == nil && looksNumeric(value) {
		return "!!float"
	}
	return "!!str"
}

func cleanNumber(s string) string {
	return strings.ReplaceAll(s, "_", "")
}

func looksNumeric(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.' ||
			c == 'e' || c == 'E' || c == 'x' || c == 'X' || c == 'o' ||
			c == 'b' || c == 'B' || c == '_' {
			continue
		}
		return false
	}
	return true
}

func isBlockScalarHeader(text string) bool {
	if text == "" {
		return false
	}
	if text[0] != '|' && text[0] != '>' {
		return false
	}
	for i := 1; i < len(text); i++ {
		switch text[i] {
		case '+', '-', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		default:
			return false
		}
	}
	return true
}

func (p *parser) parseBlockScalar(header string, line, col, parentIndent int) (*Node, error) {
	style := LiteralStyle
	if header[0] == '>' {
		style = FoldedStyle
	}
	chomp := byte(0)
	for i := 1; i < len(header); i++ {
		if header[i] == '+' || header[i] == '-' {
			chomp = header[i]
		}
	}

	start := p.pos
	minIndent := -1
	end := start
	for end < len(p.lines) {
		l := p.lines[end]
		if l.blank {
			end++
			continue
		}
		if l.indent <= parentIndent {
			break
		}
		if minIndent < 0 || l.indent < minIndent {
			minIndent = l.indent
		}
		end++
	}
	if minIndent < 0 {
		p.pos = end
		return &Node{Kind: ScalarNode, Tag: "!!str", Style: style, Line: line, Column: col}, nil
	}

	var builder strings.Builder
	var previousBlank bool
	for i := start; i < end; i++ {
		l := p.lines[i]
		if l.blank {
			builder.WriteByte('\n')
			previousBlank = true
			continue
		}
		if i > start && (style != FoldedStyle || previousBlank) {
			builder.WriteByte('\n')
		} else if i > start && style == FoldedStyle && !previousBlank {
			builder.WriteByte(' ')
		}
		if l.indent >= minIndent {
			builder.WriteString(l.full[minIndent:])
		}
		previousBlank = false
	}
	value := builder.String()
	switch chomp {
	case '-':
		value = strings.TrimRight(value, "\n")
	case 0:
		value = strings.TrimRight(value, "\n")
		if value != "" || end > start {
			value += "\n"
		}
	}
	p.pos = end
	return &Node{Kind: ScalarNode, Tag: "!!str", Value: value, Style: style, Line: line, Column: col}, nil
}

func findMapColon(text string) int {
	quote := byte(0)
	escaped := false
	depth := 0
	for i := 0; i < len(text); i++ {
		c := text[i]
		if quote == '"' {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				quote = 0
			}
			continue
		}
		if quote == '\'' {
			if c == '\'' {
				if i+1 < len(text) && text[i+1] == '\'' {
					i++
				} else {
					quote = 0
				}
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '[', '{':
			depth++
		case ']', '}':
			if depth > 0 {
				depth--
			}
		case ':':
			if depth == 0 && (i+1 == len(text) || text[i+1] == ' ' || text[i+1] == '\t') {
				return i
			}
		}
	}
	return -1
}

func startsFlowOrAliasOrProperty(text string) bool {
	if text == "" {
		return false
	}
	switch text[0] {
	case '[', '{', '*', '&', '!':
		return true
	}
	return false
}

func splitProperties(text string, col int) (tag, anchor, rest string, restCol int, err error) {
	i := 0
	for {
		for i < len(text) && (text[i] == ' ' || text[i] == '\t') {
			i++
		}
		if i >= len(text) {
			break
		}
		if text[i] == '&' {
			start := i
			i++
			for i < len(text) && !isPropertyTerminator(text[i]) {
				i++
			}
			anchor = text[start+1 : i]
			if anchor == "" {
				return "", "", "", 0, fmt.Errorf("yaml: column %d: empty anchor name", col+start)
			}
			continue
		}
		if text[i] == '!' {
			start := i
			if strings.HasPrefix(text[i:], "!<") {
				end := strings.IndexByte(text[i:], '>')
				if end < 0 {
					return "", "", "", 0, fmt.Errorf("yaml: column %d: unterminated tag", col+start)
				}
				i += end + 1
			} else {
				i++
				for i < len(text) && !isPropertyTerminator(text[i]) {
					i++
				}
			}
			tag = text[start:i]
			continue
		}
		break
	}
	rest = strings.TrimSpace(text[i:])
	leading := 0
	for i+leading < len(text) && (text[i+leading] == ' ' || text[i+leading] == '\t') {
		leading++
	}
	restCol = col + i + leading
	return tag, anchor, rest, restCol, nil
}

func isPropertyTerminator(c byte) bool {
	switch c {
	case ' ', '\t', ',', '[', ']', '{', '}':
		return true
	}
	return false
}

func propertiesOnly(text string) bool {
	_, _, rest, _, err := splitProperties(text, 1)
	return err == nil && rest == ""
}

func parseQuoted(text string, line, col int) (string, Style, error) {
	value, style, length, err := parseQuotedPrefix(text, line)
	if err != nil {
		return "", 0, err
	}
	if strings.TrimSpace(text[length:]) != "" {
		return "", 0, fmt.Errorf("yaml: line %d: trailing content after quoted scalar", line)
	}
	return value, style, nil
}

func parseQuotedPrefix(text string, line int) (string, Style, int, error) {
	if len(text) == 0 {
		return "", 0, 0, nil
	}
	switch text[0] {
	case '\'':
		var builder strings.Builder
		for i := 1; i < len(text); i++ {
			if text[i] == '\'' {
				if i+1 < len(text) && text[i+1] == '\'' {
					builder.WriteByte('\'')
					i++
					continue
				}
				return builder.String(), SingleQuotedStyle, i + 1, nil
			}
			builder.WriteByte(text[i])
		}
		return "", 0, 0, fmt.Errorf("yaml: line %d: unterminated single-quoted scalar", line)
	case '"':
		var builder strings.Builder
		for i := 1; i < len(text); i++ {
			c := text[i]
			if c == '"' {
				return builder.String(), DoubleQuotedStyle, i + 1, nil
			}
			if c != '\\' {
				builder.WriteByte(c)
				continue
			}
			i++
			if i >= len(text) {
				return "", 0, 0, fmt.Errorf("yaml: line %d: unterminated escape", line)
			}
			switch text[i] {
			case '0':
				builder.WriteByte(0)
			case 'a':
				builder.WriteByte('\a')
			case 'b':
				builder.WriteByte('\b')
			case 't', '\t':
				builder.WriteByte('\t')
			case 'n':
				builder.WriteByte('\n')
			case 'v':
				builder.WriteByte('\v')
			case 'f':
				builder.WriteByte('\f')
			case 'r':
				builder.WriteByte('\r')
			case 'e':
				builder.WriteByte(0x1b)
			case ' ':
				builder.WriteByte(' ')
			case '"':
				builder.WriteByte('"')
			case '/':
				builder.WriteByte('/')
			case '\\':
				builder.WriteByte('\\')
			case 'N':
				builder.WriteRune('\u0085')
			case '_':
				builder.WriteRune('\u00a0')
			case 'L':
				builder.WriteRune('\u2028')
			case 'P':
				builder.WriteRune('\u2029')
			case 'x', 'u', 'U':
				n := 2
				if text[i] == 'u' {
					n = 4
				} else if text[i] == 'U' {
					n = 8
				}
				if i+n >= len(text) {
					return "", 0, 0, fmt.Errorf("yaml: line %d: invalid unicode escape", line)
				}
				v, err := strconv.ParseUint(text[i+1:i+1+n], 16, 32)
				if err != nil || (v > 0x10ffff && n == 8) {
					return "", 0, 0, fmt.Errorf("yaml: line %d: invalid unicode escape", line)
				}
				if !utf8.ValidRune(rune(v)) {
					return "", 0, 0, fmt.Errorf("yaml: line %d: invalid unicode escape", line)
				}
				builder.WriteRune(rune(v))
				i += n
			default:
				return "", 0, 0, fmt.Errorf("yaml: line %d: unknown escape %q", line, text[i])
			}
		}
		return "", 0, 0, fmt.Errorf("yaml: line %d: unterminated double-quoted scalar", line)
	default:
		return "", 0, 0, fmt.Errorf("yaml: line %d: expected a quoted scalar", line)
	}
}

type flowParser struct {
	p    *parser
	s    string
	i    int
	line int
	col  int
}

func (f *flowParser) skipSpace() {
	for f.i < len(f.s) && (f.s[f.i] == ' ' || f.s[f.i] == '\t') {
		f.i++
	}
}

func (f *flowParser) parseSequence(node *Node) error {
	if f.i >= len(f.s) || f.s[f.i] != '[' {
		return fmt.Errorf("yaml: line %d: expected '['", f.line)
	}
	f.i++
	for {
		f.skipSpace()
		if f.i >= len(f.s) {
			return fmt.Errorf("yaml: line %d: unterminated flow sequence", f.line)
		}
		if f.s[f.i] == ']' {
			f.i++
			return nil
		}
		if f.s[f.i] == ',' {
			f.i++
			continue
		}
		item, err := f.parseValue(":,]")
		if err != nil {
			return err
		}
		f.skipSpace()
		if f.i < len(f.s) && f.s[f.i] == ':' {
			f.i++
			f.skipSpace()
			var value *Node
			if f.i < len(f.s) && f.s[f.i] != ',' && f.s[f.i] != ']' {
				value, err = f.parseValue(",]")
				if err != nil {
					return err
				}
			}
			if value == nil {
				value = nullNode(f.line, f.col+f.i)
			}
			item = &Node{
				Kind:    MappingNode,
				Tag:     "!!map",
				Style:   FlowStyle,
				Content: []*Node{item, value},
				Line:    item.Line,
				Column:  item.Column,
			}
		}
		node.Content = append(node.Content, item)
		f.skipSpace()
		if f.i >= len(f.s) {
			return fmt.Errorf("yaml: line %d: unterminated flow sequence", f.line)
		}
		switch f.s[f.i] {
		case ',':
			f.i++
		case ']':
			f.i++
			return nil
		default:
			return fmt.Errorf("yaml: line %d: expected ',' or ']'", f.line)
		}
	}
}

func (f *flowParser) parseMapping(node *Node) error {
	if f.i >= len(f.s) || f.s[f.i] != '{' {
		return fmt.Errorf("yaml: line %d: expected '{'", f.line)
	}
	f.i++
	for {
		f.skipSpace()
		if f.i >= len(f.s) {
			return fmt.Errorf("yaml: line %d: unterminated flow mapping", f.line)
		}
		if f.s[f.i] == '}' {
			f.i++
			return nil
		}
		if f.s[f.i] == ',' {
			f.i++
			continue
		}
		key, err := f.parseValue(":,")
		if err != nil {
			return err
		}
		f.skipSpace()
		var value *Node
		if f.i < len(f.s) && f.s[f.i] == ':' {
			f.i++
			f.skipSpace()
			if f.i < len(f.s) && f.s[f.i] != ',' && f.s[f.i] != '}' {
				value, err = f.parseValue(",}")
				if err != nil {
					return err
				}
			}
		}
		if value == nil {
			value = nullNode(f.line, f.col+f.i)
		}
		node.Content = append(node.Content, key, value)
		f.skipSpace()
		if f.i >= len(f.s) {
			return fmt.Errorf("yaml: line %d: unterminated flow mapping", f.line)
		}
		switch f.s[f.i] {
		case ',':
			f.i++
		case '}':
			f.i++
			return nil
		default:
			return fmt.Errorf("yaml: line %d: expected ',' or '}'", f.line)
		}
	}
}

func (f *flowParser) parseValue(stops string) (*Node, error) {
	f.skipSpace()
	if f.i >= len(f.s) {
		return nullNode(f.line, f.col+f.i), nil
	}
	tag, anchor, rest, restCol, err := splitProperties(f.s[f.i:], f.col+f.i)
	if err != nil {
		return nil, err
	}
	if rest != "" {
		f.i = restCol - f.col
	} else {
		f.i = len(f.s)
	}
	if f.i >= len(f.s) {
		node := nullNode(f.line, restCol)
		if tag != "" {
			node.Tag = normalizeTag(tag)
			node.Style |= TaggedStyle
		}
		if anchor != "" {
			f.p.registerAnchor(node, anchor)
		}
		return node, nil
	}

	var node *Node
	switch f.s[f.i] {
	case '[':
		node = &Node{Kind: SequenceNode, Tag: "!!seq", Style: FlowStyle, Line: f.line, Column: f.col + f.i}
		if anchor != "" {
			f.p.registerAnchor(node, anchor)
			anchor = ""
		}
		if err := f.parseSequence(node); err != nil {
			return nil, err
		}
	case '{':
		node = &Node{Kind: MappingNode, Tag: "!!map", Style: FlowStyle, Line: f.line, Column: f.col + f.i}
		if anchor != "" {
			f.p.registerAnchor(node, anchor)
			anchor = ""
		}
		if err := f.parseMapping(node); err != nil {
			return nil, err
		}
	case '*':
		f.i++
		begin := f.i
		for f.i < len(f.s) && !isFlowTerminator(f.s[f.i]) && f.s[f.i] != ' ' && f.s[f.i] != '\t' {
			f.i++
		}
		name := f.s[begin:f.i]
		target, ok := f.p.anchors[name]
		if !ok {
			return nil, fmt.Errorf("yaml: line %d: unknown anchor %q referenced", f.line, name)
		}
		node = &Node{Kind: AliasNode, Value: name, Alias: target, Line: f.line, Column: f.col + begin - 1}
	case '\'', '"':
		value, style, length, err := parseQuotedPrefix(f.s[f.i:], f.line)
		if err != nil {
			return nil, err
		}
		node = &Node{Kind: ScalarNode, Tag: "!!str", Value: value, Style: style, Line: f.line, Column: f.col + f.i}
		f.i += length
	default:
		begin := f.i
		for f.i < len(f.s) {
			c := f.s[f.i]
			if strings.IndexByte(stops, c) >= 0 {
				if c == ':' {
					if f.i+1 >= len(f.s) || f.s[f.i+1] == ' ' || f.s[f.i+1] == '\t' ||
						strings.IndexByte(stops, f.s[f.i+1]) >= 0 {
						break
					}
				} else {
					break
				}
			}
			f.i++
		}
		value := strings.TrimSpace(f.s[begin:f.i])
		node = &Node{Kind: ScalarNode, Value: value, Tag: resolveScalarTag(value, 0), Line: f.line, Column: f.col + begin}
	}
	if tag != "" {
		node.Tag = normalizeTag(tag)
		node.Style |= TaggedStyle
	}
	if anchor != "" {
		f.p.registerAnchor(node, anchor)
	}
	return node, nil
}

func isFlowTerminator(c byte) bool {
	switch c {
	case ',', ']', '}', ':', '#':
		return true
	}
	return false
}
