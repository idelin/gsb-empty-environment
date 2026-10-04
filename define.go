package macroexp

import "fmt"

// Macro is a parsed #define directive:
//
//	#define name(p1, p2) locals(l1, l2) body... #end
//	#define NAME body... #end          (object-like)
type Macro struct {
	Name       string
	Params     []string
	HasParams  bool // function-like
	Locals     []string
	Body       []Token
	DefPos     Pos
	paramIndex map[string]int
}

// parseDefines removes #define...#end blocks from the token stream and
// returns the remaining tokens plus the macro table.
func parseDefines(tokens []Token) ([]Token, map[string]*Macro, []Diagnostic) {
	macros := map[string]*Macro{}
	var rest []Token
	var diags []Diagnostic
	i := 0
	for i < len(tokens) {
		if isDirective(tokens, i, "define") {
			m, next, ds := parseDefine(tokens, i)
			diags = append(diags, ds...)
			if m != nil {
				if _, dup := macros[m.Name]; dup {
					diags = append(diags, Diagnostic{Kind: DiagSyntax, Pos: m.DefPos,
						Msg: fmt.Sprintf("macro %q redefined", m.Name)})
				}
				macros[m.Name] = m
			}
			i = next
			continue
		}
		rest = append(rest, tokens[i])
		i++
	}
	return rest, macros, diags
}

func isDirective(tokens []Token, i int, name string) bool {
	return i+1 < len(tokens) && tokens[i].Kind == Hash &&
		tokens[i+1].Kind == Ident && tokens[i+1].Text == name
}

func parseDefine(tokens []Token, start int) (*Macro, int, []Diagnostic) {
	var diags []Diagnostic
	j := start + 2
	if j >= len(tokens) || tokens[j].Kind != Ident {
		diags = append(diags, Diagnostic{Kind: DiagSyntax, Pos: tokens[start].Pos,
			Msg: "#define expects a macro name"})
		return nil, j, diags
	}
	m := &Macro{Name: tokens[j].Text, DefPos: tokens[j].Pos, paramIndex: map[string]int{}}
	j++
	if j < len(tokens) && tokens[j].Text == "(" {
		names, next, d := parseIdentList(tokens, j)
		if d != nil {
			diags = append(diags, *d)
		}
		m.Params = names
		m.HasParams = true
		j = next
	}
	if j < len(tokens) && tokens[j].Kind == Ident && tokens[j].Text == "locals" {
		if j+1 < len(tokens) && tokens[j+1].Text == "(" {
			names, next, d := parseIdentList(tokens, j+1)
			if d != nil {
				diags = append(diags, *d)
			}
			m.Locals = names
			j = next
		} else {
			diags = append(diags, Diagnostic{Kind: DiagSyntax, Pos: tokens[j].Pos,
				Msg: "locals expects a parenthesized name list"})
		}
	}
	for i, p := range m.Params {
		m.paramIndex[p] = i
	}
	// An optional '=' separates the header from the body.
	if j < len(tokens) && tokens[j].Text == "=" {
		j++
	}
	for j < len(tokens) {
		if isDirective(tokens, j, "end") {
			return m, j + 2, diags
		}
		m.Body = append(m.Body, tokens[j])
		j++
	}
	diags = append(diags, Diagnostic{Kind: DiagSyntax, Pos: m.DefPos,
		Msg: fmt.Sprintf("unterminated #define of %q: missing #end", m.Name)})
	return m, j, diags
}

// parseIdentList parses "a, b, c)" starting at the opening paren tokens[lp].
func parseIdentList(tokens []Token, lp int) ([]string, int, *Diagnostic) {
	var names []string
	j := lp + 1
	for j < len(tokens) {
		t := tokens[j]
		if t.Text == ")" {
			return names, j + 1, nil
		}
		if t.Kind == Ident {
			names = append(names, t.Text)
			j++
			if j < len(tokens) && tokens[j].Text == "," {
				j++
			}
			continue
		}
		return names, j, &Diagnostic{Kind: DiagSyntax, Pos: t.Pos,
			Msg: "expected identifier or ) in name list"}
	}
	return names, j, &Diagnostic{Kind: DiagSyntax, Pos: tokens[lp].Pos,
		Msg: "unterminated name list"}
}
