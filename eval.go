package promlite

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Element is one result series of an instant-vector evaluation.
type Element struct {
	Labels Labels
	V      float64
}

// Value is either a scalar or an instant vector.
type Value struct {
	IsScalar bool
	Scalar   float64
	Vector   []Element
}

// SampleRange is the closed interval of raw sample timestamps a step
// (and its children) actually consumed. Zero value means "no samples".
type SampleRange struct {
	Start, End time.Time
	Set        bool
}

func (r *SampleRange) add(t time.Time) {
	if !r.Set {
		r.Start, r.End, r.Set = t, t, true
		return
	}
	if t.Before(r.Start) {
		r.Start = t
	}
	if t.After(r.End) {
		r.End = t
	}
}

func (r *SampleRange) union(o SampleRange) {
	if !o.Set {
		return
	}
	r.add(o.Start)
	r.add(o.End)
}

func (r SampleRange) String() string {
	if !r.Set {
		return "samples: none"
	}
	return fmt.Sprintf("samples: [%s .. %s]",
		r.Start.UTC().Format("2006-01-02T15:04:05Z"),
		r.End.UTC().Format("2006-01-02T15:04:05Z"))
}

// Trace records, per evaluation step, which raw samples it depended on.
type Trace struct {
	Desc  string
	Range SampleRange
	Count int // number of result elements (1 for scalars)
	Kids  []*Trace
}

// Format renders the trace as an indented tree, one line per step.
func (t *Trace) Format() string {
	var b strings.Builder
	var walk func(n *Trace, prefix string, last bool)
	walk = func(n *Trace, prefix string, last bool) {
		conn := "├─ "
		if last {
			conn = "└─ "
		}
		fmt.Fprintf(&b, "%s%s%s | %s -> %d element(s)\n", prefix, conn, n.Desc, n.Range, n.Count)
		childPrefix := prefix + "│  "
		if last {
			childPrefix = prefix + "   "
		}
		for i, k := range n.Kids {
			walk(k, childPrefix, i == len(n.Kids)-1)
		}
	}
	fmt.Fprintf(&b, "%s | %s -> %d element(s)\n", t.Desc, t.Range, t.Count)
	for i, k := range t.Kids {
		walk(k, "", i == len(t.Kids)-1)
	}
	return b.String()
}

// Evaluator evaluates restricted expressions against a Store at an instant.
type Evaluator struct{ Store *Store }

func NewEvaluator(s *Store) *Evaluator { return &Evaluator{Store: s} }

// Eval evaluates e at instant t and returns the value plus a dependency trace.
func (ev *Evaluator) Eval(e Expr, t time.Time) (Value, *Trace, error) {
	return ev.eval(e, t)
}

func (ev *Evaluator) eval(e Expr, t time.Time) (Value, *Trace, error) {
	switch n := e.(type) {
	case NumberLit:
		return Value{IsScalar: true, Scalar: n.V},
			&Trace{Desc: "scalar " + Describe(n), Count: 1}, nil

	case Selector:
		samples := ev.Store.Instant(n.Metric, n.Matchers, t)
		tr := &Trace{Desc: "selector " + Describe(n)}
		vec := make([]Element, 0, len(samples))
		for _, smp := range samples {
			tr.Range.add(smp.T)
			vec = append(vec, Element{Labels: Labels(smp.Labels), V: smp.V})
		}
		tr.Count = len(vec)
		return Value{Vector: vec}, tr, nil

	case RateExpr:
		start := t.Add(-n.Window)
		series := ev.Store.Window(n.Sel.Metric, n.Sel.Matchers, start, t)
		tr := &Trace{Desc: fmt.Sprintf("rate window=%s (t-window, t]", n.Window)}
		var vec []Element
		for _, ss := range series {
			r, ok := counterRate(ss)
			if !ok {
				continue // fewer than 2 samples: no result, never fabricated
			}
			for _, smp := range ss {
				tr.Range.add(smp.T)
			}
			vec = append(vec, Element{Labels: Labels(ss[0].Labels), V: r})
		}
		tr.Count = len(vec)
		return Value{Vector: vec}, tr, nil

	case SumBy:
		v, child, err := ev.eval(n.Expr, t)
		if err != nil {
			return Value{}, nil, err
		}
		if v.IsScalar {
			return Value{}, nil, fmt.Errorf("sum by: cannot aggregate a scalar")
		}
		tr := &Trace{Desc: fmt.Sprintf("sum by (%s)", strings.Join(n.Labels, ", ")), Kids: []*Trace{child}}
		tr.Range.union(child.Range)
		groups := map[string]Element{}
		var order []string
		for _, el := range v.Vector {
			gl := Labels{}
			for _, name := range n.Labels {
				if val, ok := el.Labels[name]; ok {
					gl[name] = val
				}
			}
			k := gl.Key()
			g, ok := groups[k]
			if !ok {
				g = Element{Labels: gl}
				order = append(order, k)
			}
			g.V += el.V
			groups[k] = g
		}
		sort.Strings(order)
		vec := make([]Element, 0, len(order))
		for _, k := range order {
			vec = append(vec, groups[k])
		}
		tr.Count = len(vec)
		return Value{Vector: vec}, tr, nil

	case OffsetExpr:
		shifted := t.Add(-n.Offset)
		v, child, err := ev.eval(n.Expr, shifted)
		if err != nil {
			return Value{}, nil, err
		}
		tr := &Trace{
			Desc:  fmt.Sprintf("offset %s (evaluated at %s)", n.Offset, shifted.UTC().Format("2006-01-02T15:04:05Z")),
			Kids:  []*Trace{child},
			Count: child.Count,
		}
		tr.Range.union(child.Range)
		return v, tr, nil

	case BinaryExpr:
		return ev.evalBinary(n, t)
	}
	return Value{}, nil, fmt.Errorf("unknown expression node %T", e)
}

// counterRate computes per-second increase with counter-reset correction.
func counterRate(ss []Sample) (float64, bool) {
	if len(ss) < 2 {
		return 0, false
	}
	delta := ss[len(ss)-1].V - ss[0].V
	for i := 1; i < len(ss); i++ {
		if ss[i].V < ss[i-1].V {
			delta += ss[i-1].V // counter reset: count the pre-reset remainder
		}
	}
	dt := ss[len(ss)-1].T.Sub(ss[0].T).Seconds()
	if dt <= 0 {
		return 0, false
	}
	return delta / dt, true
}

func (ev *Evaluator) evalBinary(n BinaryExpr, t time.Time) (Value, *Trace, error) {
	lv, lt, err := ev.eval(n.L, t)
	if err != nil {
		return Value{}, nil, err
	}
	rv, rt, err := ev.eval(n.R, t)
	if err != nil {
		return Value{}, nil, err
	}
	desc := "binary " + n.Op
	if n.HasMod {
		kw := "ignoring"
		if n.On {
			kw = "on"
		}
		desc += fmt.Sprintf(" %s(%s)", kw, strings.Join(n.Labels, ", "))
	} else {
		desc += " on(all labels)"
	}
	tr := &Trace{Desc: desc, Kids: []*Trace{lt, rt}}
	tr.Range.union(lt.Range)
	tr.Range.union(rt.Range)

	// scalar op scalar
	if lv.IsScalar && rv.IsScalar {
		tr.Count = 1
		return Value{IsScalar: true, Scalar: arith(n.Op, lv.Scalar, rv.Scalar)}, tr, nil
	}
	// scalar op vector
	if lv.IsScalar {
		vec := make([]Element, 0, len(rv.Vector))
		for _, el := range rv.Vector {
			vec = append(vec, Element{Labels: el.Labels, V: arith(n.Op, lv.Scalar, el.V)})
		}
		tr.Count = len(vec)
		return Value{Vector: vec}, tr, nil
	}
	if rv.IsScalar {
		vec := make([]Element, 0, len(lv.Vector))
		for _, el := range lv.Vector {
			vec = append(vec, Element{Labels: el.Labels, V: arith(n.Op, el.V, rv.Scalar)})
		}
		tr.Count = len(vec)
		return Value{Vector: vec}, tr, nil
	}

	// vector op vector: one-to-one matching only.
	sig := func(l Labels) Labels {
		out := Labels{}
		if n.HasMod && n.On {
			for _, name := range n.Labels {
				if v, ok := l[name]; ok {
					out[name] = v
				}
			}
			return out
		}
		ignore := map[string]bool{}
		if n.HasMod {
			for _, name := range n.Labels {
				ignore[name] = true
			}
		}
		for k, v := range l {
			if !ignore[k] {
				out[k] = v
			}
		}
		return out
	}

	index := map[string]Element{}
	for _, el := range rv.Vector {
		k := sig(el.Labels).Key()
		if _, dup := index[k]; dup {
			return Value{}, nil, fmt.Errorf(
				"many-to-many matching rejected: label set %s matches multiple series on the right side of %q", k, n.Op)
		}
		index[k] = el
	}
	seen := map[string]bool{}
	var vec []Element
	for _, el := range lv.Vector {
		sl := sig(el.Labels)
		k := sl.Key()
		if seen[k] {
			return Value{}, nil, fmt.Errorf(
				"many-to-many matching rejected: label set %s matches multiple series on the left side of %q", k, n.Op)
		}
		seen[k] = true
		rhs, ok := index[k]
		if !ok {
			// Missing series: the element is dropped. It is NEVER
			// substituted with 0 (so it can never become a 0 divisor).
			continue
		}
		vec = append(vec, Element{Labels: sl, V: arith(n.Op, el.V, rhs.V)})
	}
	tr.Count = len(vec)
	return Value{Vector: vec}, tr, nil
}

func arith(op string, a, b float64) float64 {
	switch op {
	case "+":
		return a + b
	case "-":
		return a - b
	case "*":
		return a * b
	case "/":
		return a / b // IEEE semantics: x/0 -> ±Inf, 0/0 -> NaN (real data only)
	}
	return 0
}
