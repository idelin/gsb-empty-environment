package sercheck

import "fmt"

// DefaultEnumLimit bounds the serial-execution enumeration performed by
// Check. 10 transactions is 3.6M permutations worst case, pruned
// aggressively by partial simulation.
const DefaultEnumLimit = 10

// Report combines the dependency-graph verdict with the finite serial
// execution enumeration used to cross-check it.
type Report struct {
	Graph  *Graph
	Cycles []Cycle

	// Serializable is the graph verdict: no dependency cycles.
	Serializable bool

	// Enumerated reports whether the serial enumeration ran (it is
	// skipped when the committed transaction count exceeds the limit).
	Enumerated bool
	// Witness is a serial order reproducing every observed read, if one
	// was found.
	Witness []TxnID
	// WitnessFound reports whether enumeration found such an order.
	WitnessFound bool
	// Consistent reports whether the two methods agree:
	// graph-acyclic iff a serial witness exists. A mismatch indicates
	// either a bug or a history that is view-serializable but not
	// conflict-serializable (e.g. via blind writes).
	Consistent bool
}

// Check builds the dependency graph, enumerates serialization cycles,
// and cross-checks the verdict with a finite enumeration of serial
// executions over at most enumLimit committed transactions (pass
// DefaultEnumLimit if unsure; 0 disables enumeration).
func Check(h *History, enumLimit int) *Report {
	g := h.Graph()
	cycles := g.Cycles()
	rep := &Report{
		Graph:        g,
		Cycles:       cycles,
		Serializable: len(cycles) == 0,
	}
	if enumLimit > 0 {
		order, ok, err := h.EnumerateSerial(enumLimit)
		if err == nil {
			rep.Enumerated = true
			rep.Witness = order
			rep.WitnessFound = ok
			rep.Consistent = rep.Serializable == ok
		}
	}
	return rep
}

func (r *Report) String() string {
	s := fmt.Sprintf("serializable: %v\n", r.Serializable)
	for _, c := range r.Cycles {
		s += fmt.Sprintf("cycle: %s\n", c)
		for _, hop := range c.Hops {
			for _, e := range hop.Edges {
				for _, rd := range e.Reads {
					s += fmt.Sprintf("  evidence %s: %s\n", e, rd)
				}
				for _, w := range e.Writes {
					s += fmt.Sprintf("  evidence %s: %s\n", e, w)
				}
			}
		}
	}
	if r.Enumerated {
		if r.WitnessFound {
			s += fmt.Sprintf("serial witness: %v\n", r.Witness)
		} else {
			s += "serial witness: none\n"
		}
		s += fmt.Sprintf("graph/enumeration consistent: %v\n", r.Consistent)
	}
	return s
}
