package flowbounds

import "fmt"

// CheckFlow independently verifies that flow satisfies every edge's
// [Lower, Upper] range and every node's net-inflow demand. Self-loops
// are checked against their range only; they cannot affect conservation.
func CheckFlow(n *Network, flow []int64) error {
	if err := n.Validate(); err != nil {
		return err
	}
	if len(flow) != len(n.Edges) {
		return fmt.Errorf("flowbounds: flow has %d values for %d edges", len(flow), len(n.Edges))
	}
	for i, e := range n.Edges {
		if flow[i] < e.Lower || flow[i] > e.Upper {
			return fmt.Errorf("flowbounds: edge %d flow %d outside [%d, %d]", i, flow[i], e.Lower, e.Upper)
		}
	}
	net := make([]int64, n.NumNodes)
	for i, e := range n.Edges {
		if e.From == e.To {
			continue
		}
		net[e.To] += flow[i]
		net[e.From] -= flow[i]
	}
	for v := 0; v < n.NumNodes; v++ {
		if net[v] != n.Demand[v] {
			return fmt.Errorf("flowbounds: node %d net inflow %d, demand %d", v, net[v], n.Demand[v])
		}
	}
	return nil
}

// CheckCertificate independently verifies that cert proves infeasibility:
// the set U must be a proper nonempty subset of nodes, its stored
// aggregates must match the network, and its demand must strictly exceed
// the maximum net inflow the cut can carry (UpperIn - LowerOut).
func CheckCertificate(n *Network, cert *CutCertificate) error {
	if err := n.Validate(); err != nil {
		return err
	}
	if cert == nil || len(cert.InSet) != n.NumNodes {
		return fmt.Errorf("flowbounds: certificate set must cover %d nodes", n.NumNodes)
	}
	var demandSum, upperIn, lowerOut int64
	size := 0
	for v := 0; v < n.NumNodes; v++ {
		if cert.InSet[v] {
			size++
			demandSum += n.Demand[v]
		}
	}
	if size == 0 {
		return fmt.Errorf("flowbounds: certificate set is empty")
	}
	for _, e := range n.Edges {
		switch {
		case !cert.InSet[e.From] && cert.InSet[e.To]:
			upperIn += e.Upper
		case cert.InSet[e.From] && !cert.InSet[e.To]:
			lowerOut += e.Lower
		}
	}
	if demandSum != cert.DemandSum || upperIn != cert.UpperIn || lowerOut != cert.LowerOut {
		return fmt.Errorf("flowbounds: certificate aggregates (%d, %d, %d) do not match network (%d, %d, %d)",
			cert.DemandSum, cert.UpperIn, cert.LowerOut, demandSum, upperIn, lowerOut)
	}
	if demandSum <= upperIn-lowerOut {
		return fmt.Errorf("flowbounds: certificate not violated: demand %d <= max net inflow %d",
			demandSum, upperIn-lowerOut)
	}
	return nil
}
