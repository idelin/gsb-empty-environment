package depverify

// validVersion performs a lightweight structural check. Full semver
// validation is the go command's job; here we only reject empty values,
// whitespace and tokens that clearly cannot be a module version.
func validVersion(v string) bool {
	if v == "" {
		return false
	}
	for _, c := range v {
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			return false
		}
	}
	return true
}

type parser struct {
	tokens []token
	pos    int
}

func (p *parser) peek() *token {
	if p.pos >= len(p.tokens) {
		return nil
	}
	return &p.tokens[p.pos]
}

func (p *parser) next() *token {
	t := p.peek()
	if t != nil {
		p.pos++
	}
	return t
}

func (p *parser) skipNewlines() {
	for t := p.peek(); t != nil && (t.kind == tokenNewline || t.kind == tokenComment); t = p.peek() {
		p.pos++
	}
}

func (p *parser) parseError(t *token, msg string) error {
	line := 0
	if t != nil {
		line = t.line
	}
	return &ParseError{Line: line, Msg: msg}
}

// valueToken reads a path/version token, accepting bare words and strings.
func (p *parser) valueToken(what string) (string, int, error) {
	t := p.next()
	if t == nil {
		return "", 0, p.parseError(t, "expected "+what+", got end of file")
	}
	if t.kind != tokenWord && t.kind != tokenString {
		return "", t.line, p.parseError(t, "expected "+what+", got "+t.text)
	}
	if t.text == "" {
		return "", t.line, p.parseError(t, "empty "+what)
	}
	return t.text, t.line, nil
}

func (p *parser) expectArrow() error {
	eq := p.next()
	if eq == nil || eq.kind != tokenPunct || eq.text != "=" {
		got := "end of file"
		if eq != nil {
			got = eq.text
		}
		return p.parseError(eq, "expected =>, got "+got)
	}
	gt := p.next()
	if gt == nil || gt.kind != tokenPunct || gt.text != ">" {
		got := "end of file"
		if gt != nil {
			got = gt.text
		}
		return p.parseError(gt, "expected =>, got ="+got)
	}
	return nil
}

// statementEnd consumes the terminator of an in-block statement, leaving the
// closing ')' for the block loop. At top level a newline is required.
func (p *parser) statementEnd(block bool) error {
	t := p.next()
	if t == nil {
		return nil
	}
	if t.kind == tokenNewline {
		return nil
	}
	if block && t.kind == tokenPunct && t.text == ")" {
		p.pos--
		return nil
	}
	return p.parseError(t, "expected end of directive, got "+t.text)
}

func (p *parser) atBlockEnd(block bool) bool {
	t := p.peek()
	return t == nil || t.kind == tokenNewline ||
		(block && t.kind == tokenPunct && t.text == ")")
}

// ParseGoMod parses the require, replace and exclude directives of a go.mod
// file. Other directives (module, go, toolchain, retract, ...) are skipped.
func ParseGoMod(input string) (*GoModFile, error) {
	tokens, err := tokenize(input)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens}
	out := &GoModFile{}

	for {
		p.skipNewlines()
		t := p.next()
		if t == nil {
			break
		}
		if t.kind != tokenWord {
			return nil, p.parseError(t, "unexpected token "+t.text)
		}
		switch t.text {
		case "require":
			if err := p.parseRequire(out, t.line); err != nil {
				return nil, err
			}
		case "replace":
			if err := p.parseReplace(out, t.line); err != nil {
				return nil, err
			}
		case "exclude":
			if err := p.parseExclude(out, t.line); err != nil {
				return nil, err
			}
		default:
			if err := p.skipDirective(t.line); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// skipDirective discards an uninterpreted directive, including a bracketed
// block such as retract ( ... ).
func (p *parser) skipDirective(line int) error {
	for {
		t := p.peek()
		if t == nil || t.kind == tokenNewline {
			if t != nil {
				p.next()
			}
			return nil
		}
		p.next()
		if t.kind == tokenPunct && t.text == "(" {
			depth := 1
			for depth > 0 {
				cur := p.next()
				if cur == nil {
					return &ParseError{Line: line, Msg: "unterminated directive block"}
				}
				if cur.kind == tokenPunct && cur.text == "(" {
					depth++
				}
				if cur.kind == tokenPunct && cur.text == ")" {
					depth--
				}
			}
			return p.statementEnd(false)
		}
	}
}
