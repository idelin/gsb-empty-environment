package outerjoin

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

func KleeneNot(v Tri) Tri {
	switch v {
	case True:
		return False
	case False:
		return True
	default:
		return Unknown
	}
}

func KleeneAnd(values ...Tri) Tri {
	result := True
	for _, v := range values {
		if v == False {
			return False
		}
		if v == Unknown {
			result = Unknown
		}
	}
	return result
}

func KleeneOr(values ...Tri) Tri {
	result := False
	for _, v := range values {
		if v == True {
			return True
		}
		if v == Unknown {
			result = Unknown
		}
	}
	return result
}

func EvalPred(p Pred, row Row) (Tri, error) {
	switch pred := p.(type) {
	case nil:
		return True, nil
	case *BoolLiteral:
		return pred.Value, nil
	case *NotPred:
		v, err := EvalPred(pred.Inner, row)
		if err != nil {
			return Unknown, err
		}
		return KleeneNot(v), nil
	case *AndPred:
		vals := make([]Tri, 0, len(pred.Terms))
		for _, term := range pred.Terms {
			v, err := EvalPred(term, row)
			if err != nil {
				return Unknown, err
			}
			vals = append(vals, v)
		}
		return KleeneAnd(vals...), nil
	case *OrPred:
		vals := make([]Tri, 0, len(pred.Terms))
		for _, term := range pred.Terms {
			v, err := EvalPred(term, row)
			if err != nil {
				return Unknown, err
			}
			vals = append(vals, v)
		}
		return KleeneOr(vals...), nil
	case *Comparison:
		left, err := EvalExpr(pred.Left, row)
		if err != nil {
			return Unknown, err
		}
		right, err := EvalExpr(pred.Right, row)
		if err != nil {
			return Unknown, err
		}
		if left == nil || right == nil {
			return Unknown, nil
		}
		cmp, err := compareValues(left, right)
		if err != nil {
			return Unknown, err
		}
		switch pred.Op {
		case Eq:
			return triBool(cmp == 0), nil
		case Ne:
			return triBool(cmp != 0), nil
		case Lt:
			return triBool(cmp < 0), nil
		case Le:
			return triBool(cmp <= 0), nil
		case Gt:
			return triBool(cmp > 0), nil
		case Ge:
			return triBool(cmp >= 0), nil
		default:
			return Unknown, errorf("unknown comparison operator %q", pred.Op)
		}
	case *NullTest:
		value, err := EvalExpr(pred.Operand, row)
		if err != nil {
			return Unknown, err
		}
		isNull := value == nil
		if pred.Negated {
			isNull = !isNull
		}
		return triBool(isNull), nil
	case *InPred:
		value, err := EvalExpr(pred.Operand, row)
		if err != nil {
			return Unknown, err
		}
		if value == nil {
			return Unknown, nil
		}
		result := False
		for _, itemExpr := range pred.Items {
			item, err := EvalExpr(itemExpr, row)
			if err != nil {
				return Unknown, err
			}
			if item == nil {
				result = Unknown
				continue
			}
			cmp, err := compareValues(value, item)
			if err != nil {
				return Unknown, err
			}
			if cmp == 0 {
				result = True
				break
			}
		}
		if pred.Negated {
			return KleeneNot(result), nil
		}
		return result, nil
	case *BooleanPredicate:
		value, err := EvalExpr(pred.Expr, row)
		if err != nil {
			return Unknown, err
		}
		if value == nil {
			return Unknown, nil
		}
		b, ok := value.(bool)
		if !ok {
			return Unknown, errorf("boolean predicate got non-boolean value %T", value)
		}
		return triBool(b), nil
	default:
		return Unknown, errorf("unsupported predicate %T", p)
	}
}

func EvalExpr(e Expr, row Row) (Value, error) {
	switch expr := e.(type) {
	case nil:
		return nil, nil
	case *LiteralExpr:
		return expr.Value, nil
	case *ColumnExpr:
		value, ok := row[expr.Ref]
		if !ok {
			return nil, errorf("unbound column %s", expr.Ref)
		}
		return value, nil
	case *CoalesceExpr:
		for _, candidate := range expr.Exprs {
			value, err := EvalExpr(candidate, row)
			if err != nil {
				return nil, err
			}
			if value != nil {
				return value, nil
			}
		}
		return nil, nil
	case *CaseExpr:
		for _, branch := range expr.Branches {
			condition, err := EvalPred(branch.When, row)
			if err != nil {
				return nil, err
			}
			if condition == True {
				return EvalExpr(branch.Then, row)
			}
		}
		if expr.Else != nil {
			return EvalExpr(expr.Else, row)
		}
		return nil, nil
	default:
		return nil, errorf("unsupported expression %T", e)
	}
}

func triBool(v bool) Tri {
	if v {
		return True
	}
	return False
}

func compareValues(left, right Value) (int, error) {
	if left == nil || right == nil {
		return 0, errorf("NULL values cannot be compared")
	}

	lf, lok := numericFloat(left)
	rf, rok := numericFloat(right)
	if lok && rok {
		if lf < rf {
			return -1, nil
		}
		if lf > rf {
			return 1, nil
		}
		return 0, nil
	}

	ls, lok := left.(string)
	rs, rok := right.(string)
	if lok && rok {
		return strings.Compare(ls, rs), nil
	}

	lb, lok := left.(bool)
	rb, rok := right.(bool)
	if lok && rok {
		if lb == rb {
			return 0, nil
		}
		if !lb && rb {
			return -1, nil
		}
		return 1, nil
	}

	if reflect.DeepEqual(left, right) {
		return 0, nil
	}
	return 0, errorf("incomparable values %T(%v) and %T(%v)", left, left, right, right)
}

func numericFloat(v Value) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}

func Evaluate(query Query, db Database) ([]Row, error) {
	rows, err := evalNode(query.From, db)
	if err != nil {
		return nil, err
	}
	if query.Where == nil {
		return rows, nil
	}
	out := make([]Row, 0, len(rows))
	for _, row := range rows {
		keep, err := EvalPred(query.Where, row)
		if err != nil {
			return nil, err
		}
		if keep == True {
			out = append(out, row)
		}
	}
	return out, nil
}

func evalNode(node Node, db Database) ([]Row, error) {
	switch n := node.(type) {
	case *RelationNode:
		table, ok := db[n.ID]
		if !ok {
			return nil, errorf("missing table %q", n.ID)
		}
		out := make([]Row, 0, len(table))
		for _, source := range table {
			row := Row{}
			for ref, typ := range n.Schema() {
				value, exists := source[ref]
				if exists {
					row[ref] = value
					continue
				}
				_ = typ
				row[ref] = nil
			}
			out = append(out, row)
		}
		return out, nil
	case *JoinNode:
		return evalJoin(n, db)
	default:
		return nil, errorf("unsupported node %T", node)
	}
}

func evalJoin(n *JoinNode, db Database) ([]Row, error) {
	leftRows, err := evalNode(n.Left, db)
	if err != nil {
		return nil, err
	}
	rightRows, err := evalNode(n.Right, db)
	if err != nil {
		return nil, err
	}

	leftSchema := n.Left.Schema().refs()
	rightSchema := n.Right.Schema().refs()
	matchedLeft := make([]bool, len(leftRows))
	matchedRight := make([]bool, len(rightRows))

	var out []Row
	for i, left := range leftRows {
		for j, right := range rightRows {
			merged := mergeRows(left, right)
			match, err := EvalPred(n.On, merged)
			if err != nil {
				return nil, err
			}
			if match == True {
				out = append(out, merged)
				matchedLeft[i] = true
				matchedRight[j] = true
			}
		}
	}

	if n.Type == Left || n.Type == Full {
		for i, left := range leftRows {
			if !matchedLeft[i] {
				out = append(out, mergeRows(left, nullRow(rightSchema)))
			}
		}
	}
	if n.Type == Right || n.Type == Full {
		for j, right := range rightRows {
			if !matchedRight[j] {
				out = append(out, mergeRows(nullRow(leftSchema), right))
			}
		}
	}
	if n.Type != Inner && n.Type != Left && n.Type != Right && n.Type != Full {
		return nil, errorf("unknown join type %q", n.Type)
	}
	return out, nil
}

func mergeRows(left, right Row) Row {
	out := make(Row, len(left)+len(right))
	for ref, value := range left {
		out[ref] = value
	}
	for ref, value := range right {
		out[ref] = value
	}
	return out
}

func nullRow(refs []Ref) Row {
	row := Row{}
	for _, ref := range refs {
		row[ref] = nil
	}
	return row
}

type BagDiff struct {
	Equal        bool
	OriginalOnly map[string]int
	RewriteOnly  map[string]int
	OriginalSize int
	RewriteSize  int
	Sample       string
}

func VerifyBagEqual(left, right []Row) BagDiff {
	leftCounts := bagCounts(left)
	rightCounts := bagCounts(right)
	diff := BagDiff{
		Equal:        true,
		OriginalOnly: map[string]int{},
		RewriteOnly:  map[string]int{},
		OriginalSize: len(left),
		RewriteSize:  len(right),
	}
	keys := map[string]struct{}{}
	for key := range leftCounts {
		keys[key] = struct{}{}
	}
	for key := range rightCounts {
		keys[key] = struct{}{}
	}
	for key := range keys {
		lc := leftCounts[key]
		rc := rightCounts[key]
		if lc > rc {
			diff.OriginalOnly[key] = lc - rc
			diff.Equal = false
		}
		if rc > lc {
			diff.RewriteOnly[key] = rc - lc
			diff.Equal = false
		}
	}
	if !diff.Equal {
		var samples []string
		for key, count := range diff.OriginalOnly {
			samples = append(samples, fmt.Sprintf("original-only x%d: %s", count, key))
		}
		for key, count := range diff.RewriteOnly {
			samples = append(samples, fmt.Sprintf("rewrite-only x%d: %s", count, key))
		}
		sort.Strings(samples)
		if len(samples) > 0 {
			diff.Sample = samples[0]
		}
	}
	return diff
}

func bagCounts(rows []Row) map[string]int {
	counts := map[string]int{}
	for _, row := range rows {
		counts[RowKey(row)]++
	}
	return counts
}

func RowKey(row Row) string {
	refs := make([]Ref, 0, len(row))
	for ref := range row {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Relation != refs[j].Relation {
			return refs[i].Relation < refs[j].Relation
		}
		return refs[i].Attr < refs[j].Attr
	})
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		parts = append(parts, ref.String()+"="+valueKey(row[ref]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func valueKey(value Value) string {
	if value == nil {
		return "NULL"
	}
	switch v := value.(type) {
	case string:
		return strconv.Quote(v)
	case []byte:
		return strconv.Quote(string(v))
	case float64:
		if math.IsNaN(v) {
			return "NaN"
		}
		return strconv.FormatFloat(v, 'g', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(v), 'g', -1, 32)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case bool:
		return strconv.FormatBool(v)
	default:
		return fmt.Sprintf("%T(%v)", value, value)
	}
}

func VerifyQueriesBagEqual(left, right Query, db Database) (bool, BagDiff, error) {
	leftRows, err := Evaluate(left, db)
	if err != nil {
		return false, BagDiff{}, err
	}
	rightRows, err := Evaluate(right, db)
	if err != nil {
		return false, BagDiff{}, err
	}
	diff := VerifyBagEqual(leftRows, rightRows)
	return diff.Equal, diff, nil
}
