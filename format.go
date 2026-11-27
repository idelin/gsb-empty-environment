package outerjoin

import (
	"fmt"
	"strings"
)

func QueryKey(q Query) string {
	var b strings.Builder
	writeNodeKey(&b, q.From)
	if q.Where != nil {
		b.WriteString(" WHERE ")
		writePredKey(&b, q.Where)
	}
	return b.String()
}

func FormatQuery(q Query) string {
	return QueryKey(q)
}

func writeNodeKey(b *strings.Builder, n Node) {
	switch node := n.(type) {
	case *RelationNode:
		b.WriteString(node.ID)
	case *JoinNode:
		b.WriteByte('(')
		writeNodeKey(b, node.Left)
		b.WriteByte(' ')
		b.WriteString(string(node.Type))
		b.WriteString(" JOIN ")
		writeNodeKey(b, node.Right)
		if node.On != nil {
			b.WriteString(" ON ")
			writePredKey(b, node.On)
		}
		b.WriteByte(')')
	default:
		fmt.Fprintf(b, "%T", n)
	}
}

func writePredKey(b *strings.Builder, p Pred) {
	switch pred := p.(type) {
	case nil:
		b.WriteString("TRUE")
	case *BoolLiteral:
		b.WriteString(pred.Value.String())
	case *NotPred:
		b.WriteString("NOT(")
		writePredKey(b, pred.Inner)
		b.WriteByte(')')
	case *AndPred:
		b.WriteByte('(')
		for i, term := range pred.Terms {
			if i > 0 {
				b.WriteString(" AND ")
			}
			writePredKey(b, term)
		}
		b.WriteByte(')')
	case *OrPred:
		b.WriteByte('(')
		for i, term := range pred.Terms {
			if i > 0 {
				b.WriteString(" OR ")
			}
			writePredKey(b, term)
		}
		b.WriteByte(')')
	case *Comparison:
		writeExprKey(b, pred.Left)
		b.WriteByte(' ')
		b.WriteString(string(pred.Op))
		b.WriteByte(' ')
		writeExprKey(b, pred.Right)
	case *NullTest:
		writeExprKey(b, pred.Operand)
		if pred.Negated {
			b.WriteString(" IS NOT NULL")
		} else {
			b.WriteString(" IS NULL")
		}
	case *InPred:
		writeExprKey(b, pred.Operand)
		if pred.Negated {
			b.WriteString(" NOT")
		}
		b.WriteString(" IN (")
		for i, item := range pred.Items {
			if i > 0 {
				b.WriteString(", ")
			}
			writeExprKey(b, item)
		}
		b.WriteByte(')')
	case *BooleanPredicate:
		writeExprKey(b, pred.Expr)
	default:
		fmt.Fprintf(b, "%T", p)
	}
}

func writeExprKey(b *strings.Builder, e Expr) {
	switch expr := e.(type) {
	case nil:
		b.WriteString("NULL")
	case *ColumnExpr:
		b.WriteString(expr.Ref.String())
	case *LiteralExpr:
		b.WriteString(valueKey(expr.Value))
	case *CoalesceExpr:
		b.WriteString("COALESCE(")
		for i, item := range expr.Exprs {
			if i > 0 {
				b.WriteString(", ")
			}
			writeExprKey(b, item)
		}
		b.WriteByte(')')
	case *CaseExpr:
		b.WriteString("CASE")
		for _, branch := range expr.Branches {
			b.WriteString(" WHEN ")
			writePredKey(b, branch.When)
			b.WriteString(" THEN ")
			writeExprKey(b, branch.Then)
		}
		if expr.Else != nil {
			b.WriteString(" ELSE ")
			writeExprKey(b, expr.Else)
		}
		b.WriteString(" END")
	default:
		fmt.Fprintf(b, "%T", e)
	}
}
