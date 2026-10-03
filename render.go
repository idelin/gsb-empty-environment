package decor

import (
	"strings"
)

// RenderExpr renders an expression back to SQL text.
func RenderExpr(e Expr) string {
	switch x := e.(type) {
	case *ColumnRef:
		if x.Table != "" {
			return x.Table + "." + x.Name
		}
		return x.Name
	case *Literal:
		if x.Kind == LitString {
			return "'" + x.Value + "'"
		}
		return x.Value
	case *BinaryExpr:
		l := RenderExpr(x.Left)
		r := RenderExpr(x.Right)
		if lb, ok := x.Left.(*BinaryExpr); ok && precOf(lb.Op) < precOf(x.Op) {
			l = "(" + l + ")"
		}
		if rb, ok := x.Right.(*BinaryExpr); ok && precOf(rb.Op) < precOf(x.Op) {
			r = "(" + r + ")"
		}
		return l + " " + x.Op + " " + r
	case *NotExpr:
		return "NOT " + RenderExpr(x.X)
	case *ExistsExpr:
		return "EXISTS (" + RenderSelect(x.Subquery) + ")"
	case *InExpr:
		s := RenderExpr(x.Left) + " "
		if x.Negated {
			s += "NOT "
		}
		if x.Subquery != nil {
			return s + "IN (" + RenderSelect(x.Subquery) + ")"
		}
		parts := make([]string, len(x.List))
		for i, e := range x.List {
			parts[i] = RenderExpr(e)
		}
		return s + "IN (" + strings.Join(parts, ", ") + ")"
	}
	return "?"
}

func precOf(op string) int {
	switch strings.ToUpper(op) {
	case "OR":
		return 1
	case "AND":
		return 2
	default:
		return 3
	}
}

// RenderSelect renders a SELECT statement back to SQL text.
func RenderSelect(s *SelectStmt) string {
	var b strings.Builder
	b.WriteString("SELECT ")
	if s.Distinct {
		b.WriteString("DISTINCT ")
	}
	items := make([]string, 0, len(s.Items))
	for _, it := range s.Items {
		switch {
		case it.Star:
			items = append(items, "*")
		case it.TableStar != "":
			items = append(items, it.TableStar+".*")
		default:
			t := RenderExpr(it.Expr)
			if it.Alias != "" {
				t += " AS " + it.Alias
			}
			items = append(items, t)
		}
	}
	b.WriteString(strings.Join(items, ", "))
	b.WriteString(" FROM ")
	tables := make([]string, 0, len(s.From))
	for _, t := range s.From {
		x := t.Name
		if t.Alias != "" {
			x += " " + t.Alias
		}
		tables = append(tables, x)
	}
	b.WriteString(strings.Join(tables, ", "))
	if s.Where != nil {
		b.WriteString(" WHERE ")
		b.WriteString(RenderExpr(s.Where))
	}
	if s.Limit != nil {
		b.WriteString(" LIMIT ")
		b.WriteString(itoa(*s.Limit))
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
