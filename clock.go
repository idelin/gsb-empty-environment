package archiveproof

import (
	"fmt"
	"math"
	"sort"
)

const (
	defaultMaxExactClockings = 200000
	defaultMaxScenarios      = 4096
)

// ClockConstraint is one edge's signed real-time difference bound.  It is
// exported so that callers can reproduce the verifier's exhaustive clocking
// enumeration without re-deriving certificate internals.
type ClockConstraint struct {
	FromSource ServiceID
	ToSource   ServiceID
	FromTime   int64
	ToTime     int64
	Gap        Gap
}

// EnumerateClockings returns representative clock assignments covering every
// compatible timing case.  When the integer domain is small the enumeration is
// literal and therefore exhaustive.  For large domains it uses every signed
// breakpoint, neighbouring integers, and cell midpoints.
func EnumerateClockings(domains map[ServiceID]Offset, constraints []ClockConstraint) ([]Clocking, error) {
	return enumerateClockings(domains, constraints, defaultMaxExactClockings, defaultMaxScenarios)
}

func enumerateClockings(domains map[ServiceID]Offset, constraints []ClockConstraint, maxExact, maxScenarios int) ([]Clocking, error) {
	if maxExact <= 0 {
		maxExact = defaultMaxExactClockings
	}
	if maxScenarios <= 0 {
		maxScenarios = defaultMaxScenarios
	}
	sources := make([]ServiceID, 0, len(domains))
	for s, d := range domains {
		if s == "" {
			return nil, fmt.Errorf("%w: empty clock source name", ErrInvalidArchive)
		}
		if !d.Valid() {
			return nil, fmt.Errorf("%w: source %q has empty domain", ErrIncompatibleClocks, s)
		}
		sources = append(sources, s)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i] < sources[j] })
	if len(sources) == 0 {
		return []Clocking{{}}, nil
	}

	exact, count := exactClockCount(domains, sources, maxExact)
	if exact {
		out := make([]Clocking, 0, count)
		cur := make(Clocking, len(sources))
		var rec func(int)
		rec = func(i int) {
			if i == len(sources) {
				cp := cur.Clone()
				out = append(out, cp)
				return
			}
			s := sources[i]
			d := domains[s]
			for v := d.Lo; ; v++ {
				cur[s] = v
				rec(i + 1)
				if v == d.Hi {
					break
				}
			}
		}
		rec(0)
		return out, nil
	}

	values := representativeValues(domains, sources, constraints, maxScenarios)
	return cartesianScenarios(sources, values, maxScenarios), nil
}

func exactClockCount(domains map[ServiceID]Offset, sources []ServiceID, maxExact int) (bool, int) {
	count := uint64(1)
	for _, s := range sources {
		d := domains[s]
		if d.Hi < d.Lo {
			return false, 0
		}
		span := uint64(d.Hi) - uint64(d.Lo)
		if span == ^uint64(0) {
			return false, 0
		}
		width := span + 1
		if width > uint64(maxExact) || count > uint64(maxExact)/width {
			return false, 0
		}
		count *= width
	}
	return true, int(count)
}

func representativeValues(domains map[ServiceID]Offset, sources []ServiceID, constraints []ClockConstraint, cap int) map[ServiceID][]int64 {
	perSourceCap := 24
	if len(sources) > 1 {
		// Keep the product bounded even for many clock sources.
		p := 1
		for p*p < cap && p < len(sources)*8 {
			p++
		}
		if p < 6 {
			p = 6
		}
		perSourceCap = p
	}
	out := make(map[ServiceID][]int64, len(sources))
	for _, s := range sources {
		d := domains[s]
		set := map[int64]struct{}{d.Lo: {}, d.Hi: {}}
		span := uint64(d.Hi) - uint64(d.Lo)
		mid := d.Lo + int64(span/2)
		set[mid] = struct{}{}
		if d.Contains(0) {
			set[0] = struct{}{}
		}
		var bases []int64
		for _, c := range constraints {
			if !c.Gap.Valid() {
				continue
			}
			// Same-source edges do not constrain the offset at all.
			if c.FromSource == c.ToSource {
				continue
			}
			if c.FromSource != s && c.ToSource != s {
				continue
			}
			base, ok := subInt64(c.ToTime, c.FromTime)
			if !ok {
				continue
			}
			if lo, ok := addInt64(base, c.Gap.Lo); ok {
				bases = append(bases, lo)
			}
			if hi, ok := addInt64(base, c.Gap.Hi); ok {
				bases = append(bases, hi)
			}
		}
		for _, b := range bases {
			addBasis := func(v int64) {
				if v < d.Lo {
					v = d.Lo
				}
				if v > d.Hi {
					v = d.Hi
				}
				set[v] = struct{}{}
			}
			addBasis(b)
			if v, ok := subInt64(0, b); ok {
				addBasis(v)
			}
			if v, ok := addInt64(b, d.Lo); ok {
				addBasis(v)
			}
			if v, ok := addInt64(b, d.Hi); ok {
				addBasis(v)
			}
			if v, ok := addInt64(b, mid); ok {
				addBasis(v)
			}
			if v, ok := subInt64(d.Lo, b); ok {
				addBasis(v)
			}
			if v, ok := subInt64(d.Hi, b); ok {
				addBasis(v)
			}
			if v, ok := subInt64(mid, b); ok {
				addBasis(v)
			}
			if v, ok := addInt64(b, -1); ok {
				addBasis(v)
			}
			if v, ok := addInt64(b, 1); ok {
				addBasis(v)
			}
		}
		vals := make([]int64, 0, len(set))
		for v := range set {
			vals = append(vals, v)
		}
		sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
		withMids := make([]int64, 0, len(vals)*2)
		for i, v := range vals {
			withMids = append(withMids, v)
			if i+1 < len(vals) && vals[i+1] > v+1 {
				m := v + (vals[i+1]-v)/2
				withMids = append(withMids, m)
			}
		}
		vals = dedupeInt64(withMids)
		if len(vals) > perSourceCap {
			vals = sampleInt64(vals, perSourceCap)
		}
		out[s] = vals
	}
	return out
}

func dedupeInt64(in []int64) []int64 {
	out := in[:0]
	var last int64
	first := true
	for _, v := range in {
		if first || v != last {
			out = append(out, v)
			last = v
			first = false
		}
	}
	return out
}

func sampleInt64(in []int64, n int) []int64 {
	if n < 2 {
		if len(in) == 0 {
			return nil
		}
		return in[:1]
	}
	if n >= len(in) {
		return in
	}
	out := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		idx := int(int64(i) * int64(len(in)-1) / int64(n-1))
		out = append(out, in[idx])
	}
	out[0] = in[0]
	out[len(out)-1] = in[len(in)-1]
	return dedupeInt64(out)
}

func cartesianScenarios(sources []ServiceID, values map[ServiceID][]int64, cap int) []Clocking {
	if cap < 2 {
		cap = 2
	}
	total := int64(1)
	for _, s := range sources {
		n := len(values[s])
		if n == 0 {
			return nil
		}
		if total > (int64(^uint64(0)>>1))/int64(n) {
			total = int64(^uint64(0) >> 1)
			break
		}
		total *= int64(n)
	}
	var out []Clocking
	appendAt := func(index int64) {
		c := make(Clocking, len(sources))
		for i := len(sources) - 1; i >= 0; i-- {
			s := sources[i]
			vals := values[s]
			c[s] = vals[index%int64(len(vals))]
			index /= int64(len(vals))
		}
		out = append(out, c)
	}
	if total <= int64(cap) {
		for i := int64(0); i < total; i++ {
			appendAt(i)
		}
		return out
	}
	seen := make(map[string]struct{})
	for i := 0; i < cap; i++ {
		idx := int64(i) * (total - 1) / int64(cap-1)
		c := make(Clocking, len(sources))
		rem := idx
		for j := len(sources) - 1; j >= 0; j-- {
			s := sources[j]
			vals := values[s]
			c[s] = vals[rem%int64(len(vals))]
			rem /= int64(len(vals))
		}
		k := clockingKey(c, sources)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, c)
	}
	return out
}

func clockingKey(c Clocking, sources []ServiceID) string {
	k := ""
	for _, s := range sources {
		k += fmt.Sprintf("%s=%d;", s, c[s])
	}
	return k
}

func addInt64(a, b int64) (int64, bool) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, false
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, false
	}
	return a + b, true
}

func subInt64(a, b int64) (int64, bool) {
	if b == math.MinInt64 {
		if a >= 0 {
			return 0, false
		}
		return a - b, true
	}
	return addInt64(a, -b)
}

func boundsFit(t int64, in Interval) bool {
	if _, ok := addInt64(t, in.Lo); !ok {
		return false
	}
	if _, ok := addInt64(t, in.Hi); !ok {
		return false
	}
	return true
}

// gapAllows reports whether realTime (newer) - realTime (older) is in gap.
func gapAllows(fromTime, toTime int64, fromSource, toSource ServiceID, gap Gap, clocking Clocking) (bool, error) {
	df, ok := clocking[fromSource]
	if !ok {
		return false, fmt.Errorf("%w: source %q", ErrMissingClocking, fromSource)
	}
	dt, ok := clocking[toSource]
	if !ok {
		return false, fmt.Errorf("%w: source %q", ErrMissingClocking, toSource)
	}
	rf, ok := addInt64(fromTime, df)
	if !ok {
		return false, nil
	}
	rt, ok := addInt64(toTime, dt)
	if !ok {
		return false, nil
	}
	diff, ok := addInt64(rf, -rt)
	if !ok {
		return false, nil
	}
	return gap.Contains(diff), nil
}
