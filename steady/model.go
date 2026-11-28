package steady

import "time"

// FailureCurve maps a node's offered load (requests per second) to the
// failure probability of one physical attempt. Implementations must be
// non-decreasing past SoftLoad so the fixed-point iteration is well posed.
type FailureCurve interface {
	PFail(load float64) float64
}

// SoftThresholdCurve keeps a BaseFailure rate below SoftLoad, then raises
// the failure rate linearly with load until HardLoad where it reaches
// HardFailure, after which it stays at HardFailure. The hard capacity of a
// node is enforced separately by NodeConfig.Capacity.
type SoftThresholdCurve struct {
	BaseFailure float64
	SoftLoad    float64
	HardLoad    float64
	HardFailure float64
}

// PFail evaluates the piecewise-linear response curve.
func (c SoftThresholdCurve) PFail(load float64) float64 {
	switch {
	case load <= c.SoftLoad:
		return c.BaseFailure
	case load >= c.HardLoad:
		return c.HardFailure
	default:
		t := (load - c.SoftLoad) / (c.HardLoad - c.SoftLoad)
		return c.BaseFailure + t*(c.HardFailure-c.BaseFailure)
	}
}

// NodeConfig describes one service node of the call DAG.
type NodeConfig struct {
	Name string
	// Capacity is the hard capacity in requests/second. A steady load above
	// Capacity marks the node as an avalanche node.
	Capacity float64
	// MeanServiceTime converts arrival rate to outstanding concurrency via
	// Little's law; the runtime retry budget uses the same conversion.
	MeanServiceTime time.Duration
	// MinServiceTime feeds the deadline pre-check, identical to the runtime
	// EdgeConfig.MinProcessing.
	MinServiceTime time.Duration
	// Curve is the load -> failure probability response.
	Curve FailureCurve
}

// EdgeConfig describes retry/budget/hedging behaviour on one directed edge.
type EdgeConfig struct {
	From string
	To   string
	// Fanout is the number of downstream calls per processed upstream call
	// (1 for a plain chain, N for scatter-gather fan-out).
	Fanout float64
	// MaxAttempts includes the initial attempt (3 retries => 4).
	MaxAttempts int
	// MaxHedges is the number of racing hedged branches.
	MaxHedges int
	// HedgeDelay is the spacing of hedged branch dispatch.
	HedgeDelay time.Duration
	// BudgetRatio is the retry budget ratio; <0 or +Inf means unlimited.
	BudgetRatio float64
	// MinRetryConcurrency is the low-traffic budget floor.
	MinRetryConcurrency int
	// RequestDeadline is the edge-relative end-to-end deadline; zero means
	// no deadline. It bounds effective attempts exactly like the runtime.
	RequestDeadline time.Duration
	// BackoffBase feeds the deadline-bounded attempt calculation.
	BackoffBase time.Duration
}

// Topology is the complete solver input.
type Topology struct {
	Nodes   []NodeConfig
	Edges   []EdgeConfig
	Ingress map[string]float64
}

// indexedEdge is an EdgeConfig with resolved node indices.
type indexedEdge struct {
	EdgeConfig
	u, v int
}

// indexedTopology is the preprocessed, lookup-friendly representation.
type indexedTopology struct {
	nodes     []NodeConfig
	nodeIndex map[string]int
	edges     []indexedEdge
	edgeIndex map[[2]string]int
	out       [][]int // node -> edge indices leaving the node
	in        [][]int // node -> edge indices entering the node
	order     []int   // topological order
	ingress   []float64
}

func indexTopology(t *Topology) (*indexedTopology, error) {
	it := &indexedTopology{
		nodes:     append([]NodeConfig(nil), t.Nodes...),
		nodeIndex: map[string]int{},
		edgeIndex: map[[2]string]int{},
		ingress:   make([]float64, len(t.Nodes)),
	}
	for i, n := range it.nodes {
		if _, dup := it.nodeIndex[n.Name]; dup {
			return nil, &TopologyError{Msg: "duplicate node: " + n.Name}
		}
		it.nodeIndex[n.Name] = i
	}
	for name, r := range t.Ingress {
		i, ok := it.nodeIndex[name]
		if !ok {
			return nil, &TopologyError{Msg: "ingress on unknown node: " + name}
		}
		it.ingress[i] = r
	}
	it.out = make([][]int, len(it.nodes))
	it.in = make([][]int, len(it.nodes))
	for _, e := range t.Edges {
		u, ok1 := it.nodeIndex[e.From]
		v, ok2 := it.nodeIndex[e.To]
		if !ok1 || !ok2 {
			return nil, &TopologyError{Msg: "edge references unknown node: " + e.From + "->" + e.To}
		}
		key := [2]string{e.From, e.To}
		if _, dup := it.edgeIndex[key]; dup {
			return nil, &TopologyError{Msg: "duplicate edge: " + e.From + "->" + e.To}
		}
		idx := len(it.edges)
		it.edgeIndex[key] = idx
		it.edges = append(it.edges, indexedEdge{EdgeConfig: e, u: u, v: v})
		it.out[u] = append(it.out[u], idx)
		it.in[v] = append(it.in[v], idx)
	}
	order, err := topoOrder(len(it.nodes), it.out, it.edges)
	if err != nil {
		return nil, err
	}
	it.order = order
	return it, nil
}

func topoOrder(n int, out [][]int, edges []indexedEdge) ([]int, error) {
	indeg := make([]int, n)
	for u := range out {
		for _, ei := range out[u] {
			indeg[edges[ei].v]++
		}
	}
	var queue []int
	for i := 0; i < n; i++ {
		if indeg[i] == 0 {
			queue = append(queue, i)
		}
	}
	order := make([]int, 0, n)
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		order = append(order, u)
		for _, ei := range out[u] {
			v := edges[ei].v
			indeg[v]--
			if indeg[v] == 0 {
				queue = append(queue, v)
			}
		}
	}
	if len(order) != n {
		return nil, &TopologyError{Msg: "topology contains a cycle; only DAGs are supported"}
	}
	return order, nil
}

// TopologyError reports malformed solver input.
type TopologyError struct{ Msg string }

func (e *TopologyError) Error() string { return "steady: " + e.Msg }
