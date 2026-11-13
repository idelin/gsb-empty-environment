package prioritystate

// Minimal RFC 8941 Structured Fields parser.
//
// Only the subset needed to parse the Priority Field Value (RFC 9218) is
// exposed, but the parser walks the full dictionary grammar so that malformed
// input from a peer is detected instead of silently accepted.

// sfType identifies the top-level Bare Item type of a dictionary member.
type sfType int

const (
	sfInvalid sfType = iota
	sfInteger
	sfDecimal
	sfBoolean
	sfString
	sfToken
	sfByteSeq
	sfInnerList
)

// sfItem is a parsed Bare Item (or inner list). The Int and Bool fields hold
// the typed values; other types are only tracked so malformed input fails.
type sfItem struct {
	typ   sfType
	intV  int64
	boolV bool
}

// sfMember is one member of a structured dictionary.
type sfMember struct {
	key  string
	item sfItem
}

type sfParser struct {
	b []byte
	i int
}

// parseDictionary parses an RFC 8941 Dictionary. Leading and trailing optional
// whitespace is tolerated; everything else must match the grammar exactly.
func parseDictionary(b []byte) ([]sfMember, bool) {
	p := sfParser{b: b}
	p.skipOWS()
	if p.atEnd() {
		// An empty dictionary is a valid dictionary with no members.
		return nil, true
	}

	var members []sfMember
	for {
		key, ok := p.parseKey()
		if !ok {
			return nil, false
		}
		var item sfItem
		if p.peek() == '=' {
			p.i++
			item, ok = p.parseBareItem()
			if !ok {
				return nil, false
			}
		} else {
			// Member with no value: boolean true (e.g. "i").
			item = sfItem{typ: sfBoolean, boolV: true}
		}
		// Parameters are part of the grammar; parse them but ignore the values.
		if !p.parseParameters() {
			return nil, false
		}
		members = append(members, sfMember{key: key, item: item})

		p.skipOWS()
		if p.atEnd() {
			return members, true
		}
		if p.peek() != ',' {
			return nil, false
		}
		p.i++
		p.skipOWS()
		if p.atEnd() { // dangling comma
			return nil, false
		}
	}
}

// sf-key = lcalpha *( key-char )
func (p *sfParser) parseKey() (string, bool) {
	if p.atEnd() || !isLCAlpha(p.peek()) {
		return "", false
	}
	start := p.i
	p.i++
	for !p.atEnd() && isKeyChar(p.peek()) {
		p.i++
	}
	return string(p.b[start:p.i]), true
}

// parameters = *( OWS ";" OWS [ sf-key ] [ sf-bare-item ] ... )
func (p *sfParser) parseParameters() bool {
	for p.peek() == ';' {
		p.i++
		p.skipOWS()
		if _, ok := p.parseKey(); !ok {
			return false
		}
		if p.peek() == '=' {
			p.i++
			if _, ok := p.parseBareItem(); !ok {
				return false
			}
		}
	}
	return true
}

func (p *sfParser) parseBareItem() (sfItem, bool) {
	if p.atEnd() {
		return sfItem{}, false
	}
	switch c := p.peek(); {
	case c == '-' || isDigit(c):
		return p.parseNumber()
	case c == '"':
		return p.parseString()
	case c == '?':
		return p.parseBoolean()
	case c == ':':
		return p.parseByteSeq()
	case c == '(':
		return p.parseInnerList()
	case isAlpha(c) || c == '*':
		return p.parseToken()
	default:
		return sfItem{}, false
	}
}

// sf-integer / sf-decimal, each limited to 15 significant digits.
func (p *sfParser) parseNumber() (sfItem, bool) {
	start := p.i
	negative := false
	if p.peek() == '-' {
		negative = true
		p.i++
	}

	intDigits := p.consumeDigits()
	if intDigits == 0 || intDigits > 15 {
		return sfItem{}, false
	}

	typ := sfInteger
	if p.peek() == '.' {
		typ = sfDecimal
		p.i++
		fracDigits := p.consumeDigits()
		if fracDigits == 0 || fracDigits > 3 {
			return sfItem{}, false
		}
	}

	// A number cannot be immediately followed by another value character.
	if !p.atEnd() && isExtendedTokenChar(p.peek()) {
		return sfItem{}, false
	}

	if typ == sfInteger {
		raw := p.b[start:p.i]
		v := int64(0)
		for _, c := range raw {
			if c == '-' {
				continue
			}
			v = v*10 + int64(c-'0')
		}
		if negative {
			v = -v
		}
		return sfItem{typ: sfInteger, intV: v}, true
	}
	// Decimal values are parsed structurally but their numeric value is
	// never needed by the priority dictionary.
	return sfItem{typ: sfDecimal}, true
}

func (p *sfParser) parseBoolean() (sfItem, bool) {
	p.i++ // consume '?'
	if p.atEnd() {
		return sfItem{}, false
	}
	switch p.peek() {
	case '1':
		p.i++
		return sfItem{typ: sfBoolean, boolV: true}, true
	case '0':
		p.i++
		return sfItem{typ: sfBoolean, boolV: false}, true
	default:
		return sfItem{}, false
	}
}

func (p *sfParser) parseString() (sfItem, bool) {
	p.i++ // opening quote
	for !p.atEnd() {
		c := p.peek()
		if c == '\\' {
			p.i++
			if p.atEnd() {
				return sfItem{}, false
			}
			esc := p.peek()
			if esc != '"' && esc != '\\' {
				return sfItem{}, false
			}
			p.i++
			continue
		}
		if c == '"' {
			p.i++
			return sfItem{typ: sfString}, true
		}
		if c < 0x20 || c >= 0x7f {
			return sfItem{}, false
		}
		p.i++
	}
	return sfItem{}, false
}

func (p *sfParser) parseToken() (sfItem, bool) {
	start := p.i
	p.i++ // first alpha or '*' already validated
	for !p.atEnd() && isTokenChar(p.peek()) {
		p.i++
	}
	if start == p.i {
		return sfItem{}, false
	}
	return sfItem{typ: sfToken}, true
}

func (p *sfParser) parseByteSeq() (sfItem, bool) {
	p.i++ // opening colon
	for !p.atEnd() && p.peek() != ':' {
		if !isBase64Char(p.peek()) {
			return sfItem{}, false
		}
		p.i++
	}
	if p.atEnd() { // no closing colon
		return sfItem{}, false
	}
	p.i++ // closing colon
	return sfItem{typ: sfByteSeq}, true
}

// inner-list = "(" *( OWS sf-item ) OWS ")" parameters
func (p *sfParser) parseInnerList() (sfItem, bool) {
	p.i++ // opening parenthesis
	for {
		p.skipOWS()
		if p.peek() == ')' {
			p.i++
			break
		}
		if _, ok := p.parseBareItem(); !ok {
			return sfItem{}, false
		}
		if !p.parseParameters() {
			return sfItem{}, false
		}
		if p.atEnd() {
			return sfItem{}, false
		}
	}
	if !p.parseParameters() {
		return sfItem{}, false
	}
	return sfItem{typ: sfInnerList}, true
}

func (p *sfParser) consumeDigits() int {
	n := 0
	for !p.atEnd() && isDigit(p.peek()) {
		n++
		p.i++
	}
	return n
}

func (p *sfParser) skipOWS() {
	for !p.atEnd() && (p.peek() == ' ' || p.peek() == '\t') {
		p.i++
	}
}

func (p *sfParser) peek() byte {
	if p.atEnd() {
		return 0
	}
	return p.b[p.i]
}

func (p *sfParser) atEnd() bool {
	return p.i >= len(p.b)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isLCAlpha(c byte) bool { return c >= 'a' && c <= 'z' }

// key-char = lcalpha / DIGIT / "_" / "-" / "." / "*"
func isKeyChar(c byte) bool {
	return isLCAlpha(c) || isDigit(c) || c == '_' || c == '-' || c == '.' || c == '*'
}

// tchar from RFC 7230 plus ":" and "/" (RFC 8941 sf-token).
func isTokenChar(c byte) bool {
	switch {
	case isAlpha(c), isDigit(c):
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~', ':', '/':
		return true
	}
	return false
}

// Characters that may continue an sf-number-less token and therefore reject a
// number that abuts them (e.g. "1abc").
func isExtendedTokenChar(c byte) bool {
	return isTokenChar(c) || c == '.'
}

func isBase64Char(c byte) bool {
	return isAlpha(c) || isDigit(c) || c == '+' || c == '/' || c == '='
}
