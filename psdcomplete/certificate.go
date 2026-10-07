package psdcomplete

import (
	"fmt"
	"strings"
)

// Certificate proves that an assignment is positive semidefinite. It lists
// the exact determinant of every principal submatrix (all of which must be
// >= 0) together with an independent exact-elimination (LDL^T) check.
type Certificate struct {
	Minors      []MinorCert
	Elimination LDLCert
}

func buildCertificate(m Matrix) *Certificate {
	return &Certificate{
		Minors:      AllPrincipalMinors(m),
		Elimination: VerifyLDL(m),
	}
}

// Valid reports whether every principal minor in the certificate is
// non-negative and the independent elimination check agrees.
func (c *Certificate) Valid() bool {
	if !c.Elimination.PSD {
		return false
	}
	for _, mc := range c.Minors {
		if mc.Det.Sign() < 0 {
			return false
		}
	}
	return true
}

func (c *Certificate) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "PSD certificate: %d principal minors (all must be >= 0):\n", len(c.Minors))
	for _, mc := range c.Minors {
		fmt.Fprintf(&b, "  det A[%s] = %s >= 0\n", formatSubset(mc.Subset), ratString(mc.Det))
	}
	fmt.Fprintf(&b, "independent check: %s\n", c.Elimination.String())
	return b.String()
}

// String renders the full result: optimal assignment, trace, certificate,
// and a summary of the pruning evidence.
func (r *Result) String() string {
	var b strings.Builder
	if !r.OK {
		fmt.Fprintf(&b, "no feasible PSD assignment exists\n")
	} else {
		fmt.Fprintf(&b, "optimal assignment (minimal trace %s):\n", ratString(r.Trace))
		for i := 0; i < r.N; i++ {
			row := make([]string, r.N)
			for j := 0; j < r.N; j++ {
				row[j] = ratString(r.Matrix[i][j])
			}
			fmt.Fprintf(&b, "  [%s]\n", strings.Join(row, "  "))
		}
		fmt.Fprintf(&b, "\n%s", r.Certificate.String())
	}
	fmt.Fprintf(&b, "search stats: %d nodes expanded, %d leaves evaluated, %d branches pruned",
		r.NodesExpanded, r.LeavesEvaluated, r.PruneTotal)
	if r.PruneTotal > len(r.PruneLog) {
		fmt.Fprintf(&b, " (first %d prune events shown)", len(r.PruneLog))
	}
	b.WriteString("\n")
	for _, ev := range r.PruneLog {
		fmt.Fprintf(&b, "  prune: %s\n", ev.String())
	}
	return b.String()
}
