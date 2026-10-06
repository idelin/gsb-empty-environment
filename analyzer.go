package posetmatch

import "fmt"

// Analyzer holds several independent cases and runs pattern queries across
// all of them.
type Analyzer struct {
	order []string
	cases map[string]*Case
}

// NewAnalyzer returns an empty analyzer.
func NewAnalyzer() *Analyzer {
	return &Analyzer{cases: make(map[string]*Case)}
}

// Add registers a case. Names must be unique.
func (a *Analyzer) Add(c *Case) error {
	if _, ok := a.cases[c.Name()]; ok {
		return fmt.Errorf("posetmatch: duplicate case %q", c.Name())
	}
	a.cases[c.Name()] = c
	a.order = append(a.order, c.Name())
	return nil
}

// Case looks up a case by name.
func (a *Analyzer) Case(name string) (*Case, bool) {
	c, ok := a.cases[name]
	return c, ok
}

// Names lists case names in registration order.
func (a *Analyzer) Names() []string { return append([]string(nil), a.order...) }

// AnalyzeAll runs the pattern query on every case and returns each case's
// evidence keyed by case name.
func (a *Analyzer) AnalyzeAll(pattern []Label) map[string]*Evidence {
	out := make(map[string]*Evidence, len(a.cases))
	for _, name := range a.order {
		out[name] = a.cases[name].Match(pattern)
	}
	return out
}
