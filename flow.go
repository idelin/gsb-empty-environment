// Package flowbounds solves feasible-flow problems on directed networks
// where every edge carries an integer lower and upper bound on its flow
// and every node carries a net-inflow demand.
//
// Model: for each node v, inflow(v) - outflow(v) must equal Demand[v].
// Demands must sum to zero. Self-loops do not participate in node
// conservation (their flow enters and leaves the same node) and are only
// constrained by their own [Lower, Upper] range. Parallel edges and
// isolated nodes are supported.
package flowbounds

import "errors"

// Edge is a directed arc from From to To whose flow f must satisfy
// Lower <= f <= Upper. An edge with From == To is a self-loop.
type Edge struct {
	From, To     int
	Lower, Upper int64
}

// Network describes a feasible-flow instance.
type Network struct {
	NumNodes int
	Edges    []Edge
	// Demand[v] is the required net inflow of node v: inflow - outflow.
	// Must have length NumNodes and sum to zero.
	Demand []int64
}

// CutCertificate witnesses infeasibility: a nonempty set U of nodes whose
// net demand exceeds the maximum possible net inflow across the cut, i.e.
// DemandSum > UpperIn - LowerOut where UpperIn sums the upper bounds of
// edges entering U and LowerOut sums the lower bounds of edges leaving U.
type CutCertificate struct {
	InSet     []bool // InSet[v] reports whether node v belongs to U
	DemandSum int64  // sum of Demand over U
	UpperIn   int64  // sum of Upper over edges from V\U into U
	LowerOut  int64  // sum of Lower over edges from U into V\U
}

// Result is the outcome of Solve: either a feasible flow (one value per
// edge, in input order) or a cut certificate of infeasibility.
type Result struct {
	Feasible bool
	Flow     []int64         // set iff Feasible
	Cut      *CutCertificate // set iff !Feasible
}

// Validate checks structural requirements of the instance.
func (n *Network) Validate() error {
	if n.NumNodes < 0 {
		return errors.New("flowbounds: negative node count")
	}
	if len(n.Demand) != n.NumNodes {
		return errors.New("flowbounds: demand length must equal NumNodes")
	}
	var sum int64
	for _, d := range n.Demand {
		sum += d
	}
	if sum != 0 {
		return errors.New("flowbounds: demands must sum to zero")
	}
	for _, e := range n.Edges {
		if e.From < 0 || e.From >= n.NumNodes || e.To < 0 || e.To >= n.NumNodes {
			return errors.New("flowbounds: edge endpoint out of range")
		}
		if e.Lower > e.Upper {
			return errors.New("flowbounds: edge has Lower > Upper")
		}
	}
	return nil
}

// Solve returns a feasible flow honouring all bounds and demands, or a
// cut certificate proving infeasibility.
func Solve(n *Network) (Result, error) {
	if err := n.Validate(); err != nil {
		return Result{}, err
	}

	// balance[v] is the net inflow still required at v after committing
	// every edge's lower bound: sum f' in - sum f' out = balance[v],
	// where f' = f - Lower ranges over [0, Upper-Lower]. Self-loops
	// cancel out of conservation and are handled separately.
	balance := make([]int64, n.NumNodes)
	copy(balance, n.Demand)
	for _, e := range n.Edges {
		if e.From == e.To {
			continue
		}
		balance[e.To] -= e.Lower
		balance[e.From] += e.Lower
	}

	source := n.NumNodes
	sink := n.NumNodes + 1
	d := newDinic(n.NumNodes + 2)

	// arcIndex[i] is the Dinic arc index carrying edge i's residual
	// flow, or -1 for self-loops.
	arcIndex := make([]int, len(n.Edges))
	for i, e := range n.Edges {
		if e.From == e.To {
			arcIndex[i] = -1
			continue
		}
		arcIndex[i] = d.addEdge(e.From, e.To, e.Upper-e.Lower)
	}

	// Nodes still needing net inflow (balance > 0) drain to the sink;
	// nodes with net outflow (balance < 0) are fed by the source. The
	// instance is feasible iff the max flow saturates every source arc.
	var total int64
	for v := 0; v < n.NumNodes; v++ {
		if balance[v] > 0 {
			d.addEdge(v, sink, balance[v])
			total += balance[v]
		} else if balance[v] < 0 {
			d.addEdge(source, v, -balance[v])
		}
	}

	if d.maxFlow(source, sink) == total {
		flow := make([]int64, len(n.Edges))
		for i, e := range n.Edges {
			if arcIndex[i] < 0 {
				flow[i] = e.Lower // self-loop: any in-range value works
			} else {
				flow[i] = e.Lower + d.flowOn(arcIndex[i])
			}
		}
		return Result{Feasible: true, Flow: flow}, nil
	}

	// Infeasible: the T-side U of the minimum cut violates
	// Demand(U) > UpperIn(U) - LowerOut(U).
	reach := d.reachableFrom(source)
	inSet := make([]bool, n.NumNodes)
	for v := 0; v < n.NumNodes; v++ {
		inSet[v] = !reach[v]
	}
	cert := &CutCertificate{InSet: inSet}
	for v := 0; v < n.NumNodes; v++ {
		if inSet[v] {
			cert.DemandSum += n.Demand[v]
		}
	}
	for _, e := range n.Edges {
		switch {
		case !inSet[e.From] && inSet[e.To]:
			cert.UpperIn += e.Upper
		case inSet[e.From] && !inSet[e.To]:
			cert.LowerOut += e.Lower
		}
	}
	return Result{Feasible: false, Cut: cert}, nil
}
