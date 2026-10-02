package pretty

import (
	"bytes"
	"strconv"
	"strings"
)

// DefaultWidth is the default maximum line width in columns.
const DefaultWidth = 80

// indentUnit is one level of indentation.
const indentUnit = "    "

// Print formats an AST using the default 80-column width limit.
func Print(e Expr) string { return PrintWidth(e, DefaultWidth) }

// PrintWidth formats an AST so that no output line exceeds width columns
// whenever a legal break point (a binary operator or a comma) is available.
// Re-parsing the output yields an AST structurally identical to e.
func PrintWidth(e Expr, width int) string {
	p := &printer{width: width}
	p.expr(e, lowestPrec)
	return p.buf.String()
}

type printer struct {
	buf    bytes.Buffer
	width  int
	indent int // current continuation indent level
	col    int // current column
}

func (p *printer) write(s string) {
	p.buf.WriteString(s)
	p.col += len(s)
}

// newline starts a new line indented to the given level.
func (p *printer) newline(level int) {
	p.buf.WriteByte('\n')
	for i := 0; i < level; i++ {
		p.buf.WriteString(indentUnit)
	}
	p.col = level * len(indentUnit)
}

// expr prints e at the current position. If the flat rendering of e fits
// within the width limit it is emitted as-is; otherwise e is broken across
// multiple lines at legal operators or commas.
func (p *printer) expr(e Expr, prec int) {
	f := flat(e, prec)
	if p.col+len(f) <= p.width {
		p.write(f)
		return
	}
	p.broken(e, prec)
}

// broken prints e across multiple lines, breaking at operators and commas
// while increasing the continuation indent.
func (p *printer) broken(e Expr, prec int) {
	needParen := exprPrec(e) < prec
	if needParen {
		p.write("(")
	}
	switch n := e.(type) {
	case *Binary:
		bp := binPrec(n.Op)
		p.expr(n.X, bp)
		p.write(" " + n.Op)
		if p.col+1+len(flat(n.Y, bp+1)) > p.width {
			// Break after the operator, indent one level deeper.
			p.indent++
			p.newline(p.indent)
			p.expr(n.Y, bp+1)
			p.indent--
		} else {
			p.write(" ")
			p.expr(n.Y, bp+1)
		}
	case *Call:
		p.expr(n.Func, atomPrec)
		p.write("(")
		p.indent++
		for i, a := range n.Args {
			if i > 0 {
				// Break after the comma when the next argument
				// would overflow the line.
				p.write(",")
				if p.col+1+len(flat(a, lowestPrec)) > p.width {
					p.newline(p.indent)
				} else {
					p.write(" ")
				}
			}
			p.expr(a, lowestPrec)
		}
		p.indent--
		p.write(")")
	case *Unary:
		p.write(n.Op)
		p.expr(n.X, unaryPrec)
	default:
		// Atomic expressions have no legal break point.
		p.write(flat(e, prec))
	}
	if needParen {
		p.write(")")
	}
}

// flat renders e on a single line, inserting parentheses wherever the
// surrounding precedence requires them.
func flat(e Expr, prec int) string {
	var s string
	switch n := e.(type) {
	case *Number:
		s = n.Value
	case *String:
		s = strconv.Quote(n.Value)
	case *Ident:
		s = n.Name
	case *Unary:
		s = n.Op + flatOperand(n.X, unaryPrec)
	case *Binary:
		bp := binPrec(n.Op)
		s = flat(n.X, bp) + " " + n.Op + " " + flat(n.Y, bp+1)
	case *Call:
		parts := make([]string, len(n.Args))
		for i, a := range n.Args {
			parts[i] = flat(a, lowestPrec)
		}
		s = flat(n.Func, atomPrec) + "(" + strings.Join(parts, ", ") + ")"
	}
	if exprPrec(e) < prec {
		return "(" + s + ")"
	}
	return s
}

// flatOperand renders a unary operand, parenthesizing binary expressions.
func flatOperand(e Expr, prec int) string { return flat(e, prec) }
