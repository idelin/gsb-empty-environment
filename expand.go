package macroexp

import (
	"fmt"
	"strconv"
)

// DefaultMaxSteps is the default expansion budget (number of macro
// expansions) used when Expand is called with a non-positive limit.
const DefaultMaxSteps = 10000

// Result is the outcome of Expand.
type Result struct {
	Tokens []Token
	Diags  []Diagnostic
	Counts map[string]int // how many times each macro was expanded
}

// Texts returns the token texts of the expansion output.
func (r Result) Texts() []string {
	out := make([]string, len(r.Tokens))
	for i, t := range r.Tokens {
		out[i] = t.Text
	}
	return out
}

func (r Result) String() string { return Render(r.Tokens) }

// Expand lexes src, collects #define directives and expands the remaining
// token stream. maxSteps caps the total number of macro expansions;
// a non-positive value selects DefaultMaxSteps.
func Expand(file, src string, maxSteps int) Result {
	if maxSteps <= 0 {
		maxSteps = DefaultMaxSteps
	}
	rest, macros, diags := parseDefines(Lex(file, src))
	e := &expander{
		maxSteps: maxSteps,
		macros:   macros,
		counts:   map[string]int{},
	}
	out := e.expandSeq(rest, nil, nil)
	return Result{Tokens: out, Diags: append(diags, e.diags...), Counts: e.counts}
}

type expander struct {
	maxSteps       int
	macros         map[string]*Macro
	steps          int
	hyg            int // hygiene counter: unique id per expansion
	counts         map[string]int
	diags          []Diagnostic
	budgetReported bool
}

// expandSeq expands all macro calls in tokens. stack holds the macros
// currently being expanded (for cycle detection); ctx is the chain
// recorded in diagnostics (it additionally tracks argument expansion).
func (e *expander) expandSeq(tokens []Token, stack []Frame, ctx []Frame) []Token {
	var out []Token
	i := 0
	for i < len(tokens) {
		t := tokens[i]
		if t.Kind == Ident && !t.Sealed {
			if m, ok := e.macros[t.Text]; ok {
				if inStack(stack, m.Name) {
					// First re-entry: the current chain is the shortest
					// evidence of the cycle. Leave the call unexpanded.
					e.addDiag(DiagRecursion, t.Pos, ctx,
						fmt.Sprintf("recursive expansion of macro %q", m.Name))
					out = append(out, t)
					i++
					continue
				}
				if e.steps >= e.maxSteps {
					// First exhaustion is the shortest evidence; report once.
					if !e.budgetReported {
						e.budgetReported = true
						e.addDiag(DiagBudget, t.Pos, ctx,
							fmt.Sprintf("expansion budget exhausted at macro %q", m.Name))
					}
					out = append(out, t)
					i++
					continue
				}
				if !m.HasParams {
					e.steps++
					e.counts[m.Name]++
					frame := Frame{Macro: m.Name, DefPos: m.DefPos, CallPos: t.Pos}
					body := e.instantiate(m, nil, frame, ctx)
					out = append(out, e.expandSeq(body, pushFrame(stack, frame), pushFrame(ctx, frame))...)
					i++
					continue
				}
				if i+1 < len(tokens) && tokens[i+1].Text == "(" {
					args, next, d := splitArgs(tokens, i+1, t.Pos)
					if d != nil {
						d.Chain = ctx
						e.diags = append(e.diags, *d)
						out = append(out, tokens[i:]...)
						break
					}
					if len(args) == 1 && len(args[0]) == 0 && len(m.Params) == 0 {
						args = nil // f() for a zero-parameter macro
					}
					if len(args) != len(m.Params) {
						e.addDiag(DiagArity, t.Pos, ctx, fmt.Sprintf(
							"macro %q expects %d argument(s), got %d",
							m.Name, len(m.Params), len(args)))
						out = append(out, tokens[i:next]...)
						i = next
						continue
					}
					e.steps++
					e.counts[m.Name]++
					frame := Frame{Macro: m.Name, DefPos: m.DefPos, CallPos: t.Pos}
					argFrame := frame
					argFrame.InArg = true
					expArgs := make([][]Token, len(args))
					for j, a := range args {
						// Each actual argument is expanded exactly once, in
						// the caller's context; the result is shared by every
						// reference to the parameter. Sealing prevents the
						// substituted copies from being expanded again.
						expArgs[j] = seal(e.expandSeq(a, stack, pushFrame(ctx, argFrame)))
					}
					body := e.instantiate(m, expArgs, frame, ctx)
					out = append(out, e.expandSeq(body, pushFrame(stack, frame), pushFrame(ctx, frame))...)
					i = next
					continue
				}
				// Function-like macro not followed by '(': plain identifier.
			}
		}
		out = append(out, t)
		i++
	}
	return out
}

// instantiate builds the replacement list for one expansion: parameters
// are substituted with the (already expanded) arguments, locals are
// hygienically renamed with a per-expansion unique suffix, and every body
// token is stamped with its provenance. ctx is the full expansion context
// (including argument-expansion frames) recorded in each token's Chain.
func (e *expander) instantiate(m *Macro, args [][]Token, frame Frame, ctx []Frame) []Token {
	e.hyg++
	suffix := "__" + strconv.Itoa(e.hyg)
	rename := make(map[string]string, len(m.Locals))
	for _, l := range m.Locals {
		rename[l] = l + suffix
	}
	chain := pushFrame(ctx, frame)
	var body []Token
	for _, bt := range m.Body {
		if bt.Kind == Ident {
			if pi, ok := m.paramIndex[bt.Text]; ok {
				body = append(body, args[pi]...)
				continue
			}
			if rn, ok := rename[bt.Text]; ok {
				bt.Text = rn
			}
		}
		bt.DefPos = bt.Pos
		bt.CallPos = frame.CallPos
		bt.Chain = chain
		body = append(body, bt)
	}
	return body
}

// splitArgs splits a parenthesized argument list. lp indexes the opening
// '('. It returns the argument token slices (possibly empty) and the index
// just past the matching ')'.
func splitArgs(tokens []Token, lp int, callPos Pos) ([][]Token, int, *Diagnostic) {
	var args [][]Token
	var cur []Token
	depth := 1
	j := lp + 1
	for j < len(tokens) {
		t := tokens[j]
		switch t.Text {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			depth--
			if depth == 0 {
				return append(args, cur), j + 1, nil
			}
		case ",":
			if depth == 1 {
				args = append(args, cur)
				cur = nil
				j++
				continue
			}
		}
		cur = append(cur, t)
		j++
	}
	return nil, j, &Diagnostic{Kind: DiagSyntax, Pos: callPos, Msg: "unterminated macro call"}
}

func pushFrame(stack []Frame, f Frame) []Frame {
	out := make([]Frame, len(stack)+1)
	copy(out, stack)
	out[len(stack)] = f
	return out
}

func inStack(stack []Frame, name string) bool {
	for _, f := range stack {
		if f.Macro == name {
			return true
		}
	}
	return false
}

func seal(ts []Token) []Token {
	for i := range ts {
		ts[i].Sealed = true
	}
	return ts
}

func (e *expander) addDiag(kind DiagKind, pos Pos, ctx []Frame, msg string) {
	e.diags = append(e.diags, Diagnostic{Kind: kind, Pos: pos, Msg: msg, Chain: ctx})
}
