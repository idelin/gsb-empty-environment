package ldapfilter

import (
	"unicode/utf8"
)

// readAssertion reads a value that must not contain a wildcard: equality
// alternatives, ordering and approximate assertions, and extensible matches.
func (p *parser) readAssertion() (string, error) {
	value, err := p.readUntilClose(false)
	if err != nil {
		return "", err
	}
	if len(value.parts) != 1 {
		return "", p.errorf("wildcard '*' is not allowed in this assertion")
	}
	return value.parts[0], nil
}

// readSubstrings reads a substring right-hand side split on unescaped
// wildcards. The sawWildcard report lets the caller distinguish a plain
// equality value (no wildcard at all) from a substring assertion.
func (p *parser) readSubstrings() (initial string, any []string, final string, sawWildcard bool, err error) {
	value, err := p.readUntilClose(true)
	if err != nil {
		return "", nil, "", false, err
	}
	if !value.sawWildcard {
		return value.parts[0], nil, "", false, nil
	}
	parts := value.parts
	initial = parts[0]
	final = parts[len(parts)-1]
	if len(parts) > 2 {
		any = parts[1 : len(parts)-1]
	}
	return initial, any, final, true, nil
}

type decodedValue struct {
	parts       []string
	sawWildcard bool
}

// readUntilClose consumes an assertion value up to the closing ')'. When
// allowWildcards is true, unescaped '*' bytes split the value into literal
// pieces; otherwise they are rejected.
func (p *parser) readUntilClose(allowWildcards bool) (decodedValue, error) {
	var pieces []string
	var current []byte
	flush := func() error {
		if !utf8.Valid(current) {
			return p.errorf("invalid UTF-8 in assertion value")
		}
		pieces = append(pieces, string(current))
		current = nil
		return nil
	}

	sawWildcard := false
	for {
		if p.atEnd() {
			return decodedValue{}, p.errorf("unexpected end of input inside assertion value")
		}
		ch := p.input[p.pos]
		switch {
		case ch == ')':
			if err := flush(); err != nil {
				return decodedValue{}, err
			}
			return decodedValue{parts: pieces, sawWildcard: sawWildcard}, nil
		case ch == '(':
			return decodedValue{}, p.errorf("unexpected '(' inside assertion value at position %d", p.pos)
		case ch == '*':
			if !allowWildcards {
				return decodedValue{}, p.errorf("unescaped wildcard '*' at position %d", p.pos)
			}
			if err := flush(); err != nil {
				return decodedValue{}, err
			}
			sawWildcard = true
			p.pos++
		case ch == '\\':
			decoded, err := p.readEscape()
			if err != nil {
				return decodedValue{}, err
			}
			current = append(current, decoded)
		case ch == 0:
			return decodedValue{}, p.errorf("unescaped NUL byte at position %d", p.pos)
		default:
			current = append(current, ch)
			p.pos++
		}
	}
}

// readEscape consumes a backslash plus exactly two hexadecimal digits and
// returns the decoded byte.
func (p *parser) readEscape() (byte, error) {
	escape := p.pos
	p.pos++
	if p.pos+1 >= len(p.input) {
		return 0, p.errorf("incomplete escape sequence at position %d", escape)
	}
	high, okHigh := hexNibble(p.input[p.pos])
	low, okLow := hexNibble(p.input[p.pos+1])
	if !okHigh || !okLow {
		return 0, p.errorf("invalid escape sequence at position %d", escape)
	}
	p.pos += 2
	return high<<4 | low, nil
}

func hexNibble(ch byte) (byte, bool) {
	switch {
	case ch >= '0' && ch <= '9':
		return ch - '0', true
	case ch >= 'a' && ch <= 'f':
		return ch - 'a' + 10, true
	case ch >= 'A' && ch <= 'F':
		return ch - 'A' + 10, true
	default:
		return 0, false
	}
}
