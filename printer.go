package exprfmt

import "strings"

// MaxWidth is the line width the printer targets.
const MaxWidth = 80

// indentUnit is the number of spaces per indentation level.
const indentUnit = 2

// Precedence levels for non-binary nodes.
const (
	unaryPrec = 7
	callPrec  = 8
	atomPrec  = 9
)

// Format renders e as source code, breaking lines at operators and commas
// so that no line exceeds MaxWidth columns whenever the expression allows
// it. Re-parsing the output yields an AST identical to e.
func Format(e Expr) string { return printExpr(e, 0, 0) }

func precOf(e Expr) int {
	switch n := e.(type) {
	case *Binary:
		return binPrec[n.Op]
	case *Unary:
		return unaryPrec
	case *Call:
		return callPrec
	}
	return atomPrec
}

// leftCtx and rightCtx give the precedence context a binary operator imposes
// on its operands. The right operand uses prec+1 so that right-nested
// operators (including associative ones) keep their parentheses and the
// printed tree round-trips exactly.
func leftCtx(n *Binary) int { return binPrec[n.Op] }

func rightCtx(n *Binary) int { return binPrec[n.Op] + 1 }

func indent(level int) string { return strings.Repeat(" ", level*indentUnit) }

// flat renders e on a single line, adding parentheses wherever the
// surrounding precedence ctx requires them.
func flat(e Expr, ctx int) string {
	var s string
	switch n := e.(type) {
	case *Number:
		s = n.Value
	case *Str:
		s = n.Value
	case *Ident:
		s = n.Name
	case *Unary:
		s = n.Op + flat(n.X, unaryPrec)
	case *Binary:
		s = flat(n.X, leftCtx(n)) + " " + n.Op + " " + flat(n.Y, rightCtx(n))
	case *Call:
		var b strings.Builder
		b.WriteString(flat(n.Func, callPrec))
		b.WriteByte('(')
		for i, a := range n.Args {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(flat(a, 0))
		}
		b.WriteByte(')')
		s = b.String()
	}
	if precOf(e) < ctx {
		return "(" + s + ")"
	}
	return s
}

// printExpr renders e at indentation level ind under precedence context ctx.
// If the flat rendering fits in the remaining width it is used; otherwise
// the expression is broken across lines.
func printExpr(e Expr, ind, ctx int) string {
	if f := flat(e, ctx); ind*indentUnit+len(f) <= MaxWidth {
		return f
	}
	return breakExpr(e, ind, ctx)
}

// breakExpr renders e across multiple lines. Binary operators break after
// the operator with the right operand indented one level; calls break one
// argument per line. If e needs parentheses in context ctx, they are placed
// on their own lines around the broken body.
func breakExpr(e Expr, ind, ctx int) string {
	paren := precOf(e) < ctx
	inner := ind
	if paren {
		inner = ind + 1
	}
	var body string
	switch n := e.(type) {
	case *Binary:
		body = printExpr(n.X, inner, leftCtx(n)) + " " + n.Op + "\n" +
			indent(inner+1) + printExpr(n.Y, inner+1, rightCtx(n))
	case *Call:
		var b strings.Builder
		b.WriteString(printExpr(n.Func, inner, callPrec))
		b.WriteString("(\n")
		for _, a := range n.Args {
			b.WriteString(indent(inner + 1))
			b.WriteString(printExpr(a, inner+1, 0))
			b.WriteString(",\n")
		}
		b.WriteString(indent(inner))
		b.WriteString(")")
		body = b.String()
	case *Unary:
		body = n.Op + printExpr(n.X, inner, unaryPrec)
	default:
		body = flat(e, 0) // unbreakable atom
	}
	if paren {
		return "(\n" + indent(inner) + body + "\n" + indent(ind) + ")"
	}
	return body
}
