package joinselim

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Row is one physical table row: column name -> value. nil represents SQL NULL.
type Row map[string]any

// DataSet maps physical table names to table instances.
type DataSet map[string][]Row

// Tuple is one projected output row.
type Tuple []any

type evalRow map[string]any

// Evaluate executes the restricted query using exact bag semantics.
//
// The returned slice retains multiplicities. If Query.Distinct is true, equal
// tuples are deduplicated. Order is deterministic but semantically irrelevant.
func Evaluate(query Query, data DataSet) ([]Tuple, error) {
	if err := query.Validate(); err != nil {
		return nil, err
	}

	current := []evalRow{}
	for _, physical := range data[query.From.Table] {
		row := evalRow{}
		for col, value := range physical {
			row[Col(query.From.Alias, col).String()] = value
		}
		current = append(current, row)
	}

	for _, join := range query.Joins {
		rightRows := data[join.Table]
		next := make([]evalRow, 0, len(current))
		for _, left := range current {
			matches := 0
			for _, physicalRight := range rightRows {
				combined := cloneEvalRow(left)
				for col, value := range physicalRight {
					combined[Col(join.Alias, col).String()] = value
				}
				ok, err := joinConditionTrue(join, combined)
				if err != nil {
					return nil, err
				}
				if ok {
					next = append(next, combined)
					matches++
				}
			}
			if join.Type == LeftJoin && matches == 0 {
				padded := cloneEvalRow(left)
				next = append(next, padded)
			}
		}
		current = next
	}

	tuples := make([]Tuple, 0, len(current))
	for _, row := range current {
		keep, err := allPredicatesTrue(query.Where, row)
		if err != nil {
			return nil, err
		}
		if !keep {
			continue
		}
		tuple := make(Tuple, len(query.Projection))
		for i, ref := range query.Projection {
			tuple[i] = row[ref.String()]
		}
		if len(query.Projection) == 0 {
			tuple = Tuple{}
		}
		tuples = append(tuples, tuple)
	}

	if query.Distinct {
		tuples = DistinctTuples(tuples)
	}
	sortTuples(tuples)
	return tuples, nil
}

func joinConditionTrue(join Join, row evalRow) (bool, error) {
	for _, eq := range join.On {
		left := row[eq.Left.String()]
		right := row[eq.Right.String()]
		equal, err := valuesEqual(left, right)
		if err != nil {
			return false, err
		}
		if !equal {
			return false, nil
		}
	}
	return allPredicatesTrue(join.OnPredicates, row)
}

func allPredicatesTrue(predicates []Predicate, row evalRow) (bool, error) {
	for _, p := range predicates {
		ok, err := predicateTrue(p, row)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func predicateTrue(p Predicate, row evalRow) (bool, error) {
	left := row[p.Col.String()]
	switch p.Op {
	case OpNotNull:
		return left != nil, nil
	case OpIsNull:
		return left == nil, nil
	}

	var right any
	if p.Other != nil {
		right = row[p.Other.String()]
	} else {
		right = p.Value
	}

	switch p.Op {
	case OpEqual:
		return valuesEqual(left, right)
	case OpNotEqual:
		eq, err := valuesEqual(left, right)
		if err != nil {
			return false, err
		}
		// SQL NULL comparison is unknown and rejected by WHERE/ON.
		if left == nil || right == nil {
			return false, nil
		}
		return !eq, nil
	case OpLT, OpLE, OpGT, OpGE:
		return compareOrdered(p.Op, left, right)
	default:
		return false, fmt.Errorf("unsupported predicate op %q", p.Op)
	}
}

func valuesEqual(a, b any) (bool, error) {
	if a == nil || b == nil {
		return false, nil
	}
	if an, aok := numericFloat(a); aok {
		bn, bok := numericFloat(b)
		if !bok {
			return false, nil
		}
		return an == bn, nil
	}
	if as, ok := a.(string); ok {
		bs, ok := b.(string)
		return ok && as == bs, nil
	}
	if ab, ok := a.(bool); ok {
		bb, ok := b.(bool)
		return ok && ab == bb, nil
	}
	return false, fmt.Errorf("unsupported equality value types %T and %T", a, b)
}

func compareOrdered(op PredicateOp, a, b any) (bool, error) {
	if a == nil || b == nil {
		return false, nil
	}
	an, aok := numericFloat(a)
	bn, bok := numericFloat(b)
	if aok && bok {
		switch op {
		case OpLT:
			return an < bn, nil
		case OpLE:
			return an <= bn, nil
		case OpGT:
			return an > bn, nil
		case OpGE:
			return an >= bn, nil
		}
	}
	if as, ok := a.(string); ok {
		if bs, ok := b.(string); ok {
			switch op {
			case OpLT:
				return as < bs, nil
			case OpLE:
				return as <= bs, nil
			case OpGT:
				return as > bs, nil
			case OpGE:
				return as >= bs, nil
			}
		}
	}
	return false, fmt.Errorf("unsupported ordered comparison %q for %T and %T", op, a, b)
}

func numericFloat(v any) (float64, bool) {
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

func cloneEvalRow(in evalRow) evalRow {
	out := make(evalRow, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// DistinctTuples deduplicates tuples using exact value equality.
func DistinctTuples(tuples []Tuple) []Tuple {
	seen := map[string]bool{}
	out := make([]Tuple, 0, len(tuples))
	for _, tuple := range tuples {
		key := TupleKey(tuple)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, tuple)
	}
	sortTuples(out)
	return out
}

// TupleKey returns a deterministic type-preserving multiset key.
func TupleKey(tuple Tuple) string {
	var b strings.Builder
	b.WriteString("[")
	for _, value := range tuple {
		appendValueKey(&b, value)
	}
	b.WriteString("]")
	return b.String()
}

func appendValueKey(b *strings.Builder, value any) {
	switch v := value.(type) {
	case nil:
		b.WriteString("N;")
	case int:
		b.WriteString("i:")
		b.WriteString(strconv.Itoa(v))
		b.WriteByte(';')
	case int64:
		b.WriteString("i64:")
		b.WriteString(strconv.FormatInt(v, 10))
		b.WriteByte(';')
	case float64:
		b.WriteString("f:")
		b.WriteString(strconv.FormatFloat(v, 'g', -1, 64))
		b.WriteByte(';')
	case string:
		b.WriteString("s:")
		b.WriteString(strconv.Itoa(len(v)))
		b.WriteByte(':')
		b.WriteString(v)
		b.WriteByte(';')
	case bool:
		b.WriteString("b:")
		b.WriteString(strconv.FormatBool(v))
		b.WriteByte(';')
	default:
		fmt.Fprintf(b, "x:%T:%v;", value, value)
	}
}

// Multiset converts tuples to a map of exact tuple key to multiplicity.
func Multiset(tuples []Tuple) map[string]int {
	out := map[string]int{}
	for _, tuple := range tuples {
		out[TupleKey(tuple)]++
	}
	return out
}

// EqualMultiset reports whether two outputs have identical rows and multiplicities.
func EqualMultiset(a, b []Tuple) bool {
	ma, mb := Multiset(a), Multiset(b)
	if len(ma) != len(mb) {
		return false
	}
	for key, count := range ma {
		if mb[key] != count {
			return false
		}
	}
	return true
}

// MultisetDiff returns a human-readable difference between two bag outputs.
func MultisetDiff(want, got []Tuple) string {
	wm, gm := Multiset(want), Multiset(got)
	keys := map[string]bool{}
	for k := range wm {
		keys[k] = true
	}
	for k := range gm {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	var b strings.Builder
	for _, k := range sorted {
		if wm[k] != gm[k] {
			fmt.Fprintf(&b, "%s want=%d got=%d\n", k, wm[k], gm[k])
		}
	}
	return b.String()
}

func sortTuples(tuples []Tuple) {
	sort.SliceStable(tuples, func(i, j int) bool {
		return TupleKey(tuples[i]) < TupleKey(tuples[j])
	})
}
