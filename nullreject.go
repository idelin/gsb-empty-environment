package outerjoin

import (
	"fmt"
	"sort"
)

type Witness Row

func IsNullRejecting(pred Pred, schema Schema, targetRelations ...string) (bool, Witness, error) {
	targets := map[string]bool{}
	for _, rel := range targetRelations {
		targets[rel] = true
	}
	for rel := range targets {
		if _, ok := schema[Ref{Relation: rel, Attr: "__check__"}]; !ok {
			found := false
			for ref := range schema {
				if ref.Relation == rel {
					found = true
					break
				}
			}
			if !found {
				return false, nil, errorf("null-rejection target relation %q is absent from schema", rel)
			}
		}
	}

	referenced := CollectRefsPred(pred)
	for _, ref := range referenced {
		if _, ok := schema[ref]; !ok {
			return false, nil, errorf("predicate references %s, absent from schema", ref)
		}
	}

	literals := collectLiteralsPred(pred)
	freeCount := len(referenced) + 2
	domains := map[Ref][]Value{}
	var freeRefs []Ref
	for _, ref := range referenced {
		if targets[ref.Relation] {
			domains[ref] = []Value{nil}
			continue
		}
		domains[ref] = domainFor(ref, schema[ref], literals, freeCount)
		freeRefs = append(freeRefs, ref)
	}

	base := Row{}
	for ref, typ := range schema {
		_ = typ
		if targets[ref.Relation] {
			base[ref] = nil
		}
	}

	var witness Row
	foundTrue, err := enumerateAssignments(pred, base, freeRefs, domains, 0, func(row Row) (bool, error) {
		value, evalErr := EvalPred(pred, row)
		if evalErr != nil {
			return false, evalErr
		}
		if value == True {
			witness = row.Clone()
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return false, nil, err
	}
	if foundTrue {
		return false, Witness(witness), nil
	}
	return true, nil, nil
}

func NullRejectingOnSet(pred Pred, schema Schema, relations []string) (bool, Row, error) {
	nr, witness, err := IsNullRejecting(pred, schema, relations...)
	return nr, Row(witness), err
}

type RelationNullRejection struct {
	Relation      string
	NullRejecting bool
	TrueWitness   Row
}

func NullRejectingRelations(pred Pred, schema Schema) ([]RelationNullRejection, error) {
	rels := schema.relations()
	out := make([]RelationNullRejection, 0, len(rels))
	for _, rel := range rels {
		nr, witness, err := IsNullRejecting(pred, schema, rel)
		if err != nil {
			return nil, err
		}
		result := RelationNullRejection{Relation: rel, NullRejecting: nr}
		if witness != nil {
			result.TrueWitness = Row(witness)
		}
		out = append(out, result)
	}
	return out, nil
}

func enumerateAssignments(
	pred Pred,
	row Row,
	refs []Ref,
	domains map[Ref][]Value,
	index int,
	visit func(Row) (bool, error),
) (bool, error) {
	if index == len(refs) {
		stop, err := visit(row)
		return stop, err
	}
	ref := refs[index]
	for _, value := range domains[ref] {
		row[ref] = value
		stop, err := enumerateAssignments(pred, row, refs, domains, index+1, visit)
		if err != nil {
			return false, err
		}
		if stop {
			return true, nil
		}
	}
	delete(row, ref)
	return false, nil
}

func domainFor(ref Ref, typ DataType, literals []Value, freeCount int) []Value {
	switch typ {
	case TypeBool:
		return []Value{nil, false, true}
	case TypeString:
		return stringDomain(literals, freeCount)
	case TypeFloat:
		return floatDomain(literals, freeCount)
	case TypeInt:
		return intDomain(literals, freeCount)
	default:
		values := []Value{nil}
		seen := map[string]bool{"nil": true}
		for _, literal := range literals {
			key := valueKey(literal)
			if !seen[key] {
				seen[key] = true
				values = append(values, literal)
			}
		}
		for _, value := range intDomain(nil, freeCount) {
			key := valueKey(value)
			if !seen[key] {
				seen[key] = true
				values = append(values, value)
			}
		}
		for _, value := range []string{"", "__a__", "__b__", "__m__", "__z__"} {
			key := valueKey(value)
			if !seen[key] {
				seen[key] = true
				values = append(values, value)
			}
		}
		return values
	}
}

func intDomain(literals []Value, freeCount int) []Value {
	set := map[int64]bool{}
	for _, literal := range literals {
		if n, ok := integral(literal); ok {
			set[n] = true
		}
	}
	if len(set) == 0 {
		for n := int64(-freeCount - 1); n <= int64(freeCount+1); n++ {
			set[n] = true
		}
	} else {
		values := sortedInts(set)
		min := values[0]
		max := values[len(values)-1]
		for d := 1; d <= freeCount; d++ {
			set[min-int64(d)] = true
			set[max+int64(d)] = true
		}
		for i := 0; i+1 < len(values); i++ {
			lo := values[i]
			hi := values[i+1]
			for d := int64(1); d <= int64(freeCount); d++ {
				v := lo + d
				if v >= hi {
					break
				}
				set[v] = true
			}
		}
	}
	set[0] = true
	set[1] = true
	return intValues(set)
}

func floatDomain(literals []Value, freeCount int) []Value {
	set := map[float64]bool{}
	for _, literal := range literals {
		if n, ok := numericFloat(literal); ok {
			set[n] = true
		}
	}
	values := make([]float64, 0, len(set))
	for v := range set {
		values = append(values, v)
	}
	sort.Float64s(values)
	if len(values) == 0 {
		for i := -freeCount - 1; i <= freeCount+1; i++ {
			set[float64(i)] = true
		}
	} else {
		min := values[0]
		max := values[len(values)-1]
		for d := 1; d <= freeCount; d++ {
			set[min-float64(d)] = true
			set[max+float64(d)] = true
		}
		for i := 0; i+1 < len(values); i++ {
			lo := values[i]
			hi := values[i+1]
			for d := 1; d <= freeCount; d++ {
				set[lo+(hi-lo)*float64(d)/float64(freeCount+1)] = true
			}
		}
	}
	out := []Value{nil}
	seen := map[string]bool{}
	for v := range set {
		key := valueKey(v)
		if !seen[key] {
			seen[key] = true
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].(float64) < out[j].(float64)
	})
	return out
}

func stringDomain(literals []Value, freeCount int) []Value {
	set := map[string]bool{"": true, "__a__": true, "__b__": true, "__m__": true, "__z__": true}
	for _, literal := range literals {
		if s, ok := literal.(string); ok {
			set[s] = true
		}
	}
	for i := 0; i < freeCount; i++ {
		set[fmt.Sprintf("__free_%02d__", i)] = true
	}
	out := []Value{nil}
	values := make([]string, 0, len(set))
	for s := range set {
		values = append(values, s)
	}
	sort.Strings(values)
	for _, s := range values {
		out = append(out, s)
	}
	return out
}

func integral(value Value) (int64, bool) {
	switch n := value.(type) {
	case int:
		return int64(n), true
	case int8:
		return int64(n), true
	case int16:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint:
		return int64(n), true
	case uint8:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint64:
		return int64(n), true
	case float64:
		if n == float64(int64(n)) {
			return int64(n), true
		}
	case float32:
		return integral(float64(n))
	}
	return 0, false
}

func sortedInts(set map[int64]bool) []int64 {
	out := make([]int64, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func intValues(set map[int64]bool) []Value {
	ints := sortedInts(set)
	out := []Value{nil}
	for _, v := range ints {
		out = append(out, v)
	}
	return out
}

func CollectRefsPred(pred Pred) []Ref {
	seen := map[Ref]bool{}
	var refs []Ref
	add := func(r Ref) {
		if !seen[r] {
			seen[r] = true
			refs = append(refs, r)
		}
	}
	var walkPred func(Pred)
	var walkExpr func(Expr)
	walkPred = func(p Pred) {
		switch q := p.(type) {
		case nil:
		case *BoolLiteral:
		case *NotPred:
			walkPred(q.Inner)
		case *AndPred:
			for _, term := range q.Terms {
				walkPred(term)
			}
		case *OrPred:
			for _, term := range q.Terms {
				walkPred(term)
			}
		case *Comparison:
			walkExpr(q.Left)
			walkExpr(q.Right)
		case *NullTest:
			walkExpr(q.Operand)
		case *InPred:
			walkExpr(q.Operand)
			for _, item := range q.Items {
				walkExpr(item)
			}
		case *BooleanPredicate:
			walkExpr(q.Expr)
		}
	}
	walkExpr = func(e Expr) {
		switch x := e.(type) {
		case nil:
		case *ColumnExpr:
			add(x.Ref)
		case *LiteralExpr:
		case *CoalesceExpr:
			for _, item := range x.Exprs {
				walkExpr(item)
			}
		case *CaseExpr:
			for _, branch := range x.Branches {
				walkPred(branch.When)
				walkExpr(branch.Then)
			}
			if x.Else != nil {
				walkExpr(x.Else)
			}
		}
	}
	walkPred(pred)
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Relation != refs[j].Relation {
			return refs[i].Relation < refs[j].Relation
		}
		return refs[i].Attr < refs[j].Attr
	})
	return refs
}

func collectLiteralsPred(pred Pred) []Value {
	var out []Value
	var walkPred func(Pred)
	var walkExpr func(Expr)
	walkPred = func(p Pred) {
		switch q := p.(type) {
		case nil, *BoolLiteral:
		case *NotPred:
			walkPred(q.Inner)
		case *AndPred:
			for _, term := range q.Terms {
				walkPred(term)
			}
		case *OrPred:
			for _, term := range q.Terms {
				walkPred(term)
			}
		case *Comparison:
			walkExpr(q.Left)
			walkExpr(q.Right)
		case *NullTest:
			walkExpr(q.Operand)
		case *InPred:
			walkExpr(q.Operand)
			for _, item := range q.Items {
				walkExpr(item)
			}
		case *BooleanPredicate:
			walkExpr(q.Expr)
		}
	}
	walkExpr = func(e Expr) {
		switch x := e.(type) {
		case nil:
		case *LiteralExpr:
			out = append(out, x.Value)
		case *ColumnExpr:
		case *CoalesceExpr:
			for _, item := range x.Exprs {
				walkExpr(item)
			}
		case *CaseExpr:
			for _, branch := range x.Branches {
				walkPred(branch.When)
				walkExpr(branch.Then)
			}
			if x.Else != nil {
				walkExpr(x.Else)
			}
		}
	}
	walkPred(pred)
	return out
}

func PredicateCanBeTrue(pred Pred, schema Schema) (bool, Row, error) {
	nr, witness, err := IsNullRejecting(pred, schema)
	if err != nil {
		return false, nil, err
	}
	return !nr, Row(witness), nil
}
