package netsec

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// TenantLabel is the label the gateway enforces on every vector selector.
const TenantLabel = "tenant"

// Expr is a node in the simplified query tree accepted by the gateway.
type Expr interface {
	astNode()
	String() string
}

// MatchOp is the operator of a label matcher.
type MatchOp int

const (
	OpEqual MatchOp = iota
	OpNotEqual
	OpRegexMatch
	OpRegexNoMatch
)

func (op MatchOp) String() string {
	switch op {
	case OpEqual:
		return "="
	case OpNotEqual:
		return "!="
	case OpRegexMatch:
		return "=~"
	case OpRegexNoMatch:
		return "!~"
	default:
		return "?"
	}
}

// Matcher is a single label matcher inside a vector selector.
type Matcher struct {
	Label string
	Op    MatchOp
	Value string
}

func (m *Matcher) String() string {
	if m == nil {
		return ""
	}
	return m.Label + m.Op.String() + strconv.Quote(m.Value)
}

// VectorSelector selects time series by metric name and label matchers.
// Name may be empty for metric-name-less selectors such as {code="200"}.
type VectorSelector struct {
	Name     string
	Matchers []*Matcher
}

func (*VectorSelector) astNode() {}

func (v *VectorSelector) String() string {
	var b strings.Builder
	b.WriteString(v.Name)
	b.WriteByte('{')
	for i, matcher := range v.Matchers {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(matcher.String())
	}
	b.WriteByte('}')
	return b.String()
}

// RangeSelector wraps a vector selector with a time window, e.g. http[5m].
type RangeSelector struct {
	Vector *VectorSelector
	Range  time.Duration
}

func (*RangeSelector) astNode() {}

func (r *RangeSelector) String() string {
	return r.Vector.String() + "[" + formatDuration(r.Range) + "]"
}

// SubqueryExpr is a subquery such as rate(http[5m])[1h:1m].
type SubqueryExpr struct {
	Expr  Expr
	Range time.Duration
	Step  time.Duration
}

func (*SubqueryExpr) astNode() {}

func (s *SubqueryExpr) String() string {
	var b strings.Builder
	b.WriteString(s.Expr.String())
	b.WriteByte('[')
	b.WriteString(formatDuration(s.Range))
	b.WriteByte(':')
	if s.Step > 0 {
		b.WriteString(formatDuration(s.Step))
	}
	b.WriteByte(']')
	return b.String()
}

// NumberLiteral is a numeric constant.
type NumberLiteral struct {
	Value float64
}

func (*NumberLiteral) astNode() {}

func (n *NumberLiteral) String() string {
	return strconv.FormatFloat(n.Value, 'g', -1, 64)
}

// StringLiteral is a string constant, typically a function argument such as
// the destination label of label_replace or label_join.
type StringLiteral struct {
	Value string
}

func (*StringLiteral) astNode() {}

func (s *StringLiteral) String() string {
	return strconv.Quote(s.Value)
}

// Call is a function invocation such as rate(http[5m]).
type Call struct {
	Func string
	Args []Expr
}

func (*Call) astNode() {}

func (c *Call) String() string {
	args := make([]string, len(c.Args))
	for i, arg := range c.Args {
		args[i] = arg.String()
	}
	return c.Func + "(" + strings.Join(args, ", ") + ")"
}

// AggOp identifies an aggregation operator.
type AggOp string

const (
	AggSum         AggOp = "sum"
	AggAvg         AggOp = "avg"
	AggCount       AggOp = "count"
	AggMin         AggOp = "min"
	AggMax         AggOp = "max"
	AggGroup       AggOp = "group"
	AggStddev      AggOp = "stddev"
	AggStdvar      AggOp = "stdvar"
	AggTopk        AggOp = "topk"
	AggBottomk     AggOp = "bottomk"
	AggCountValues AggOp = "count_values"
	AggLimitk      AggOp = "limitk"
)

// AggregateExpr is an aggregation with a by/without grouping clause.
// Param holds the optional scalar or string parameter of operators such as
// topk or count_values.
type AggregateExpr struct {
	Op       AggOp
	Param    Expr
	Expr     Expr
	Grouping []string
	Without  bool
}

func (*AggregateExpr) astNode() {}

func (a *AggregateExpr) String() string {
	var b strings.Builder
	b.WriteString(string(a.Op))
	if len(a.Grouping) > 0 {
		if a.Without {
			b.WriteString(" without")
		} else {
			b.WriteString(" by")
		}
		b.WriteString(" (" + strings.Join(a.Grouping, ", ") + ")")
	}
	b.WriteString(" (")
	if a.Param != nil {
		b.WriteString(a.Param.String())
		b.WriteString(", ")
	}
	b.WriteString(a.Expr.String())
	b.WriteString(")")
	return b.String()
}

// VectorMatching describes the on/ignoring and group_left/group_right label
// lists of a binary operation.
type VectorMatching struct {
	On         []string
	Ignoring   []string
	GroupLeft  []string
	GroupRight []string
}

// BinaryExpr is a binary operation between two expressions. Matching is nil
// for one-to-one operations.
type BinaryExpr struct {
	Op       string
	LHS      Expr
	RHS      Expr
	Matching *VectorMatching
}

func (*BinaryExpr) astNode() {}

func (e *BinaryExpr) String() string {
	var b strings.Builder
	b.WriteByte('(')
	b.WriteString(e.LHS.String())
	b.WriteByte(' ')
	b.WriteString(e.Op)
	b.WriteByte(' ')
	b.WriteString(e.RHS.String())
	b.WriteByte(')')
	if e.Matching != nil {
		matching := e.Matching
		if len(matching.On) > 0 {
			b.WriteString(" on (" + strings.Join(matching.On, ", ") + ")")
		}
		if len(matching.Ignoring) > 0 {
			b.WriteString(" ignoring (" + strings.Join(matching.Ignoring, ", ") + ")")
		}
		if len(matching.GroupLeft) > 0 {
			b.WriteString(" group_left (" + strings.Join(matching.GroupLeft, ", ") + ")")
		}
		if len(matching.GroupRight) > 0 {
			b.WriteString(" group_right (" + strings.Join(matching.GroupRight, ", ") + ")")
		}
	}
	return b.String()
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	var b strings.Builder
	if hours := d / time.Hour; hours > 0 {
		fmt.Fprintf(&b, "%dh", hours)
		d -= hours * time.Hour
	}
	if minutes := d / time.Minute; minutes > 0 {
		fmt.Fprintf(&b, "%dm", minutes)
		d -= minutes * time.Minute
	}
	if seconds := d / time.Second; seconds > 0 {
		fmt.Fprintf(&b, "%ds", seconds)
		d -= seconds * time.Second
	}
	if millis := d / time.Millisecond; millis > 0 {
		fmt.Fprintf(&b, "%dms", millis)
	}
	return b.String()
}
