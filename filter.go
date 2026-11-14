package netsec

import (
	"fmt"
	"strconv"
	"strings"
)

// EncodeFilter converts an RFC 4515 text filter to its LDAP BER encoding.
func EncodeFilter(filter string) ([]byte, error) {
	if strings.TrimSpace(filter) == "" {
		filter = "(objectClass=*)"
	}
	p := &filterParser{s: filter}
	out, err := p.parse()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.i != len(p.s) {
		return nil, fmt.Errorf("netsec: trailing data in filter at byte %d", p.i)
	}
	return out, nil
}

type filterParser struct {
	s string
	i int
}

func (p *filterParser) skipSpace() {
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ' ', '\t', '\r', '\n':
			p.i++
		default:
			return
		}
	}
}

func (p *filterParser) parse() ([]byte, error) {
	p.skipSpace()
	if p.i >= len(p.s) || p.s[p.i] != '(' {
		return nil, fmt.Errorf("netsec: filter must start with '('")
	}
	p.i++
	p.skipSpace()
	if p.i >= len(p.s) {
		return nil, fmt.Errorf("netsec: truncated filter")
	}

	var out []byte
	switch p.s[p.i] {
	case '&', '|':
		op := p.s[p.i]
		p.i++
		var children [][]byte
		for {
			p.skipSpace()
			if p.i >= len(p.s) {
				return nil, fmt.Errorf("netsec: truncated filter list")
			}
			if p.s[p.i] == ')' {
				break
			}
			child, err := p.parse()
			if err != nil {
				return nil, err
			}
			children = append(children, child)
		}
		if len(children) == 0 {
			return nil, fmt.Errorf("netsec: empty filter list")
		}
		tag := byte(0xa0)
		if op == '|' {
			tag = 0xa1
		}
		var content []byte
		for _, child := range children {
			content = append(content, child...)
		}
		out = berElement(tag, content)
	case '!':
		p.i++
		child, err := p.parse()
		if err != nil {
			return nil, err
		}
		out = berElement(0xa2, child)
	default:
		item, err := p.parseItem()
		if err != nil {
			return nil, err
		}
		out = item
	}

	p.skipSpace()
	if p.i >= len(p.s) || p.s[p.i] != ')' {
		return nil, fmt.Errorf("netsec: missing ')' in filter")
	}
	p.i++
	return out, nil
}

func (p *filterParser) parseItem() ([]byte, error) {
	start := p.i
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case '=', '>', '<', '~':
			goto operator
		case '(':
			return nil, fmt.Errorf("netsec: unexpected '(' inside filter item")
		}
		p.i++
	}
	return nil, fmt.Errorf("netsec: filter item has no operator")

operator:
	attr := strings.TrimSpace(p.s[start:p.i])
	if !validAttribute(attr) {
		return nil, fmt.Errorf("netsec: invalid attribute %q", attr)
	}
	op := p.s[p.i]
	p.i++
	if op == '>' || op == '<' || op == '~' {
		if p.i >= len(p.s) || p.s[p.i] != '=' {
			return nil, fmt.Errorf("netsec: invalid filter operator")
		}
		p.i++
	}

	parts, starred, err := p.parseValue()
	if err != nil {
		return nil, err
	}

	var tag byte
	switch {
	case op == '>':
		tag = 0xa5
	case op == '<':
		tag = 0xa6
	case op == '~':
		tag = 0xa8
	default:
		if !starred {
			tag = 0xa3
		}
	}

	if op == '=' && starred {
		allEmpty := true
		for _, part := range parts {
			if len(part) != 0 {
				allEmpty = false
				break
			}
		}
		if allEmpty {
			return berElement(0x87, []byte(attr)), nil
		}
		var subs []byte
		last := len(parts) - 1
		for i, part := range parts {
			if len(part) == 0 {
				continue
			}
			var subTag byte
			switch {
			case i == 0:
				subTag = 0x80
			case i == last:
				subTag = 0x82
			default:
				subTag = 0x81
			}
			subs = append(subs, berElement(subTag, part)...)
		}
		if len(subs) == 0 {
			return nil, fmt.Errorf("netsec: substring filter has no components")
		}
		content := append(berOctetString(attr), berElement(berTagSequence, subs)...)
		return berElement(0xa4, content), nil
	}

	value := parts[0]
	content := append(berOctetString(attr), berOctetValue(value)...)
	return berElement(tag, content), nil
}

// parseValue returns value components separated by unescaped '*'.
func (p *filterParser) parseValue() (parts [][]byte, starred bool, err error) {
	var current []byte
	for p.i < len(p.s) {
		ch := p.s[p.i]
		switch ch {
		case ')':
			parts = append(parts, current)
			return parts, starred, nil
		case '*':
			parts = append(parts, current)
			current = nil
			starred = true
			p.i++
		case '\\':
			p.i++
			if p.i >= len(p.s) {
				return nil, false, fmt.Errorf("netsec: trailing escape in filter")
			}
			if p.i+1 < len(p.s) && isHex(p.s[p.i]) && isHex(p.s[p.i+1]) {
				value, _ := strconv.ParseUint(p.s[p.i:p.i+2], 16, 8)
				current = append(current, byte(value))
				p.i += 2
				continue
			}
			current = append(current, p.s[p.i])
			p.i++
		default:
			current = append(current, ch)
			p.i++
		}
	}
	return nil, false, fmt.Errorf("netsec: unterminated filter value")
}

func validAttribute(attr string) bool {
	if attr == "" {
		return false
	}
	for i := 0; i < len(attr); i++ {
		c := attr[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '.' || c == ';' {
			continue
		}
		return false
	}
	return true
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
