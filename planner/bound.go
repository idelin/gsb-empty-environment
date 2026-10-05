package planner

// LowerBound returns a legal lower bound on the peak disk usage of ANY
// valid execution order. It combines two order-independent arguments:
//
//  1. While block i decompresses, its compressed and temp bytes coexist,
//     and every dependency of i is already published but not yet released
//     (release happens only after ALL dependents publish, i included).
//     So peak >= compressed[i] + temp[i] + sum(final[d] for d in deps(i)).
//     Shared deps are summed once because deps(i) is a set.
//  2. At the end of the run exactly the outputs' final bytes remain, and
//     the peak is at least the usage at that instant:
//     peak >= sum(final[o] for o in outputs).
func (p *Planner) LowerBound() int64 {
	lb := int64(0)
	for i := 0; i < p.n; i++ {
		need := p.compressed[i] + p.temp[i]
		for _, d := range p.deps[i] {
			need += p.final[d]
		}
		if need > lb {
			lb = need
		}
	}
	var end int64
	for i := 0; i < p.n; i++ {
		if p.output[i] {
			end += p.final[i]
		}
	}
	if end > lb {
		lb = end
	}
	return lb
}
