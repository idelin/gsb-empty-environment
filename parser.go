package ldapfilter

import (
	"fmt"
)

const eof byte = 0xFF

// Parse decodes an RFC 4511 search filter string into a syntax tree.
//
// It rejects empty AND/OR sets, unclosed parentheses, trailing characters and
// malformed RFC 4515 escapes. Escaped metacharacters are decoded back to their
// literal byte values, so attacker-controlled input is carried as data and
// can never be interpreted as filter structure.
func Parse(input string) (Filter, error) {
	parser := &parser{input: input}
	filter, err := parser.parseFilter()
	if err != nil {
		return nil, err
	}
	if !parser.atEnd() {
		return nil, fmt.Errorf("ldapfilter: trailing data at position %d", parser.pos)
	}
	return filter, nil
}

type parser struct {
	input string
	pos   int
}

func (p *parser) errorf(format string, args ...any) error {
	return fmt.Errorf("ldapfilter: "+format, args...)
}

func (p *parser) atEnd() bool {
	return p.pos >= len(p.input)
}

func (p *parser) peek() byte {
	if p.atEnd() {
		return eof
	}
	return p.input[p.pos]
}

func (p *parser) parseFilter() (Filter, error) {
	if p.peek() != '(' {
		if p.atEnd() {
			return nil, fmt.Errorf("ldapfilter: unexpected end of input, expected '('")
		}
		return nil, fmt.Errorf("ldapfilter: expected '(' at position %d", p.pos)
	}
	p.pos++

	var filter Filter
	var err error
	switch p.peek() {
	case '&':
		filter, err = p.parseSet('&')
	case '|':
		filter, err = p.parseSet('|')
	case '!':
		filter, err = p.parseNot()
	default:
		filter, err = p.parseItem()
	}
	if err != nil {
		return nil, err
	}
	return filter, nil
}

func (p *parser) parseSet(operator byte) (Filter, error) {
	open := p.pos
	p.pos++
	var children []Filter
	for p.peek() == '(' {
		child, err := p.parseFilter()
		if err != nil {
			return nil, err
		}
		children = append(children, child)
	}
	if p.peek() != ')' {
		return nil, p.errorf("unclosed %q filter opened at position %d", operator, open)
	}
	if len(children) == 0 {
		return nil, p.errorf("empty %q filter set at position %d", operator, open)
	}
	p.pos++
	if operator == '&' {
		return &AndFilter{Children: children}, nil
	}
	return &OrFilter{Children: children}, nil
}

func (p *parser) parseNot() (Filter, error) {
	open := p.pos
	p.pos++
	if p.peek() != '(' {
		return nil, p.errorf("unclosed '!' filter opened at position %d", open)
	}
	child, err := p.parseFilter()
	if err != nil {
		return nil, err
	}
	if p.peek() != ')' {
		return nil, p.errorf("unclosed '!' filter opened at position %d", open)
	}
	p.pos++
	return &NotFilter{Child: child}, nil
}

// parseItem parses an item filter once its opening '(' has been consumed.
func (p *parser) parseItem() (Filter, error) {
	start := p.pos
	attribute, operator, err := p.scanHead()
	if err != nil {
		return nil, err
	}
	if attribute == "" && operator != ':' {
		if p.atEnd() {
			return nil, p.errorf("unexpected end of input in filter opened at position %d", start)
		}
		return nil, p.errorf("missing attribute at position %d", start)
	}

	switch {
	case operator == '~':
		if err := validateAttribute(attribute); err != nil {
			return nil, err
		}
		value, err := p.readAssertion()
		if err != nil {
			return nil, err
		}
		if err := p.closeItem(start); err != nil {
			return nil, err
		}
		return &ApproxFilter{Attribute: attribute, Value: value}, nil

	case operator == '>':
		if err := validateAttribute(attribute); err != nil {
			return nil, err
		}
		value, err := p.readAssertion()
		if err != nil {
			return nil, err
		}
		if err := p.closeItem(start); err != nil {
			return nil, err
		}
		return &GreaterOrEqualFilter{Attribute: attribute, Value: value}, nil

	case operator == '<':
		if err := validateAttribute(attribute); err != nil {
			return nil, err
		}
		value, err := p.readAssertion()
		if err != nil {
			return nil, err
		}
		if err := p.closeItem(start); err != nil {
			return nil, err
		}
		return &LessOrEqualFilter{Attribute: attribute, Value: value}, nil

	case operator == '=':
		return p.parseEqualityLike(attribute, start)

	case operator == ':':
		return p.parseExtensible(attribute, start)

	default:
		return nil, p.errorf("missing assertion operator at position %d", start)
	}
}

func (p *parser) parseEqualityLike(attribute string, start int) (Filter, error) {
	if err := validateAttribute(attribute); err != nil {
		return nil, err
	}

	if p.peek() == '*' && p.pos+1 < len(p.input) && p.input[p.pos+1] == ')' {
		p.pos += 2
		return &PresentFilter{Attribute: attribute}, nil
	}

	initial, pieces, final, sawWildcard, err := p.readSubstrings()
	if err != nil {
		return nil, err
	}
	if err := p.closeItem(start); err != nil {
		return nil, err
	}
	if !sawWildcard {
		return &EqualityFilter{Attribute: attribute, Value: initial}, nil
	}
	return &SubstringsFilter{
		Attribute: attribute,
		Initial:   initial,
		Any:       pieces,
		Final:     final,
	}, nil
}

func (p *parser) parseExtensible(attribute string, start int) (Filter, error) {
	dnAttributes := false
	var matchingRule string

	for p.peek() == ':' {
		optionStart := p.pos
		p.pos++
		option := p.scanOption()
		switch {
		case option == "dn":
			if dnAttributes {
				return nil, p.errorf("duplicate :dn option at position %d", optionStart)
			}
			dnAttributes = true
		case option == "":
			// A trailing colon directly before '=' closes the option list, as
			// in (cn:dn:=x). A double colon with nothing between them is
			// invalid syntax.
			if p.peek() != '=' {
				return nil, p.errorf("empty extensible matching rule at position %d", optionStart)
			}
		default:
			if matchingRule != "" {
				return nil, p.errorf("multiple matching rules at position %d", optionStart)
			}
			if err := validateOID(option); err != nil {
				return nil, err
			}
			matchingRule = option
		}
	}

	if p.peek() != '=' {
		return nil, p.errorf("missing '=' in extensible match opened at position %d", start)
	}
	p.pos++

	if attribute != "" {
		if err := validateAttribute(attribute); err != nil {
			return nil, err
		}
	}
	if attribute == "" && matchingRule == "" {
		return nil, p.errorf("extensible match requires an attribute or a matching rule at position %d", start)
	}

	value, err := p.readAssertion()
	if err != nil {
		return nil, err
	}
	if err := p.closeItem(start); err != nil {
		return nil, err
	}
	return &ExtensibleFilter{
		Attribute:    attribute,
		MatchingRule: matchingRule,
		DNAttributes: dnAttributes,
		Value:        value,
	}, nil
}

// scanHead reads the attribute description and the assertion operator. The
// returned operator is one of '=', '~', '>', '<' or ':'.
func (p *parser) scanHead() (string, byte, error) {
	from := p.pos
	for !p.atEnd() {
		ch := p.input[p.pos]
		switch ch {
		case '=':
			p.pos++
			return p.input[from : p.pos-1], '=', nil
		case '~', '<', '>':
			operator := ch
			p.pos++
			if p.peek() != '=' {
				return "", 0, p.errorf("expected '=' after %q at position %d", operator, p.pos-1)
			}
			p.pos++
			return p.input[from : p.pos-2], operator, nil
		case ':':
			return p.input[from:p.pos], ':', nil
		case '(':
			return "", 0, p.errorf("unexpected '(' at position %d", p.pos)
		}
		p.pos++
	}
	return "", 0, p.errorf("missing assertion operator at position %d", from)
}

// scanOption reads an extensible-match option token up to the next ':' or '='.
func (p *parser) scanOption() string {
	from := p.pos
	for !p.atEnd() {
		switch p.input[p.pos] {
		case ':', '=':
			return p.input[from:p.pos]
		}
		p.pos++
	}
	return p.input[from:]
}

func (p *parser) closeItem(start int) error {
	if p.peek() != ')' {
		if p.atEnd() {
			return p.errorf("unclosed filter opened at position %d", start)
		}
		return p.errorf("expected ')' at position %d", p.pos)
	}
	p.pos++
	return nil
}
