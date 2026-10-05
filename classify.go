package main

// Domain is the finite input space: a list of candidate values per input
// variable, enumerated lazily as a cartesian product.
type Domain struct {
	Sets  [][]int8
	total int64
}

func NewDomain(sets [][]int8) Domain {
	total := int64(1)
	for _, s := range sets {
		total *= int64(len(s))
	}
	return Domain{Sets: sets, total: total}
}

func (d Domain) Total() int64 { return d.total }

// Fill writes the idx-th input vector into buf (odometer order, last
// variable varies fastest).
func (d Domain) Fill(idx int64, buf []int8) {
	for i := len(d.Sets) - 1; i >= 0; i-- {
		s := d.Sets[i]
		buf[i] = s[idx%int64(len(s))]
		idx /= int64(len(s))
	}
}

type outcome struct {
	val interface{}
	err error
}

func runOutcome(p *Program, in []int8) outcome {
	v, err := Run(p, in)
	return outcome{v, err}
}

func (o outcome) differs(q outcome) bool {
	if (o.err == nil) != (q.err == nil) {
		return true
	}
	return o.err == nil && o.val != q.val
}

// Kill is a mutant distinguished by a test-suite input.
type Kill struct {
	M     Mutant
	Input []int8
}

// Surv is a mutant no test kills, with a domain input proving it is not
// equivalent to the original.
type Surv struct {
	M              Mutant
	Counterexample []int8
}

// Report partitions all mutants into killed / equivalent / survived.
type Report struct {
	Killed     []Kill
	Equivalent []Mutant
	Survived   []Surv
}

// Classify runs each mutant against the test suite first; mutants no test
// kills are then compared exhaustively against the original over dom.
func Classify(orig *Program, ms []Mutant, tests [][]int8, dom Domain) *Report {
	rep := &Report{}
	for _, m := range ms {
		killed := false
		for _, t := range tests {
			if runOutcome(orig, t).differs(runOutcome(m.Prog, t)) {
				in := append([]int8(nil), t...)
				rep.Killed = append(rep.Killed, Kill{M: m, Input: in})
				killed = true
				break
			}
		}
		if killed {
			continue
		}
		buf := make([]int8, len(orig.Inputs))
		found := false
		for i := int64(0); i < dom.Total(); i++ {
			dom.Fill(i, buf)
			if runOutcome(orig, buf).differs(runOutcome(m.Prog, buf)) {
				in := append([]int8(nil), buf...)
				rep.Survived = append(rep.Survived, Surv{M: m, Counterexample: in})
				found = true
				break
			}
		}
		if !found {
			rep.Equivalent = append(rep.Equivalent, m)
		}
	}
	return rep
}
