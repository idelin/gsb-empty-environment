package federate

import (
	"math/big"
	"sort"
	"strconv"
	"strings"
)

type aggState struct {
	count    int64
	sum      *big.Rat
	allInt   bool
	has      bool
	min      Value
	max      Value
	distinct map[string]Value
}

func newAggState() *aggState {
	return &aggState{sum: new(big.Rat), allInt: true}
}

func (s *aggState) addNumber(v Value) {
	if v.Type != TypeInt && v.Type != TypeFloat {
		return
	}
	if v.Type == TypeFloat {
		s.allInt = false
	}
	r := new(big.Rat)
	if v.Type == TypeInt {
		r.SetInt64(v.Int)
	} else {
		r.SetFloat64(v.Float)
	}
	s.sum.Add(s.sum, r)
	s.has = true
}

func (s *aggState) update(a *AggExpr, row Row, token, seed uint64) {
	if a.Kind == AggCount && a.Arg == nil {
		s.count++
		return
	}
	if a.Arg == nil {
		return
	}
	v := a.Arg.Eval(&EvalEnv{Row: row, Token: token, Seed: seed})
	if v.IsNull() {
		return
	}
	if a.Distinct {
		if s.distinct == nil {
			s.distinct = make(map[string]Value)
		}
		s.distinct[GroupKey(v)] = v
		return
	}
	switch a.Kind {
	case AggCount:
		s.count++
	case AggSum, AggAvg:
		s.addNumber(v)
		if a.Kind == AggAvg {
			s.count++
		}
	case AggMin:
		if !s.has {
			s.min, s.has = v, true
			return
		}
		if c, ok := Compare(v, s.min); ok && c < 0 {
			s.min = v
		}
	case AggMax:
		if !s.has {
			s.max, s.has = v, true
			return
		}
		if c, ok := Compare(v, s.max); ok && c > 0 {
			s.max = v
		}
	}
}

func (s *aggState) merge(o *aggState) {
	s.count += o.count
	if o.has {
		if !s.has {
			s.sum.Set(o.sum)
			s.allInt = o.allInt
			s.has = true
			if o.min.Type != TypeNull {
				s.min = o.min
			}
			if o.max.Type != TypeNull {
				s.max = o.max
			}
		} else {
			s.sum.Add(s.sum, o.sum)
			s.allInt = s.allInt && o.allInt
			if o.min.Type != TypeNull {
				if s.min.Type == TypeNull {
					s.min = o.min
				} else if c, ok := Compare(o.min, s.min); ok && c < 0 {
					s.min = o.min
				}
			}
			if o.max.Type != TypeNull {
				if s.max.Type == TypeNull {
					s.max = o.max
				} else if c, ok := Compare(o.max, s.max); ok && c > 0 {
					s.max = o.max
				}
			}
		}
	}
	if len(o.distinct) > 0 {
		if s.distinct == nil {
			s.distinct = make(map[string]Value, len(o.distinct))
		}
		for k, v := range o.distinct {
			s.distinct[k] = v
		}
	}
}

func (s *aggState) final(a *AggExpr) Value {
	if a.Distinct {
		return s.finalDistinct(a)
	}
	switch a.Kind {
	case AggCount:
		return Int(s.count)
	case AggSum:
		if !s.has {
			return Null()
		}
		return valueFromRat(s.sum, s.allInt)
	case AggAvg:
		if s.count == 0 {
			return Null()
		}
		f, _ := s.sum.Float64()
		return Float(f / float64(s.count))
	case AggMin:
		if !s.has {
			return Null()
		}
		return s.min
	case AggMax:
		if !s.has {
			return Null()
		}
		return s.max
	default:
		return Null()
	}
}

func (s *aggState) finalDistinct(a *AggExpr) Value {
	if len(s.distinct) == 0 {
		if a.Kind == AggCount {
			return Int(0)
		}
		return Null()
	}
	keys := make([]string, 0, len(s.distinct))
	for k := range s.distinct {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	switch a.Kind {
	case AggCount:
		return Int(int64(len(keys)))
	case AggSum, AggAvg:
		total := new(big.Rat)
		allInt := true
		for _, k := range keys {
			v := s.distinct[k]
			if v.Type != TypeInt && v.Type != TypeFloat {
				continue
			}
			if v.Type == TypeFloat {
				allInt = false
			}
			r := new(big.Rat)
			if v.Type == TypeInt {
				r.SetInt64(v.Int)
			} else {
				r.SetFloat64(v.Float)
			}
			total.Add(total, r)
		}
		if a.Kind == AggSum {
			return valueFromRat(total, allInt)
		}
		f, _ := total.Float64()
		return Float(f / float64(len(keys)))
	case AggMin, AggMax:
		var best Value
		have := false
		for _, k := range keys {
			v := s.distinct[k]
			if !have {
				best, have = v, true
				continue
			}
			c, ok := Compare(v, best)
			if !ok {
				continue
			}
			if (a.Kind == AggMin && c < 0) || (a.Kind == AggMax && c > 0) {
				best = v
			}
		}
		if !have {
			return Null()
		}
		return best
	default:
		return Null()
	}
}

func valueFromRat(r *big.Rat, allInt bool) Value {
	if allInt && r.IsInt() && r.Num().IsInt64() {
		return Int(r.Num().Int64())
	}
	f, _ := r.Float64()
	return Float(f)
}

type groupRecord struct {
	key    string
	rep    Row
	token  uint64
	states map[string]*aggState
	values map[string]Value
}

type groupSet struct {
	order []*groupRecord
	byKey map[string]*groupRecord
}

func newGroupSet() *groupSet {
	return &groupSet{byKey: make(map[string]*groupRecord)}
}

func (gs *groupSet) get(key string, rep Row, token uint64, aggs []*AggExpr) *groupRecord {
	if g, ok := gs.byKey[key]; ok {
		return g
	}
	g := &groupRecord{key: key, rep: rep, token: token, states: make(map[string]*aggState)}
	for _, a := range aggs {
		g.states[aggKey(a)] = newAggState()
	}
	gs.byKey[key] = g
	gs.order = append(gs.order, g)
	return g
}

func groupKeyFor(row Row, token, seed uint64, groupBy []Expr) string {
	if len(groupBy) == 0 {
		return ""
	}
	var b strings.Builder
	for _, e := range groupBy {
		v := e.Eval(&EvalEnv{Row: row, Token: token, Seed: seed})
		k := GroupKey(v)
		b.WriteString(strconv.Itoa(len(k)))
		b.WriteByte(':')
		b.WriteString(k)
		b.WriteByte(';')
	}
	return b.String()
}

func accumulateGroups(records []located, groupBy []Expr, aggs []*AggExpr, seed uint64) *groupSet {
	gs := newGroupSet()
	if len(records) == 0 && len(groupBy) == 0 {
		gs.get("", Row{}, 0, aggs)
	}
	for _, rec := range records {
		key := groupKeyFor(rec.row, rec.token, seed, groupBy)
		g := gs.get(key, rec.row, rec.token, aggs)
		for _, a := range aggs {
			g.states[aggKey(a)].update(a, rec.row, rec.token, seed)
		}
	}
	return gs
}

func finalizeGroups(gs *groupSet, aggs []*AggExpr) {
	for _, g := range gs.order {
		if g.values == nil {
			g.values = make(map[string]Value, len(aggs))
		}
		for _, a := range aggs {
			g.values[aggKey(a)] = g.states[aggKey(a)].final(a)
		}
	}
}

func mergeGroupSets(parts []*groupSet, aggs []*AggExpr) *groupSet {
	out := newGroupSet()
	for _, part := range parts {
		for _, src := range part.order {
			dst := out.get(src.key, src.rep, src.token, aggs)
			for _, a := range aggs {
				k := aggKey(a)
				ss, ok := src.states[k]
				if !ok {
					continue
				}
				ds, ok := dst.states[k]
				if !ok {
					ds = newAggState()
					dst.states[k] = ds
				}
				ds.merge(ss)
			}
		}
	}
	return out
}

func applyHavingGroups(groups []*groupRecord, having Expr, seed uint64) []*groupRecord {
	if having == nil {
		return groups
	}
	out := groups[:0]
	for _, g := range groups {
		env := &EvalEnv{Row: g.rep, Aggs: g.values, Token: g.token, Seed: seed}
		if parseBool(having.Eval(env)) {
			out = append(out, g)
		}
	}
	return out
}

func groupOrderKeys(g *groupRecord, order []OrderItem, seed uint64) []Value {
	if len(order) == 0 {
		return nil
	}
	env := &EvalEnv{Row: g.rep, Aggs: g.values, Token: g.token, Seed: seed}
	vals := make([]Value, len(order))
	for i, o := range order {
		vals[i] = o.Expr.Eval(env)
	}
	return vals
}

func sortLimitGroups(groups []*groupRecord, order []OrderItem, limit int64, seed uint64) []*groupRecord {
	if len(order) > 0 {
		keys := make(map[*groupRecord][]Value, len(groups))
		for _, g := range groups {
			keys[g] = groupOrderKeys(g, order, seed)
		}
		sort.SliceStable(groups, func(i, j int) bool {
			return compareOrderValues(keys[groups[i]], keys[groups[j]], order, groups[i].token, groups[j].token) < 0
		})
	}
	if limit >= 0 && int64(len(groups)) > limit {
		groups = groups[:limit]
	}
	return groups
}
