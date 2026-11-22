package timeproof

// NodeValue is one variable assignment inside a feasibility witness.
type NodeValue struct {
	Node  NodeLabel
	Value *Rat
}

// Witness is independently checkable evidence for one interval endpoint.
//
// For an upper-bound witness, Path telescopes (each edge is x_To-x_From <= w)
// from the origin to the query node and sums to Bound; Assignment is a full
// feasible assignment attaining it. For a lower-bound witness the Path is
// stored forward from the query to the origin and sums to -Bound.
type Witness struct {
	Bound        BoundKind
	KeptDomains  []string
	KeptReceipts []int
	Assignment   []NodeValue
	Path         []Edge
	BoundValue   *Rat // finite endpoint being certified
}

// BoundKind distinguishes an interval's lower and upper endpoint witnesses.
type BoundKind string

const (
	BoundLower BoundKind = "lower"
	BoundUpper BoundKind = "upper"
)

// Interval is one closed rational interval of possible true times. A nil
// endpoint means unbounded (-/+ infinity).
type Interval struct {
	Lo        *Rat
	Hi        *Rat
	LoWitness *Witness
	HiWitness *Witness
}

// maskResult is the solved feasibility outcome for one kept-receipt mask.
type maskResult struct {
	feasible bool
	interval Interval
	cycle    []Edge
}

// solveMask builds and solves the constraint system for one mask.
func solveMask(q *Query, domainNames []string, kept []bool) maskResult {
	nodes, edges := buildGraph(q, kept)
	index := make(map[NodeLabel]int, len(nodes))
	for i, l := range nodes {
		index[l] = i
	}
	originIdx := index[originNode()]
	queryIdx := index[queryNode()]

	fwd := bellmanFord(nodes, edges, originIdx)
	if !fwd.feasible {
		return maskResult{feasible: false, cycle: fwd.cycle}
	}

	hasKept := false
	for _, k := range kept {
		if k {
			hasKept = true
			break
		}
	}
	if !hasKept {
		// No constraints: the true time is entirely unbounded.
		return maskResult{
			feasible: true,
			interval: Interval{},
		}
	}

	rev := bellmanFord(nodes, reverseEdges(edges), originIdx)
	if !rev.feasible {
		// Infeasibility must already appear in the forward run; guard anyway.
		return maskResult{feasible: false, cycle: rev.cycle}
	}

	upper := buildWitness(nodes, edges, index, fwd, queryIdx, originIdx, BoundUpper, q, domainNames, kept)
	lower := buildWitness(nodes, edges, index, rev, queryIdx, originIdx, BoundLower, q, domainNames, kept)

	return maskResult{
		feasible: true,
		interval: Interval{
			Lo:        ratCopy(lower.BoundValue),
			Hi:        ratCopy(upper.BoundValue),
			LoWitness: lower,
			HiWitness: upper,
		},
	}
}

// buildWitness assembles an endpoint witness from a Bellman-Ford run.
func buildWitness(
	nodes []NodeLabel,
	edges []Edge,
	index map[NodeLabel]int,
	run bfResult,
	queryIdx, originIdx int,
	bound BoundKind,
	q *Query,
	domainNames []string,
	kept []bool,
) *Witness {
	w := &Witness{Bound: bound}
	w.KeptDomains = append(w.KeptDomains, domainsOfMask(q, kept)...)
	for i, k := range kept {
		if k {
			w.KeptReceipts = append(w.KeptReceipts, i)
		}
	}

	// Assignment and tight path reconstruction depend on the bound direction.
	if bound == BoundUpper {
		// Forward run from origin: dist is a feasible assignment.
		for i, l := range nodes {
			w.Assignment = append(w.Assignment, NodeValue{Node: l, Value: ratCopy(run.dist[i])})
		}
		w.Path = pathTo(edges, index, run.pred, queryIdx, originIdx)
		w.BoundValue = ratCopy(run.dist[queryIdx])
		return w
	}

	// Lower bound: reversed run. Forward feasible value x_v = -dist_rev[v].
	for i, l := range nodes {
		w.Assignment = append(w.Assignment, NodeValue{Node: l, Value: negRat(run.dist[i])})
	}
	// Reversed predecessor chain origin -> query maps to a forward chain
	// query -> origin.
	revEdges := reverseEdges(edges)
	revPath := pathTo(revEdges, index, run.pred, queryIdx, originIdx)
	for i := len(revPath) - 1; i >= 0; i-- {
		e := revPath[i]
		w.Path = append(w.Path, Edge{From: e.To, To: e.From, Weight: ratCopy(e.Weight), Provenance: e.Provenance})
	}
	// Lower endpoint is -dist_rev[query] (dist_rev[query] <= 0 once connected).
	w.BoundValue = negRat(run.dist[queryIdx])
	return w
}

// pathTo reconstructs the edge chain from src to target using predecessors.
func pathTo(edges []Edge, index map[NodeLabel]int, pred []int, target, src int) []Edge {
	var path []Edge
	cur := target
	seen := map[int]bool{}
	for cur != src {
		if seen[cur] {
			return path
		}
		seen[cur] = true
		ei := pred[cur]
		if ei == -1 {
			break
		}
		path = append(path, edges[ei])
		cur = index[edges[ei].From]
	}
	// Reverse into src -> target order.
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// domainsOfMask returns the sorted distinct domain names retained by a mask.
func domainsOfMask(q *Query, kept []bool) []string {
	seen := map[string]bool{}
	var out []string
	for i, r := range q.Receipts {
		if kept[i] && !seen[r.Domain] {
			seen[r.Domain] = true
			out = append(out, r.Domain)
		}
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	// Avoid importing sort twice in this file; use a tiny insertion sort.
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
