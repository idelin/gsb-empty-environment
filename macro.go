package macroexp

// Macro is a parsed #def definition.
type Macro struct {
	Name   string
	Params []string
	Body   []Token
	DefPos Pos // position of the macro name in its #def
}

// parse splits a token stream into content tokens and macro definitions.
// Directive lines are consumed and never appear in the content.
func parse(toks []Token) (content []Token, macros map[string]*Macro, errs []error) {
	macros = map[string]*Macro{}
	i := 0
	for i < len(toks) {
		t := toks[i]
		if t.Kind == KindDirective {
			switch t.Text {
			case "#def":
				m, next, err := parseDef(toks, i)
				if err != nil {
					errs = append(errs, err)
				}
				if m != nil {
					macros[m.Name] = m
				}
				i = next
				continue
			case "#end":
				errs = append(errs, &ExpandError{Kind: ErrSyntax, Pos: t.Pos, Msg: "unmatched #end"})
				i++
				continue
			case "#local":
				errs = append(errs, &ExpandError{Kind: ErrSyntax, Pos: t.Pos, Msg: "#local outside macro body"})
				i++
				continue
			default:
				errs = append(errs, &ExpandError{Kind: ErrSyntax, Pos: t.Pos, Msg: "unknown directive " + t.Text})
				i++
				continue
			}
		}
		content = append(content, t)
		i++
	}
	return content, macros, errs
}

func skipSpaces(toks []Token, i int) int {
	for i < len(toks) && toks[i].Kind == KindSpace {
		i++
	}
	return i
}

func isPunct(t Token, text string) bool { return t.Kind == KindPunct && t.Text == text }

// parseDef parses one #def directive starting at toks[i] (the #def token).
// Forms:
//
//	#def NAME(a, b) body to end of line
//	#def NAME(a, b)\n body lines \n#end
//	#def NAME body            (object-like, zero params)
func parseDef(toks []Token, i int) (*Macro, int, error) {
	defTok := toks[i]
	i++
	i = skipSpaces(toks, i)
	if i >= len(toks) || toks[i].Kind != KindIdent {
		return nil, i, &ExpandError{Kind: ErrSyntax, Pos: defTok.Pos, Msg: "#def expects a macro name"}
	}
	name := toks[i]
	i++
	m := &Macro{Name: name.Text, DefPos: name.Pos}
	i = skipSpaces(toks, i)
	if i < len(toks) && isPunct(toks[i], "(") {
		i++
		for {
			i = skipSpaces(toks, i)
			if i < len(toks) && isPunct(toks[i], ")") {
				i++
				break
			}
			if i >= len(toks) || toks[i].Kind != KindIdent {
				return nil, i, &ExpandError{Kind: ErrSyntax, Pos: name.Pos, Msg: "malformed parameter list in #def " + m.Name}
			}
			m.Params = append(m.Params, toks[i].Text)
			i++
			i = skipSpaces(toks, i)
			if i < len(toks) && isPunct(toks[i], ",") {
				i++
				continue
			}
			if i < len(toks) && isPunct(toks[i], ")") {
				i++
				break
			}
			return nil, i, &ExpandError{Kind: ErrSyntax, Pos: name.Pos, Msg: "malformed parameter list in #def " + m.Name}
		}
		i = skipSpaces(toks, i)
	}
	if i >= len(toks) || toks[i].Kind == KindNewline {
		// Multi-line body terminated by a line whose first token is #end.
		if i < len(toks) {
			i++
		}
		atLineStart := true
		closed := false
		for i < len(toks) {
			t := toks[i]
			if atLineStart && t.Kind == KindDirective && t.Text == "#end" {
				closed = true
				i++
				for i < len(toks) && toks[i].Kind != KindNewline {
					i++
				}
				if i < len(toks) {
					i++
				}
				break
			}
			if t.Kind == KindNewline {
				atLineStart = true
			} else if t.Kind != KindSpace {
				atLineStart = false
			}
			m.Body = append(m.Body, t)
			i++
		}
		if !closed {
			return nil, i, &ExpandError{Kind: ErrSyntax, Pos: name.Pos, Msg: "missing #end for macro " + m.Name}
		}
	} else {
		for i < len(toks) && toks[i].Kind != KindNewline {
			m.Body = append(m.Body, toks[i])
			i++
		}
		if i < len(toks) {
			i++
		}
	}
	m.Body = trimTokens(m.Body)
	return m, i, nil
}

func trimTokens(toks []Token) []Token {
	for len(toks) > 0 && (toks[0].Kind == KindSpace || toks[0].Kind == KindNewline) {
		toks = toks[1:]
	}
	for len(toks) > 0 {
		last := toks[len(toks)-1]
		if last.Kind != KindSpace && last.Kind != KindNewline {
			break
		}
		toks = toks[:len(toks)-1]
	}
	return toks
}
