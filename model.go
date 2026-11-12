package netsec

import "math/big"

// Program is a finite probabilistic program: its statements contain only
// bounded loops, rational branches and finite state, so every run terminates.
type Program struct {
	Name string
	Body []Stmt

	compiled *program
}

// Observation is one emitted (Label, value) pair, in emission order.
type Observation struct {
	Label string
	Value bool
}

// Outcome is an observable program result. A normal run has Exception == "".
// A run that hit a Fail carries the exception name and the observations made
// before the failure.
type Outcome struct {
	Observations []Observation
	Exception    string
}

// Point is one outcome together with its exact probability mass.
type Point struct {
	Outcome Outcome
	Prob    *big.Rat
}

// Distribution is the exact probability of every observable outcome. The
// probabilities sum to exactly one.
type Distribution struct {
	Points []Point
}

// ProbOf returns the exact mass of outcome, or zero when it cannot occur.
func (d *Distribution) ProbOf(outcome Outcome) *big.Rat {
	for i := range d.Points {
		if outcomesEqual(d.Points[i].Outcome, outcome) {
			return new(big.Rat).Set(d.Points[i].Prob)
		}
	}
	return new(big.Rat)
}

// FailureProb returns the total mass of outcomes carrying any exception.
func (d *Distribution) FailureProb() *big.Rat {
	total := new(big.Rat)
	for i := range d.Points {
		if d.Points[i].Outcome.Exception != "" {
			total.Add(total, d.Points[i].Prob)
		}
	}
	return total
}

func outcomesEqual(a, b Outcome) bool {
	if a.Exception != b.Exception || len(a.Observations) != len(b.Observations) {
		return false
	}
	for i := range a.Observations {
		if a.Observations[i] != b.Observations[i] {
			return false
		}
	}
	return true
}

// outcomeKey is a canonical, collision-free encoding of an Outcome.
func outcomeKey(o Outcome) string {
	buf := make([]byte, 0, 32)
	putInt := func(n int) {
		buf = append(buf, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
	putStr := func(s string) {
		putInt(len(s))
		buf = append(buf, s...)
	}
	putInt(len(o.Observations))
	for _, obs := range o.Observations {
		putStr(obs.Label)
		if obs.Value {
			buf = append(buf, 1)
		} else {
			buf = append(buf, 0)
		}
	}
	putStr(o.Exception)
	return string(buf)
}
