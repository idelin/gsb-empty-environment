package vex

import (
	"fmt"
	"strings"
	"unicode"
)

// conditionEvaluator evaluates build-tag expressions against a configuration.
// It records each distinct evaluation so the certificate can carry the
// condition evidence used by extraction and reachability.
type conditionEvaluator struct {
	tags    map[string]bool
	results []ConditionResult
	seen    map[string]bool
}

func newConditionEvaluator(tags []string) *conditionEvaluator {
	active := make(map[string]bool, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			active[tag] = true
		}
	}
	return &conditionEvaluator{
		tags: active,
		seen: make(map[string]bool),
	}
}

func (c *conditionEvaluator) eval(owner, expression string) (bool, error) {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return true, nil
	}
	expression = strings.ReplaceAll(expression, ",", " && ")
	parser := conditionParser{input: expression, tags: c.tags}
	value, err := parser.parse()
	if err != nil {
		return false, fmt.Errorf("condition %q for %s: %w", expression, owner, err)
	}
	key := owner + "\x00" + expression
	if !c.seen[key] {
		c.results = append(c.results, ConditionResult{
			Owner:      owner,
			Expression: expression,
			Result:     value,
		})
		c.seen[key] = true
	}
	return value, nil
}

func (c *conditionEvaluator) evalAll(owner string, expressions []string) (bool, error) {
	for _, expression := range expressions {
		ok, err := c.eval(owner, expression)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

type conditionParser struct {
	input string
	pos   int
	tags  map[string]bool
}

func (p *conditionParser) parse() (bool, error) {
	value, err := p.parseOr()
	if err != nil {
		return false, err
	}
	p.skipSpace()
	if p.pos != len(p.input) {
		return false, fmt.Errorf("unexpected %q at byte %d", p.input[p.pos:], p.pos)
	}
	return value, nil
}

func (p *conditionParser) parseOr() (bool, error) {
	value, err := p.parseAnd()
	if err != nil {
		return false, err
	}
	for {
		p.skipSpace()
		if !p.consumeSymbol("||") && !p.consumeWord("or") {
			return value, nil
		}
		right, err := p.parseAnd()
		if err != nil {
			return false, err
		}
		value = value || right
	}
}

func (p *conditionParser) parseAnd() (bool, error) {
	value, err := p.parseUnary()
	if err != nil {
		return false, err
	}
	for {
		p.skipSpace()
		if !p.consumeSymbol("&&") && !p.consumeWord("and") {
			return value, nil
		}
		right, err := p.parseUnary()
		if err != nil {
			return false, err
		}
		value = value && right
	}
}

func (p *conditionParser) parseUnary() (bool, error) {
	p.skipSpace()
	if p.consumeSymbol("!") || p.consumeWord("not") {
		value, err := p.parseUnary()
		if err != nil {
			return false, err
		}
		return !value, nil
	}
	if p.consumeSymbol("(") {
		value, err := p.parseOr()
		if err != nil {
			return false, err
		}
		p.skipSpace()
		if !p.consumeSymbol(")") {
			return false, fmt.Errorf("missing closing parenthesis")
		}
		return value, nil
	}
	name := p.consumeName()
	if name == "" {
		return false, fmt.Errorf("expected build tag at byte %d", p.pos)
	}
	switch strings.ToLower(name) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return p.tags[name], nil
}

func (p *conditionParser) skipSpace() {
	for p.pos < len(p.input) {
		r := rune(p.input[p.pos])
		if !unicode.IsSpace(r) {
			return
		}
		p.pos++
	}
}

func (p *conditionParser) consumeSymbol(symbol string) bool {
	p.skipSpace()
	if strings.HasPrefix(p.input[p.pos:], symbol) {
		p.pos += len(symbol)
		return true
	}
	return false
}

func (p *conditionParser) consumeWord(word string) bool {
	p.skipSpace()
	remaining := p.input[p.pos:]
	if len(remaining) < len(word) || !strings.EqualFold(remaining[:len(word)], word) {
		return false
	}
	if len(remaining) > len(word) {
		r := rune(remaining[len(word)])
		if isNameRune(r) {
			return false
		}
	}
	p.pos += len(word)
	return true
}

func (p *conditionParser) consumeName() string {
	p.skipSpace()
	start := p.pos
	for p.pos < len(p.input) {
		r := rune(p.input[p.pos])
		if !isNameRune(r) {
			break
		}
		p.pos++
	}
	return p.input[start:p.pos]
}

func isNameRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) ||
		r == '_' || r == '.' || r == '-' || r == '+' || r == ':' || r == '/'
}
