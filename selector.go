package streamsel

import (
	"fmt"
	"strings"
)

// maxSelectorSteps bounds the selector chain length; the streaming matcher
// tracks progress in a uint64 bitmask.
const maxSelectorSteps = 64

type combinator int

const (
	combDescendant combinator = iota
	combChild
)

type attrSelector struct {
	name   string
	value  string
	hasVal bool
}

// simpleSelector is one compound selector: an optional tag ("" or "*" matches
// any tag) plus id, class and attribute conditions.
type simpleSelector struct {
	tag     string
	id      string
	classes []string
	attrs   []attrSelector
}

type selectorStep struct {
	comb combinator // combinator linking this step to the previous one
	sel  simpleSelector
}

type selector struct {
	steps []selectorStep
}

// parseSelector parses the supported CSS subset:
//
//	selector   := compound ( combinator compound )*
//	combinator := whitespace | whitespace? ">" whitespace?
//	compound   := [ tag | "*" ] ( "#id" | ".class" | "[attr]" | "[attr=value]" )*
func parseSelector(s string) (selector, error) {
	var steps []selectorStep
	pending := combDescendant
	i := 0
	for i < len(s) && isSpace(s[i]) {
		i++
	}
	for {
		sel, n, err := parseCompound(s[i:])
		if err != nil {
			return selector{}, err
		}
		if n == 0 {
			return selector{}, fmt.Errorf("streamsel: expected a selector at %q", s[i:])
		}
		i += n
		steps = append(steps, selectorStep{comb: pending, sel: sel})
		j := i
		for j < len(s) && isSpace(s[j]) {
			j++
		}
		if j >= len(s) {
			break
		}
		if s[j] == '>' {
			pending = combChild
			j++
			for j < len(s) && isSpace(s[j]) {
				j++
			}
		} else {
			pending = combDescendant
		}
		i = j
	}
	if len(steps) == 0 {
		return selector{}, fmt.Errorf("streamsel: empty selector")
	}
	if len(steps) > maxSelectorSteps {
		return selector{}, fmt.Errorf("streamsel: selector has %d steps, max is %d", len(steps), maxSelectorSteps)
	}
	return selector{steps: steps}, nil
}

func parseCompound(s string) (simpleSelector, int, error) {
	var sel simpleSelector
	i := 0
	if i < len(s) && (isASCIILetter(s[i]) || s[i] == '*') {
		if s[i] == '*' {
			i++
		} else {
			start := i
			for i < len(s) && isNameChar(s[i]) {
				i++
			}
			sel.tag = strings.ToLower(s[start:i])
		}
	}
	for i < len(s) {
		switch s[i] {
		case '#':
			i++
			start := i
			for i < len(s) && isNameChar(s[i]) {
				i++
			}
			if start == i {
				return sel, 0, fmt.Errorf("streamsel: empty id in selector %q", s)
			}
			sel.id = s[start:i]
		case '.':
			i++
			start := i
			for i < len(s) && isNameChar(s[i]) {
				i++
			}
			if start == i {
				return sel, 0, fmt.Errorf("streamsel: empty class in selector %q", s)
			}
			sel.classes = append(sel.classes, s[start:i])
		case '[':
			a, n, err := parseAttrSelector(s[i:])
			if err != nil {
				return sel, 0, err
			}
			i += n
			sel.attrs = append(sel.attrs, a)
		default:
			return sel, i, nil
		}
	}
	return sel, i, nil
}

func parseAttrSelector(s string) (attrSelector, int, error) {
	var a attrSelector
	i := 1 // consume '['
	for i < len(s) && isSpace(s[i]) {
		i++
	}
	start := i
	for i < len(s) && isNameChar(s[i]) {
		i++
	}
	a.name = strings.ToLower(s[start:i])
	if a.name == "" {
		return a, 0, fmt.Errorf("streamsel: empty attribute name in selector %q", s)
	}
	for i < len(s) && isSpace(s[i]) {
		i++
	}
	if i < len(s) && s[i] == '=' {
		i++
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		if i < len(s) && (s[i] == '"' || s[i] == '\'') {
			q := s[i]
			i++
			vs := i
			for i < len(s) && s[i] != q {
				i++
			}
			if i >= len(s) {
				return a, 0, fmt.Errorf("streamsel: unterminated attribute value in selector %q", s)
			}
			a.value = s[vs:i]
			i++
		} else {
			vs := i
			for i < len(s) && !isSpace(s[i]) && s[i] != ']' {
				i++
			}
			a.value = s[vs:i]
		}
		a.hasVal = true
		for i < len(s) && isSpace(s[i]) {
			i++
		}
	}
	if i >= len(s) || s[i] != ']' {
		return a, 0, fmt.Errorf("streamsel: missing ']' in selector %q", s)
	}
	return a, i + 1, nil
}

func (s simpleSelector) matches(tag string, attrs []attr) bool {
	if s.tag != "" && s.tag != tag {
		return false
	}
	id, _ := attrValue(attrs, "id")
	if s.id != "" && id != s.id {
		return false
	}
	classAttr, _ := attrValue(attrs, "class")
	for _, c := range s.classes {
		if !hasClass(classAttr, c) {
			return false
		}
	}
	for _, a := range s.attrs {
		v, ok := attrValue(attrs, a.name)
		if !ok || (a.hasVal && v != a.value) {
			return false
		}
	}
	return true
}

func hasClass(classAttr, class string) bool {
	for _, f := range strings.Fields(classAttr) {
		if f == class {
			return true
		}
	}
	return false
}

// matchesMap is the map-based variant used by the naive tree matcher.
func (s simpleSelector) matchesMap(tag string, attrs map[string]string) bool {
	if s.tag != "" && s.tag != tag {
		return false
	}
	if s.id != "" && attrs["id"] != s.id {
		return false
	}
	for _, c := range s.classes {
		if !hasClass(attrs["class"], c) {
			return false
		}
	}
	for _, a := range s.attrs {
		v, ok := attrs[a.name]
		if !ok || (a.hasVal && v != a.value) {
			return false
		}
	}
	return true
}
