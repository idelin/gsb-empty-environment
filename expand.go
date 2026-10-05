package macroexp

import (
	"fmt"
	"strings"
)

// Frame is one step of a macro expansion chain.
type Frame struct {
	Macro string
	Def   Pos // where the macro was defined
	Call  Pos // where it was called
}

// OutToken is an output token carrying full provenance:
// where its text was defined, the innermost call site that emitted it,
// and the complete expansion chain (outermost first).
type OutToken struct {
	Text  string
	Def   Pos
	Call  Pos // zero if the token did not come from a macro expansion
	Chain []Frame
}

// ErrKind classifies expansion errors.
type ErrKind int

const (
	ErrSyntax ErrKind = iota
	ErrArity
	ErrCycle
	ErrBudget
)

// ExpandError describes a failure. For ErrCycle and ErrBudget, Chain holds
// the shortest call evidence known for the failure.
type ExpandError struct {
	Kind  ErrKind
	Pos   Pos
	Chain []Frame
	Msg   string
}

func (e *ExpandError) Error() string {
	if len(e.Chain) > 0 {
		names := make([]string, len(e.Chain))
		for i, f := range e.Chain {
			names[i] = f.Macro
		}
		return fmt.Sprintf("%s: %s (chain: %s)", e.Pos, e.Msg, strings.Join(names, " -> "))
	}
	return fmt.Sprintf("%s: %s", e.Pos, e.Msg)
}

// Options configures an Expander.
type Options struct {
	Budget int // max macro-expansion steps; <= 0 means DefaultBudget
}

const DefaultBudget = 10000

// Result is the expanded output.
type Result struct {
	Text   string
	Tokens []OutToken
}

// Expander expands macros over a token stream with provenance tracking,
// hygienic local renaming, memoized argument expansion, recursion-cycle
// detection and a step budget.
type Expander struct {
	macros    map[string]*Macro
	budget    int
	steps     int
	fresh     int
	errs      []error
	budgetErr int // index into errs of the recorded budget error, -1 if none
}

func New(opts Options) *Expander {
	budget := opts.Budget
	if budget <= 0 {
		budget = DefaultBudget
	}
	return &Expander{macros: map[string]*Macro{}, budget: budget, budgetErr: -1}
}

// Steps reports how many macro calls have been expanded. Because each
// argument is expanded at most once per call, repeated references to the
// same parameter do not inflate this count.
func (e *Expander) Steps() int { return e.steps }

// Expand processes src, returning the expanded text plus per-token
// provenance, and any errors encountered.
func (e *Expander) Expand(file, src string) (Result, []error) {
	e.steps = 0
	e.errs = nil
	e.budgetErr = -1
	toks := Lex(file, src)
	content, macros, perrs := parse(toks)
	for _, m := range macros {
		e.macros[m.Name] = m
	}
	e.errs = append(e.errs, perrs...)

	in := make([]xtoken, len(content))
	for i, t := range content {
		in[i] = xtoken{kind: t.Kind, text: t.Text, def: t.Pos}
	}
	out := e.expand(in, nil)

	var res Result
	var sb strings.Builder
	for _, t := range out {
		ot := OutToken{Text: t.text, Def: t.def, Chain: t.chain}
		if n := len(t.chain); n > 0 {
			ot.Call = t.chain[n-1].Call
		}
		res.Tokens = append(res.Tokens, ot)
		sb.WriteString(t.text)
	}
	res.Text = sb.String()
	return res, e.errs
}

// xtoken is an internal token carrying provenance through expansion.
// Sealed tokens come from an already-expanded argument and are spliced
// verbatim: they are never re-expanded, which is what guarantees each
// argument is expanded only once.
type xtoken struct {
	kind   Kind
	text   string
	def    Pos
	chain  []Frame
	sealed bool
}

// iframe is an expansion-context frame. body is true while the macro's
// own replacement list is being rescanned; recursion cycles are detected
// against body frames only, so finite nesting such as D(D(x)) is allowed
// while true recursion through a macro body is caught.
type iframe struct {
	frame Frame
	body  bool
}

func pushFrame(chain []iframe, f Frame, body bool) []iframe {
	out := make([]iframe, len(chain)+1)
	copy(out, chain)
	out[len(chain)] = iframe{frame: f, body: body}
	return out
}

func framesOf(chain []iframe) []Frame {
	if len(chain) == 0 {
		return nil
	}
	out := make([]Frame, len(chain))
	for i, f := range chain {
		out[i] = f.frame
	}
	return out
}

// lastBodyFrame returns the index of the most recent body frame for name,
// or -1. Cycle evidence starts there, which trims unrelated outer frames
// and yields the shortest cycle.
func lastBodyFrame(chain []iframe, name string) int {
	for i := len(chain) - 1; i >= 0; i-- {
		if chain[i].body && chain[i].frame.Macro == name {
			return i
		}
	}
	return -1
}

func (e *Expander) addErr(err error) { e.errs = append(e.errs, err) }

// errBudget records a budget-exhaustion error, keeping only the shortest
// call evidence seen.
func (e *Expander) errBudget(pos Pos, evidence []Frame) {
	if e.budgetErr >= 0 {
		prev := e.errs[e.budgetErr].(*ExpandError)
		if len(prev.Chain) <= len(evidence) {
			return
		}
		prev.Pos = pos
		prev.Chain = evidence
		return
	}
	e.errs = append(e.errs, &ExpandError{
		Kind:  ErrBudget,
		Pos:   pos,
		Chain: evidence,
		Msg:   "macro expansion budget exhausted",
	})
	e.budgetErr = len(e.errs) - 1
}

func (e *Expander) expand(toks []xtoken, chain []iframe) []xtoken {
	var out []xtoken
	i := 0
	for i < len(toks) {
		t := toks[i]
		if t.sealed || t.kind != KindIdent {
			if t.chain == nil {
				t.chain = framesOf(chain)
			}
			out = append(out, t)
			i++
			continue
		}
		m, ok := e.macros[t.text]
		if !ok {
			if t.chain == nil {
				t.chain = framesOf(chain)
			}
			out = append(out, t)
			i++
			continue
		}
		// Locate the call extent: NAME, optional spaces, optional (args).
		j := i + 1
		for j < len(toks) && toks[j].kind == KindSpace {
			j++
		}
		hasParens := j < len(toks) && toks[j].kind == KindPunct && toks[j].text == "("
		if len(m.Params) > 0 && !hasParens {
			// Function-like macro used without parens: not a call.
			out = append(out, toks[i:j]...)
			i = j
			continue
		}
		var args [][]xtoken
		end := i + 1
		if hasParens {
			var perr *ExpandError
			args, end, perr = parseArgs(toks, j, t.def)
			if perr != nil {
				e.addErr(perr)
				out = append(out, toks[i:j]...)
				i = j
				continue
			}
			if len(m.Params) == 0 && len(args) == 1 && len(args[0]) == 0 {
				args = nil // M() for a zero-param macro
			}
		}
		callPos := t.def
		frame := Frame{Macro: m.Name, Def: m.DefPos, Call: callPos}

		emitRaw := func() {
			out = append(out, toks[i:end]...)
			i = end
		}

		if len(args) != len(m.Params) {
			e.addErr(&ExpandError{
				Kind:  ErrArity,
				Pos:   callPos,
				Chain: framesOf(chain),
				Msg:   fmt.Sprintf("macro %s expects %d argument(s), got %d", m.Name, len(m.Params), len(args)),
			})
			emitRaw()
			continue
		}
		if idx := lastBodyFrame(chain, m.Name); idx >= 0 {
			// Recursion cycle: evidence runs from the innermost active
			// body of this macro to the re-entering call.
			evidence := append(framesOf(chain[idx:]), frame)
			e.addErr(&ExpandError{
				Kind:  ErrCycle,
				Pos:   callPos,
				Chain: evidence,
				Msg:   "recursive macro expansion of " + m.Name,
			})
			emitRaw()
			continue
		}
		if e.steps >= e.budget {
			e.errBudget(callPos, append(framesOf(chain), frame))
			emitRaw()
			continue
		}
		e.steps++

		// Arguments are pre-expanded once, in the enclosing context plus
		// this call's (non-body) frame, and sealed so the body rescan
		// never re-expands them. Results are memoized: a parameter
		// referenced twice reuses the same expansion.
		argChain := pushFrame(chain, frame, false)
		expandedArgs := make([][]xtoken, len(args))
		done := make([]bool, len(args))
		getArg := func(k int) []xtoken {
			if !done[k] {
				expanded := e.expand(args[k], argChain)
				for n := range expanded {
					expanded[n].sealed = true
				}
				expandedArgs[k] = expanded
				done[k] = true
			}
			return expandedArgs[k]
		}
		bodyChain := pushFrame(chain, frame, true)
		sub := e.substitute(m, getArg, framesOf(bodyChain))
		out = append(out, e.expand(sub, bodyChain)...)
		i = end
	}
	return out
}

// matchLocal reports whether body[i:] starts with "#local <ident>"
// (whitespace allowed between) and returns the local name and the index
// just past the identifier.
func matchLocal(body []Token, i int) (string, int, bool) {
	if i >= len(body) || body[i].Kind != KindDirective || body[i].Text != "#local" {
		return "", i, false
	}
	j := i + 1
	for j < len(body) && body[j].Kind == KindSpace {
		j++
	}
	if j >= len(body) || body[j].Kind != KindIdent {
		return "", i, false
	}
	return body[j].Text, j + 1, true
}

// substitute splices expanded arguments and hygienically renamed locals
// into the macro body. newChain is the provenance chain including this
// call's body frame.
func (e *Expander) substitute(m *Macro, getArg func(int) []xtoken, newChain []Frame) []xtoken {
	// Collect #local declarations and allocate one fresh name per local
	// per expansion, so locals never capture identifiers from arguments
	// or from enclosing expansions.
	locals := map[string]string{}
	for i := 0; i < len(m.Body); i++ {
		if name, next, ok := matchLocal(m.Body, i); ok {
			if _, exists := locals[name]; !exists {
				e.fresh++
				locals[name] = fmt.Sprintf("%s#%d", name, e.fresh)
			}
			i = next - 1
		}
	}
	paramIdx := map[string]int{}
	for i, p := range m.Params {
		paramIdx[p] = i
	}
	emit := func(k Kind, text string, def Pos) xtoken {
		return xtoken{kind: k, text: text, def: def, chain: newChain}
	}
	var out []xtoken
	for i := 0; i < len(m.Body); i++ {
		t := m.Body[i]
		if name, next, ok := matchLocal(m.Body, i); ok {
			out = append(out, emit(KindIdent, locals[name], t.Pos))
			i = next - 1
			continue
		}
		if t.Kind == KindIdent {
			if idx, ok := paramIdx[t.Text]; ok {
				out = append(out, getArg(idx)...)
				continue
			}
			if fresh, ok := locals[t.Text]; ok {
				out = append(out, emit(KindIdent, fresh, t.Pos))
				continue
			}
		}
		out = append(out, emit(t.Kind, t.Text, t.Pos))
	}
	return out
}

// parseArgs parses a parenthesized argument list starting at toks[open] == "(".
// It returns the arguments (trimmed), the index just past the closing ")",
// or an error located at the call site.
func parseArgs(toks []xtoken, open int, callPos Pos) ([][]xtoken, int, *ExpandError) {
	var args [][]xtoken
	var cur []xtoken
	depth := 1
	for i := open + 1; i < len(toks); i++ {
		t := toks[i]
		if t.kind == KindPunct {
			switch t.text {
			case "(", "[", "{":
				depth++
			case ")", "]", "}":
				depth--
				if depth == 0 {
					args = append(args, trimX(cur))
					return args, i + 1, nil
				}
			case ",":
				if depth == 1 {
					args = append(args, trimX(cur))
					cur = nil
					continue
				}
			}
		}
		cur = append(cur, t)
	}
	return nil, 0, &ExpandError{Kind: ErrSyntax, Pos: callPos, Msg: "unterminated argument list"}
}

func trimX(toks []xtoken) []xtoken {
	for len(toks) > 0 && (toks[0].kind == KindSpace || toks[0].kind == KindNewline) {
		toks = toks[1:]
	}
	for len(toks) > 0 {
		last := toks[len(toks)-1]
		if last.kind != KindSpace && last.kind != KindNewline {
			break
		}
		toks = toks[:len(toks)-1]
	}
	return toks
}
