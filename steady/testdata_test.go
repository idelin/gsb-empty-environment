package steady

import "time"

// avalancheTopology builds the 4-hop A->B->C->D chain used across the
// acceptance tests. D sits at a mild-overload operating point so its
// failure rate climbs under retry amplification; budgetRatio < 0 means the
// legacy unlimited-retry configuration.
func avalancheTopology(budgetRatio float64, minRetry int, maxAttempts int) *Topology {
	curves := map[string]SoftThresholdCurve{
		"A": {BaseFailure: 0.005, SoftLoad: 300, HardLoad: 600, HardFailure: 0.9},
		"B": {BaseFailure: 0.005, SoftLoad: 300, HardLoad: 600, HardFailure: 0.9},
		"C": {BaseFailure: 0.005, SoftLoad: 300, HardLoad: 600, HardFailure: 0.9},
		"D": {BaseFailure: 0.05, SoftLoad: 48, HardLoad: 78, HardFailure: 0.85},
	}
	nodes := make([]NodeConfig, 0, 4)
	for _, name := range []string{"A", "B", "C", "D"} {
		nodes = append(nodes, NodeConfig{
			Name: name, Capacity: 200,
			MeanServiceTime: 30 * time.Millisecond,
			MinServiceTime:  5 * time.Millisecond,
			Curve:           curves[name],
		})
	}
	edges := make([]EdgeConfig, 0, 3)
	for _, pair := range [][2]string{{"A", "B"}, {"B", "C"}, {"C", "D"}} {
		edges = append(edges, EdgeConfig{
			From: pair[0], To: pair[1], Fanout: 1,
			MaxAttempts: maxAttempts, MaxHedges: 0,
			BudgetRatio: budgetRatio, MinRetryConcurrency: minRetry,
			BackoffBase: time.Millisecond,
		})
	}
	return &Topology{
		Nodes: nodes, Edges: edges,
		Ingress: map[string]float64{"A": 50},
	}
}

func nodeByName(r *Result, name string) NodeResult {
	for _, n := range r.Nodes {
		if n.Name == name {
			return n
		}
	}
	return NodeResult{}
}

func simNodeByName(r *SimResult, name string) SimNodeResult {
	for _, n := range r.Nodes {
		if n.Name == name {
			return n
		}
	}
	return SimNodeResult{}
}
