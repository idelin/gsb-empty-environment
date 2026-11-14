package netsec

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Parse parses an executable GraphQL document. It intentionally does not
// require a schema: authorization planning is a syntax-level, fail-closed
// operation and all field decisions are delegated to a Policy.
func Parse(query string) (*Document, error) {
	tokens, err := lex(query)
	if err != nil {
		return nil, err
	}
	parser := &queryParser{tokens: tokens}
	return parser.parseDocument()
}

type tokenKind int

const (
	tokenEOF tokenKind = iota
	tokenName
	tokenInt
	tokenFloat
	tokenString
	tokenPunct
	tokenSpread
)

type token struct {
	kind     tokenKind
	text     string
	position Position
}

func lex(input string) ([]token, error) {
	var tokens []token
	line, column := 1, 1
	for i := 0; i < len(input); {
		r, size := utf8.DecodeRuneInString(input[i:])
		if r == utf8.RuneError && size == 1 {
			return nil, parseError(Position{Line: line, Column: column}, "invalid UTF-8")
		}
		switch r {
		case '\ufeff':
			i += size
			column++
		case ' ', '\t', ',':
			i += size
			column++
		case '\r', '\n':
			if r == '\r' && i+size < len(input) && input[i+size] == '\n' {
				i += size
			}
			i += size
			line++
			column = 1
		case '#':
			for i < len(input) {
				r2, size2 := utf8.DecodeRuneInString(input[i:])
				if r2 == '\r' || r2 == '\n' {
					break
				}
				i += size2
				column++
			}
		case '.':
			if strings.HasPrefix(input[i:], "...") {
				tokens = append(tokens, token{kind: tokenSpread, text: "...", position: Position{Line: line, Column: column}})
				i += 3
				column += 3
				continue
			}
			return nil, parseError(Position{Line: line, Column: column}, "expected ...")
		case '"':
			if strings.HasPrefix(input[i:], `"""`) {
				value, consumed, lines, columns, err := lexBlockString(input[i:], Position{Line: line, Column: column})
				if err != nil {
					return nil, err
				}
				tokens = append(tokens, token{kind: tokenString, text: value, position: Position{Line: line, Column: column}})
				i += consumed
				line += lines
				if lines > 0 {
					column = columns
				} else {
					column += columns
				}
				continue
			}
			value, consumed, lines, columns, err := lexString(input[i:], Position{Line: line, Column: column})
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, token{kind: tokenString, text: value, position: Position{Line: line, Column: column}})
			i += consumed
			line += lines
			if lines > 0 {
				column = columns
			} else {
				column += columns
			}
		default:
			if isNameStart(r) {
				start := i
				position := Position{Line: line, Column: column}
				for i < len(input) {
					r2, size2 := utf8.DecodeRuneInString(input[i:])
					if !isNameContinue(r2) {
						break
					}
					i += size2
					column++
				}
				tokens = append(tokens, token{kind: tokenName, text: input[start:i], position: position})
				continue
			}
			if r == '-' || (r >= '0' && r <= '9') {
				value, kind, consumed, err := lexNumber(input[i:], Position{Line: line, Column: column})
				if err != nil {
					return nil, err
				}
				tokens = append(tokens, token{kind: kind, text: value, position: Position{Line: line, Column: column}})
				i += consumed
				column += consumed
				continue
			}
			if strings.ContainsRune("!$&():=@[]{|}", r) {
				tokens = append(tokens, token{kind: tokenPunct, text: string(r), position: Position{Line: line, Column: column}})
				i += size
				column++
				continue
			}
			return nil, parseError(Position{Line: line, Column: column}, "unexpected character %q", r)
		}
	}
	tokens = append(tokens, token{kind: tokenEOF, position: Position{Line: line, Column: column}})
	return tokens, nil
}

func isNameStart(r rune) bool {
	return r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}

func isNameContinue(r rune) bool {
	return isNameStart(r) || (r >= '0' && r <= '9')
}

func lexNumber(input string, position Position) (string, tokenKind, int, error) {
	i := 0
	if input[i] == '-' {
		i++
	}
	if i >= len(input) {
		return "", tokenInt, 0, parseError(position, "invalid number")
	}
	if input[i] == '0' {
		i++
		if i < len(input) && input[i] >= '0' && input[i] <= '9' {
			return "", tokenInt, 0, parseError(position, "numbers may not have a leading zero")
		}
	} else if input[i] >= '1' && input[i] <= '9' {
		for i < len(input) && input[i] >= '0' && input[i] <= '9' {
			i++
		}
	} else {
		return "", tokenInt, 0, parseError(position, "invalid number")
	}
	kind := tokenInt
	if i < len(input) && input[i] == '.' {
		kind = tokenFloat
		i++
		if i >= len(input) || input[i] < '0' || input[i] > '9' {
			return "", tokenFloat, 0, parseError(position, "expected a digit after decimal point")
		}
		for i < len(input) && input[i] >= '0' && input[i] <= '9' {
			i++
		}
	}
	if i < len(input) && (input[i] == 'e' || input[i] == 'E') {
		kind = tokenFloat
		i++
		if i < len(input) && (input[i] == '+' || input[i] == '-') {
			i++
		}
		if i >= len(input) || input[i] < '0' || input[i] > '9' {
			return "", tokenFloat, 0, parseError(position, "expected a digit in exponent")
		}
		for i < len(input) && input[i] >= '0' && input[i] <= '9' {
			i++
		}
	}
	if i < len(input) {
		r, _ := utf8.DecodeRuneInString(input[i:])
		if isNameStart(r) || r == '.' {
			return "", tokenInt, 0, parseError(position, "invalid number")
		}
	}
	return input[:i], kind, i, nil
}

func lexString(input string, position Position) (string, int, int, int, error) {
	var builder strings.Builder
	line, column := 0, 1
	i := 1
	column++
	for i < len(input) {
		r, size := utf8.DecodeRuneInString(input[i:])
		if r == '\r' || r == '\n' {
			return "", 0, 0, 0, parseError(position, "line break in string literal")
		}
		if r < 0x20 || r == 0x7f {
			return "", 0, 0, 0, parseError(position, "control character in string literal")
		}
		if r == '"' {
			return builder.String(), i + size, line, column + 1, nil
		}
		if r != '\\' {
			builder.WriteRune(r)
			i += size
			column++
			continue
		}
		i += size
		column++
		if i >= len(input) {
			return "", 0, 0, 0, parseError(position, "unterminated escape sequence")
		}
		escape, size2 := utf8.DecodeRuneInString(input[i:])
		i += size2
		column++
		switch escape {
		case '"', '\\', '/':
			builder.WriteRune(escape)
		case 'b':
			builder.WriteByte('\b')
		case 'f':
			builder.WriteByte('\f')
		case 'n':
			builder.WriteByte('\n')
		case 'r':
			builder.WriteByte('\r')
		case 't':
			builder.WriteByte('\t')
		case 'u':
			code, consumed, err := readUnicodeEscape(input[i:], position)
			if err != nil {
				return "", 0, 0, 0, err
			}
			i += consumed
			column += consumed
			if utf16.IsSurrogate(code) {
				if code < 0xD800 || code > 0xDBFF || !strings.HasPrefix(input[i:], `\u`) {
					return "", 0, 0, 0, parseError(position, "invalid Unicode surrogate pair")
				}
				second, consumed2, err := readUnicodeEscape(input[i+2:], position)
				if err != nil {
					return "", 0, 0, 0, err
				}
				if second < 0xDC00 || second > 0xDFFF {
					return "", 0, 0, 0, parseError(position, "invalid Unicode surrogate pair")
				}
				i += 2 + consumed2
				column += 2 + consumed2
				builder.WriteRune(utf16.DecodeRune(code, second))
			} else {
				builder.WriteRune(code)
			}
		default:
			return "", 0, 0, 0, parseError(position, "invalid escape sequence \\%c", escape)
		}
	}
	return "", 0, 0, 0, parseError(position, "unterminated string literal")
}

func readUnicodeEscape(input string, position Position) (rune, int, error) {
	if len(input) < 4 {
		return 0, 0, parseError(position, "short Unicode escape")
	}
	value, err := strconv.ParseUint(input[:4], 16, 16)
	if err != nil {
		return 0, 0, parseError(position, "invalid Unicode escape")
	}
	return rune(value), 4, nil
}

func lexBlockString(input string, position Position) (string, int, int, int, error) {
	line, column := 0, 1
	i := 3
	column += 3
	for i < len(input) {
		if strings.HasPrefix(input[i:], `\"""`) {
			i += 4
			column += 4
			continue
		}
		if strings.HasPrefix(input[i:], `"""`) {
			raw := input[3:i]
			i += 3
			column += 3
			return blockStringValue(raw), i, line, column, nil
		}
		r, size := utf8.DecodeRuneInString(input[i:])
		if r == '\r' || r == '\n' {
			if r == '\r' && i+size < len(input) && input[i+size] == '\n' {
				i += size
			}
			i += size
			line++
			column = 1
			continue
		}
		i += size
		column++
	}
	return "", 0, 0, 0, parseError(position, "unterminated block string")
}

func blockStringValue(raw string) string {
	raw = strings.ReplaceAll(raw, `\"""`, `"""`)
	lines := strings.Split(raw, "\n")
	common := -1
	for i := 1; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")
		if line == "" {
			continue
		}
		indent := 0
		for indent < len(line) && (line[indent] == ' ' || line[indent] == '\t') {
			indent++
		}
		if common == -1 || indent < common {
			common = indent
		}
	}
	if common > 0 {
		for i := 1; i < len(lines); i++ {
			if len(lines[i]) >= common {
				lines[i] = lines[i][common:]
			}
		}
	}
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

type queryParser struct {
	tokens []token
	index  int
}

func (p *queryParser) current() token {
	if p.index >= len(p.tokens) {
		return token{kind: tokenEOF}
	}
	return p.tokens[p.index]
}

func (p *queryParser) peek(offset int) token {
	index := p.index + offset
	if index >= len(p.tokens) {
		return token{kind: tokenEOF}
	}
	return p.tokens[index]
}

func (p *queryParser) advance() token {
	result := p.current()
	if p.index < len(p.tokens) {
		p.index++
	}
	return result
}

func (p *queryParser) acceptPunct(value string) bool {
	if p.current().kind == tokenPunct && p.current().text == value {
		p.advance()
		return true
	}
	return false
}

func (p *queryParser) expectPunct(value string) (token, error) {
	current := p.current()
	if current.kind != tokenPunct || current.text != value {
		return token{}, parseError(current.position, "expected %q, found %q", value, current.text)
	}
	p.advance()
	return current, nil
}

func (p *queryParser) expectName() (token, error) {
	current := p.current()
	if current.kind != tokenName {
		return token{}, parseError(current.position, "expected a name, found %q", current.text)
	}
	p.advance()
	return current, nil
}

func (p *queryParser) parseDocument() (*Document, error) {
	document := &Document{Fragments: make(map[string]*FragmentDefinition)}
	for p.current().kind != tokenEOF {
		switch {
		case p.current().kind == tokenPunct && p.current().text == "{":
			operation, err := p.parseOperation("", Position{})
			if err != nil {
				return nil, err
			}
			document.Operations = append(document.Operations, operation)
		case p.current().kind == tokenName && isOperationType(p.current().text):
			operation, err := p.parseOperation("", p.current().position)
			if err != nil {
				return nil, err
			}
			document.Operations = append(document.Operations, operation)
		case p.current().kind == tokenName && p.current().text == "fragment":
			fragment, err := p.parseFragmentDefinition()
			if err != nil {
				return nil, err
			}
			if _, exists := document.Fragments[fragment.Name]; exists {
				return nil, parseError(fragment.Position, "fragment %q is defined more than once", fragment.Name)
			}
			document.Fragments[fragment.Name] = fragment
		default:
			return nil, parseError(p.current().position, "unexpected token %q", p.current().text)
		}
	}
	if len(document.Operations) == 0 {
		return nil, parseError(Position{Line: 1, Column: 1}, "document has no operation")
	}
	anonymous := 0
	names := make(map[string]struct{})
	for _, operation := range document.Operations {
		if operation.Name == "" {
			anonymous++
			continue
		}
		if _, exists := names[operation.Name]; exists {
			return nil, parseError(operation.Position, "operation %q is defined more than once", operation.Name)
		}
		names[operation.Name] = struct{}{}
	}
	if anonymous > 1 || (anonymous == 1 && len(document.Operations) > 1) {
		return nil, parseError(Position{Line: 1, Column: 1}, "anonymous operation must be the only operation")
	}
	return document, nil
}

func isOperationType(value string) bool {
	return value == string(OperationQuery) || value == string(OperationMutation) || value == string(OperationSubscription)
}

func (p *queryParser) parseOperation(precomputedName string, position Position) (*Operation, error) {
	operation := &Operation{Type: OperationQuery, Name: precomputedName, Position: position}
	if operation.Position.Line == 0 {
		operation.Position = p.current().position
	}
	if current := p.current(); current.kind == tokenName && isOperationType(current.text) {
		operation.Type = OperationType(current.text)
		if position.Line == 0 {
			operation.Position = current.position
		}
		p.advance()
		if p.current().kind == tokenName {
			operation.Name = p.advance().text
		}
		if p.current().kind == tokenPunct && p.current().text == "(" {
			definitions, err := p.parseVariableDefinitions()
			if err != nil {
				return nil, err
			}
			operation.VariableDefinitions = definitions
		}
	}
	directives, err := p.parseDirectives("operation")
	if err != nil {
		return nil, err
	}
	operation.Directives = directives
	if p.current().kind != tokenPunct || p.current().text != "{" {
		return nil, parseError(p.current().position, "expected a selection set")
	}
	selections, err := p.parseSelectionSet()
	if err != nil {
		return nil, err
	}
	operation.SelectionSet = selections
	return operation, nil
}

func (p *queryParser) parseVariableDefinitions() ([]*VariableDefinition, error) {
	if _, err := p.expectPunct("("); err != nil {
		return nil, err
	}
	var definitions []*VariableDefinition
	names := make(map[string]struct{})
	for !p.acceptPunct(")") {
		position := p.current().position
		if _, err := p.expectPunct("$"); err != nil {
			return nil, err
		}
		nameToken, err := p.expectName()
		if err != nil {
			return nil, err
		}
		if _, exists := names[nameToken.text]; exists {
			return nil, parseError(nameToken.position, "variable $%s is defined more than once", nameToken.text)
		}
		names[nameToken.text] = struct{}{}
		if _, err := p.expectPunct(":"); err != nil {
			return nil, err
		}
		typeReference, err := p.parseTypeReference()
		if err != nil {
			return nil, err
		}
		definition := &VariableDefinition{Name: nameToken.text, Type: typeReference, Position: position}
		if p.acceptPunct("=") {
			value, err := p.parseValue(false)
			if err != nil {
				return nil, err
			}
			definition.Default = value
			definition.HasDefault = true
		}
		directives, err := p.parseDirectives("variable definition")
		if err != nil {
			return nil, err
		}
		definition.Directives = directives
		definitions = append(definitions, definition)
		if p.current().kind == tokenEOF {
			return nil, parseError(p.current().position, "unterminated variable definitions")
		}
	}
	return definitions, nil
}

func (p *queryParser) parseTypeReference() (*TypeReference, error) {
	var reference *TypeReference
	if p.acceptPunct("[") {
		inner, err := p.parseTypeReference()
		if err != nil {
			return nil, err
		}
		if _, err := p.expectPunct("]"); err != nil {
			return nil, err
		}
		reference = &TypeReference{List: inner}
	} else {
		name, err := p.expectName()
		if err != nil {
			return nil, err
		}
		reference = &TypeReference{Named: name.text}
	}
	if p.acceptPunct("!") {
		reference.NonNull = true
	}
	return reference, nil
}

func (p *queryParser) parseSelectionSet() ([]Selection, error) {
	if _, err := p.expectPunct("{"); err != nil {
		return nil, err
	}
	var selections []Selection
	for !p.acceptPunct("}") {
		selection, err := p.parseSelection()
		if err != nil {
			return nil, err
		}
		selections = append(selections, selection)
		if p.current().kind == tokenEOF {
			return nil, parseError(p.current().position, "unterminated selection set")
		}
	}
	if len(selections) == 0 {
		return nil, parseError(p.current().position, "selection set may not be empty")
	}
	return selections, nil
}

func (p *queryParser) parseSelection() (Selection, error) {
	if p.current().kind == tokenSpread {
		position := p.advance().position
		if p.current().kind == tokenName && p.current().text == "on" {
			p.advance()
			typeCondition, err := p.expectName()
			if err != nil {
				return nil, err
			}
			directives, err := p.parseDirectives("inline fragment")
			if err != nil {
				return nil, err
			}
			selections, err := p.parseSelectionSet()
			if err != nil {
				return nil, err
			}
			return &InlineFragment{TypeCondition: typeCondition.text, Directives: directives, SelectionSet: selections, Position: position}, nil
		}
		if p.current().kind == tokenName {
			name := p.advance().text
			directives, err := p.parseDirectives("fragment spread")
			if err != nil {
				return nil, err
			}
			return &FragmentSpread{Name: name, Directives: directives, Position: position}, nil
		}
		directives, err := p.parseDirectives("inline fragment")
		if err != nil {
			return nil, err
		}
		selections, err := p.parseSelectionSet()
		if err != nil {
			return nil, err
		}
		return &InlineFragment{Directives: directives, SelectionSet: selections, Position: position}, nil
	}
	first, err := p.expectName()
	if err != nil {
		return nil, err
	}
	field := &Field{Name: first.text, Position: first.position}
	if p.acceptPunct(":") {
		field.Alias = first.text
		second, err := p.expectName()
		if err != nil {
			return nil, err
		}
		field.Name = second.text
	}
	if p.current().kind == tokenPunct && p.current().text == "(" {
		arguments, err := p.parseArguments()
		if err != nil {
			return nil, err
		}
		field.Arguments = arguments
	}
	directives, err := p.parseDirectives("field")
	if err != nil {
		return nil, err
	}
	field.Directives = directives
	if p.current().kind == tokenPunct && p.current().text == "{" {
		selections, err := p.parseSelectionSet()
		if err != nil {
			return nil, err
		}
		field.SelectionSet = selections
	}
	return field, nil
}

func (p *queryParser) parseArguments() ([]*Argument, error) {
	if _, err := p.expectPunct("("); err != nil {
		return nil, err
	}
	var arguments []*Argument
	names := make(map[string]struct{})
	for !p.acceptPunct(")") {
		name, err := p.expectName()
		if err != nil {
			return nil, err
		}
		if _, exists := names[name.text]; exists {
			return nil, parseError(name.position, "argument %q is specified more than once", name.text)
		}
		names[name.text] = struct{}{}
		if _, err := p.expectPunct(":"); err != nil {
			return nil, err
		}
		value, err := p.parseValue(true)
		if err != nil {
			return nil, err
		}
		arguments = append(arguments, &Argument{Name: name.text, Value: value, Position: name.position})
		if p.current().kind == tokenEOF {
			return nil, parseError(p.current().position, "unterminated arguments")
		}
	}
	return arguments, nil
}

func (p *queryParser) parseValue(allowVariable bool) (*Value, error) {
	current := p.current()
	switch current.kind {
	case tokenPunct:
		switch current.text {
		case "$":
			if !allowVariable {
				return nil, parseError(current.position, "variable references are not allowed in default values")
			}
			p.advance()
			name, err := p.expectName()
			if err != nil {
				return nil, err
			}
			return &Value{Kind: ValueVariable, Variable: name.text, Position: current.position}, nil
		case "[":
			p.advance()
			var list []*Value
			for !p.acceptPunct("]") {
				value, err := p.parseValue(allowVariable)
				if err != nil {
					return nil, err
				}
				list = append(list, value)
				if p.current().kind == tokenEOF {
					return nil, parseError(p.current().position, "unterminated list value")
				}
			}
			return &Value{Kind: ValueList, List: list, Position: current.position}, nil
		case "{":
			p.advance()
			var fields []*ObjectValueField
			names := make(map[string]struct{})
			for !p.acceptPunct("}") {
				name, err := p.expectName()
				if err != nil {
					return nil, err
				}
				if _, exists := names[name.text]; exists {
					return nil, parseError(name.position, "object field %q is specified more than once", name.text)
				}
				names[name.text] = struct{}{}
				if _, err := p.expectPunct(":"); err != nil {
					return nil, err
				}
				value, err := p.parseValue(allowVariable)
				if err != nil {
					return nil, err
				}
				fields = append(fields, &ObjectValueField{Name: name.text, Value: value, Position: name.position})
				if p.current().kind == tokenEOF {
					return nil, parseError(p.current().position, "unterminated object value")
				}
			}
			return &Value{Kind: ValueObject, Object: fields, Position: current.position}, nil
		}
	case tokenInt:
		p.advance()
		return &Value{Kind: ValueInt, Raw: current.text, Position: current.position}, nil
	case tokenFloat:
		p.advance()
		return &Value{Kind: ValueFloat, Raw: current.text, Position: current.position}, nil
	case tokenString:
		p.advance()
		return &Value{Kind: ValueString, Raw: current.text, Position: current.position}, nil
	case tokenName:
		p.advance()
		switch current.text {
		case "true":
			return &Value{Kind: ValueBoolean, Bool: true, Position: current.position}, nil
		case "false":
			return &Value{Kind: ValueBoolean, Bool: false, Position: current.position}, nil
		case "null":
			return &Value{Kind: ValueNull, Position: current.position}, nil
		default:
			return &Value{Kind: ValueEnum, Raw: current.text, Position: current.position}, nil
		}
	}
	return nil, parseError(current.position, "expected a value, found %q", current.text)
}

func (p *queryParser) parseDirectives(origin string) ([]*Directive, error) {
	var directives []*Directive
	names := make(map[string]struct{})
	for p.current().kind == tokenPunct && p.current().text == "@" {
		position := p.advance().position
		name, err := p.expectName()
		if err != nil {
			return nil, err
		}
		if _, exists := names[name.text]; exists {
			return nil, parseError(name.position, "directive @%s is specified more than once", name.text)
		}
		names[name.text] = struct{}{}
		var arguments []*Argument
		if p.current().kind == tokenPunct && p.current().text == "(" {
			arguments, err = p.parseArguments()
			if err != nil {
				return nil, err
			}
		}
		directives = append(directives, &Directive{Name: name.text, Arguments: arguments, Position: position, Origin: origin})
	}
	return directives, nil
}

func (p *queryParser) parseFragmentDefinition() (*FragmentDefinition, error) {
	position := p.advance().position
	name, err := p.expectName()
	if err != nil {
		return nil, err
	}
	if name.text == "on" {
		return nil, parseError(name.position, "fragment name may not be \"on\"")
	}
	onToken, err := p.expectName()
	if err != nil {
		return nil, err
	}
	if onToken.text != "on" {
		return nil, parseError(onToken.position, "expected \"on\" after fragment name")
	}
	typeCondition, err := p.expectName()
	if err != nil {
		return nil, err
	}
	directives, err := p.parseDirectives("fragment definition")
	if err != nil {
		return nil, err
	}
	selections, err := p.parseSelectionSet()
	if err != nil {
		return nil, err
	}
	return &FragmentDefinition{
		Name:          name.text,
		TypeCondition: typeCondition.text,
		Directives:    directives,
		SelectionSet:  selections,
		Position:      position,
	}, nil
}

type ParseError struct {
	Position Position
	Message  string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("netsec: GraphQL parse error at %d:%d: %s", e.Position.Line, e.Position.Column, e.Message)
}

func parseError(position Position, format string, arguments ...any) error {
	return &ParseError{Position: position, Message: fmt.Sprintf(format, arguments...)}
}
