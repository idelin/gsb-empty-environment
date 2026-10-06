package mvsg

import (
	"fmt"
	"strings"
)

// CycleEdge is one hop of a serialization cycle with its witnessing
// dependencies.
type CycleEdge struct {
	From TxnID
	To   TxnID
	Deps []Dependency
}

// Cycle is one elementary serialization cycle with operation evidence and
// the result of the serial-execution cross-check.
type Cycle struct {
	// Txns lists the cycle members in edge order; the last links back to
	// the first.
	Txns []TxnID
	// Edges holds one entry per hop, parallel to Txns.
	Edges []CycleEdge
	// Verified is true when enumerating all serial executions of the cycle
	// members confirmed that none reproduces the observed reads and version
	// order.
	Verified bool
	// PermutationsChecked is the number of serial orders simulated.
	PermutationsChecked int
}

func (c *Cycle) String() string {
	var b strings.Builder
	for i, id := range c.Txns {
		if i > 0 {
			b.WriteString(" -> ")
		}
		b.WriteString(string(id))
	}
	if len(c.Txns) > 0 {
		b.WriteString(" -> ")
		b.WriteString(string(c.Txns[0]))
	}
	if c.Verified {
		fmt.Fprintf(&b, "  [verified: none of %d serial orders reproduces the observations]", c.PermutationsChecked)
	} else if c.PermutationsChecked > 0 {
		fmt.Fprintf(&b, "  [unverified: a serial order consistent with the observations exists (%d orders checked)]", c.PermutationsChecked)
	} else {
		b.WriteString("  [unverified: cycle too large for serial-order enumeration]")
	}
	for _, e := range c.Edges {
		fmt.Fprintf(&b, "\n  %s -> %s:", e.From, e.To)
		for _, d := range e.Deps {
			b.WriteString("\n    ")
			b.WriteString(d.String())
		}
	}
	return b.String()
}

// Report is the outcome of Analyze.
type Report struct {
	Graph  *Graph
	Cycles []*Cycle
}

// Serializable reports whether the dependency graph is acyclic.
func (r *Report) Serializable() bool { return len(r.Cycles) == 0 }

func (r *Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d committed transactions, %d dependencies",
		len(r.Graph.Txns), len(r.Graph.Deps))
	if r.Serializable() {
		b.WriteString("\nserializable: no dependency cycles")
	} else {
		fmt.Fprintf(&b, "\nnot serializable: %d serialization cycle(s)", len(r.Cycles))
		for i, c := range r.Cycles {
			fmt.Fprintf(&b, "\ncycle #%d: %s", i+1, c)
		}
	}
	for _, w := range r.Graph.Warnings {
		b.WriteString("\nwarning: ")
		b.WriteString(w)
	}
	return b.String()
}

// Analyze builds the dependency graph of h, enumerates every elementary
// serialization cycle with its operation evidence, and cross-checks each
// cycle by enumerating the serial executions of its members.
func Analyze(h *History) *Report {
	g := BuildGraph(h)
	rep := &Report{Graph: g}

	txns := map[TxnID]*Transaction{}
	for _, t := range h.Txns {
		if t != nil && t.Committed {
			txns[t.ID] = t
		}
	}

	for _, cyc := range findElementaryCycles(g.Txns, g.neighbors()) {
		c := &Cycle{Txns: cyc}
		for i := 0; i < len(cyc); i++ {
			from := cyc[i]
			to := cyc[(i+1)%len(cyc)]
			c.Edges = append(c.Edges, CycleEdge{From: from, To: to, Deps: g.Between(from, to)})
		}
		c.Verified, c.PermutationsChecked = verifyCycle(g, txns, cyc)
		rep.Cycles = append(rep.Cycles, c)
	}
	return rep
}
