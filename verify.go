package flowsolve

import "fmt"

// VerifyFlow independently checks that flow satisfies every edge bound and
// node conservation. Self-loops contribute to both inflow and outflow of
// their node and therefore cancel in conservation; only their bounds apply.
func VerifyFlow(n *Network, flow []int64) error {
	if len(flow) != len(n.Edges) {
		return fmt.Errorf("flowsolve: flow has %d values, want %d", len(flow), len(n.Edges))
	}
	balance := make([]int64, n.NumNodes)
	for i, e := range n.Edges {
		f := flow[i]
		if f < e.Lo || f > e.Hi {
			return fmt.Errorf("flowsolve: edge %d flow %d outside [%d, %d]", i, f, e.Lo, e.Hi)
		}
		balance[e.To] += f
		balance[e.From] -= f
	}
	for v := 0; v < n.NumNodes; v++ {
		if balance[v] != n.Demand[v] {
			return fmt.Errorf("flowsolve: node %d net inflow %d, want %d", v, balance[v], n.Demand[v])
		}
	}
	return nil
}

// VerifyCertificate independently checks that cert proves infeasibility:
// the set X must be non-trivial and satisfy Demand(X) < LoIn(X) - HiOut(X).
func VerifyCertificate(n *Network, cert *Certificate) error {
	if len(cert.Nodes) != n.NumNodes {
		return fmt.Errorf("flowsolve: certificate covers %d nodes, want %d", len(cert.Nodes), n.NumNodes)
	}
	recomputed := &Certificate{Nodes: cert.Nodes}
	fillCertificate(n, recomputed)
	if recomputed.Demand != cert.Demand || recomputed.LoIn != cert.LoIn || recomputed.HiOut != cert.HiOut {
		return fmt.Errorf("flowsolve: certificate aggregates inconsistent: got (%d, %d, %d), want (%d, %d, %d)",
			cert.Demand, cert.LoIn, cert.HiOut, recomputed.Demand, recomputed.LoIn, recomputed.HiOut)
	}
	if !(cert.Demand < cert.LoIn-cert.HiOut) {
		return fmt.Errorf("flowsolve: certificate inequality fails: demand %d >= loIn-hiOut %d",
			cert.Demand, cert.LoIn-cert.HiOut)
	}
	return nil
}
