package netsec

import (
	"fmt"
	"strconv"
)

// sfItemKind identifies the Bare Item type parsed from a Structured Fields
// value. Types irrelevant to the priority dictionary are still parsed so
// that malformed input is rejected instead of silently misread.
type sfItemKind int

const (
	sfInteger sfItemKind = iota
	sfDecimal
	sfBoolean
	sfString
	sfToken
	sfByteSequence
	sfInnerList
)

type sfItem struct {
	kind       sfItemKind
	integer    int64
	decimal    float64
	boolean    bool
	str        string
	parameters map[string]sfItem
}

type sfMember struct {
	item      sfItem
	innerList []sfItem
}

type sfParser struct {
	input string
	pos   int
}

// parseSFDictionary parses a Structured Fields Dictionary (RFC 8941,
// Section 4.2). Duplicate keys keep the last value; per RFC 8941 duplicate
// keys are technically a parse error, but this package intentionally applies
// last-one-wins as required for PRIORITY_UPDATE processing.
func parseSFDictionary(input string) (map[string]sfMember, error) {
	p := &sfParser{input: input}
	dictionary := make(map[string]sfMember)

	p.skipOWS()
	if p.atEnd() {
		return dictionary, nil
	}

	for {
		p.skipOWS()
		if p.atEnd() {
			break
		}

		key, err := p.parseKey()
		if err != nil {
			return nil, p.fieldError(err)
		}

		var member sfMember
		p.skipOWS()
		if p.atEnd() {
			member = sfMember{item: sfItem{kind: sfBoolean, boolean: true}}
		} else if p.peek() == '=' {
			p.pos++
			p.skipOWS()
			member, err = p.parseMember()
			if err != nil {
				return nil, p.fieldError(err)
			}
		} else {
			// A key without a value stands for the boolean item true.
			member = sfMember{item: sfItem{kind: sfBoolean, boolean: true}}
		}
		dictionary[key] = member

		p.skipOWS()
		if p.atEnd() {
			break
		}
		if p.peek() != ',' || p.pos+1 >= len(p.input) {
			return nil, p.fieldError(fmt.Errorf("expected comma between dictionary members"))
		}
		p.pos++
		p.skipOWS()
		if p.atEnd() {
			return nil, p.fieldError(fmt.Errorf("trailing comma in dictionary"))
		}
	}

	return dictionary, nil
}

func (p *sfParser) parseMember() (sfMember, error) {
	if p.atEnd() {
		return sfMember{}, fmt.Errorf("expected member value after '='")
	}
	if p.peek() == '(' {
		return p.parseInnerList()
	}
	item, err := p.parseItem()
	if err != nil {
		return sfMember{}, err
	}
	return sfMember{item: item}, nil
}

func (p *sfParser) parseItem() (sfItem, error) {
	item, err := p.parseBareItem()
	if err != nil {
		return sfItem{}, err
	}
	item.parameters, err = p.parseParameters()
	if err != nil {
		return sfItem{}, err
	}
	return item, nil
}

func (p *sfParser) parseBareItem() (sfItem, error) {
	if p.atEnd() {
		return sfItem{}, fmt.Errorf("expected bare item")
	}
	switch c := p.peek(); {
	case c == '-' || (c >= '0' && c <= '9'):
		return p.parseNumber()
	case c == '?':
		return p.parseBoolean()
	case c == '"':
		return p.parseString()
	case c == ':':
		return p.parseByteSequence()
	case isAlpha(c) || c == '*':
		return p.parseToken()
	default:
		return sfItem{}, fmt.Errorf("unexpected character %q in bare item", c)
	}
}

func (p *sfParser) parseNumber() (sfItem, error) {
	start := p.pos
	if p.peek() == '-' {
		p.pos++
		if p.atEnd() || !isDigit(p.peek()) {
			return sfItem{}, fmt.Errorf("malformed number")
		}
	}

	integerDigits := 0
	for !p.atEnd() && isDigit(p.peek()) {
		p.pos++
		integerDigits++
	}
	if integerDigits > 15 {
		return sfItem{}, fmt.Errorf("integer exceeds 15 digits")
	}

	if p.atEnd() || p.peek() != '.' {
		value, err := strconv.ParseInt(p.input[start:p.pos], 10, 64)
		if err != nil {
			return sfItem{}, fmt.Errorf("malformed integer: %v", err)
		}
		return sfItem{kind: sfInteger, integer: value}, nil
	}

	p.pos++ // consume '.'
	fractionDigits := 0
	for !p.atEnd() && isDigit(p.peek()) {
		p.pos++
		fractionDigits++
	}
	if fractionDigits == 0 {
		return sfItem{}, fmt.Errorf("decimal point must be followed by a digit")
	}
	if fractionDigits > 3 {
		return sfItem{}, fmt.Errorf("decimal fraction must have 1..3 digits")
	}
	if integerDigits > 12 {
		return sfItem{}, fmt.Errorf("decimal integer part exceeds 12 digits")
	}
	value, err := strconv.ParseFloat(p.input[start:p.pos], 64)
	if err != nil {
		return sfItem{}, fmt.Errorf("malformed decimal: %v", err)
	}
	return sfItem{kind: sfDecimal, decimal: value}, nil
}

func (p *sfParser) parseBoolean() (sfItem, error) {
	p.pos++ // consume '?'
	if p.atEnd() {
		return sfItem{}, fmt.Errorf("truncated boolean")
	}
	switch p.peek() {
	case '0':
		p.pos++
		return sfItem{kind: sfBoolean, boolean: false}, nil
	case '1':
		p.pos++
		return sfItem{kind: sfBoolean, boolean: true}, nil
	default:
		return sfItem{}, fmt.Errorf("boolean must be ?0 or ?1")
	}
}

func (p *sfParser) parseString() (sfItem, error) {
	p.pos++ // consume opening quote
	var value []byte
	for {
		if p.atEnd() {
			return sfItem{}, fmt.Errorf("unterminated string")
		}
		c := p.peek()
		if c == '"' {
			p.pos++
			return sfItem{kind: sfString, str: string(value)}, nil
		}
		if c < 0x20 || c >= 0x7f {
			return sfItem{}, fmt.Errorf("control character in string")
		}
		if c == '\\' {
			p.pos++
			if p.atEnd() {
				return sfItem{}, fmt.Errorf("unterminated escape")
			}
			escaped := p.peek()
			if escaped != '"' && escaped != '\\' {
				return sfItem{}, fmt.Errorf("invalid escaped character %q", escaped)
			}
			value = append(value, escaped)
			p.pos++
			continue
		}
		value = append(value, c)
		p.pos++
	}
}

func (p *sfParser) parseToken() (sfItem, error) {
	start := p.pos
	for !p.atEnd() {
		c := p.peek()
		if isTokenChar(c) {
			p.pos++
			continue
		}
		break
	}
	if p.pos == start {
		return sfItem{}, fmt.Errorf("empty token")
	}
	return sfItem{kind: sfToken, str: p.input[start:p.pos]}, nil
}

func (p *sfParser) parseByteSequence() (sfItem, error) {
	p.pos++ // consume opening colon
	start := p.pos
	for !p.atEnd() && p.peek() != ':' {
		c := p.peek()
		if !isBase64Char(c) {
			return sfItem{}, fmt.Errorf("invalid byte sequence character %q", c)
		}
		p.pos++
	}
	if p.atEnd() {
		return sfItem{}, fmt.Errorf("unterminated byte sequence")
	}
	value := p.input[start:p.pos]
	p.pos++ // consume closing colon
	return sfItem{kind: sfByteSequence, str: value}, nil
}

func (p *sfParser) parseParameters() (map[string]sfItem, error) {
	var parameters map[string]sfItem
	for !p.atEnd() && p.peek() == ';' {
		p.pos++
		p.skipRWS()
		key, err := p.parseKey()
		if err != nil {
			return nil, err
		}
		item := sfItem{kind: sfBoolean, boolean: true}
		if !p.atEnd() && p.peek() == '=' {
			p.pos++
			item, err = p.parseBareItem()
			if err != nil {
				return nil, err
			}
		}
		if parameters == nil {
			parameters = make(map[string]sfItem)
		}
		parameters[key] = item // duplicate parameter keys: last wins
	}
	return parameters, nil
}

func (p *sfParser) parseInnerList() (sfMember, error) {
	p.pos++ // consume '('
	var items []sfItem
	for {
		p.skipRWS()
		if p.atEnd() {
			return sfMember{}, fmt.Errorf("unterminated inner list")
		}
		if p.peek() == ')' {
			p.pos++
			parameters, err := p.parseParameters()
			if err != nil {
				return sfMember{}, err
			}
			return sfMember{
				item:      sfItem{kind: sfInnerList, parameters: parameters},
				innerList: items,
			}, nil
		}
		item, err := p.parseItem()
		if err != nil {
			return sfMember{}, err
		}
		items = append(items, item)
	}
}

func (p *sfParser) parseKey() (string, error) {
	start := p.pos
	if p.atEnd() {
		return "", fmt.Errorf("missing key")
	}
	first := p.peek()
	if !isLowerAlpha(first) && first != '*' {
		return "", fmt.Errorf("key must start with a lowercase letter or '*'")
	}
	p.pos++
	for !p.atEnd() && isKeyChar(p.peek()) {
		p.pos++
	}
	return p.input[start:p.pos], nil
}

func (p *sfParser) skipOWS() {
	for !p.atEnd() {
		switch c := p.peek(); c {
		case ' ', '\t':
			p.pos++
		default:
			return
		}
	}
}

func (p *sfParser) skipRWS() {
	for !p.atEnd() && p.peek() == ' ' {
		p.pos++
	}
}

func (p *sfParser) peek() byte {
	return p.input[p.pos]
}

func (p *sfParser) atEnd() bool {
	return p.pos >= len(p.input)
}

func (p *sfParser) fieldError(err error) error {
	return fmt.Errorf("%w: %v", ErrFieldValue, err)
}

func isDigit(c byte) bool      { return c >= '0' && c <= '9' }
func isAlpha(c byte) bool      { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isLowerAlpha(c byte) bool { return c >= 'a' && c <= 'z' }

func isKeyChar(c byte) bool {
	return isLowerAlpha(c) || isDigit(c) || c == '_' || c == '-' || c == '.' || c == '*'
}

func isTokenChar(c byte) bool {
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.',
		'^', '_', '`', '|', '~', ':', '/':
		return true
	}
	return isAlpha(c) || isDigit(c)
}

func isBase64Char(c byte) bool {
	return isAlpha(c) || isDigit(c) || c == '+' || c == '/' || c == '='
}
