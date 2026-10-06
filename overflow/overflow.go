// Package overflow simulates liquid flowing through a network of
// capacity-limited containers connected by a directed acyclic overflow
// graph.
//
// Each tick, every container first receives its configured inflow. Any
// amount exceeding a container's capacity then overflows along the
// outgoing edges: each edge carries a rational fraction of the excess to
// a downstream container, and the unallocated remainder is recorded as
// discharge. Overflow is propagated along the topological order of the
// DAG until every container is within capacity (a stable state).
//
// All arithmetic uses exact rational numbers (math/big.Rat), so total
// liquid (levels + discharge) is conserved exactly across ticks.
package overflow

import (
	"fmt"
	"math/big"
)

// Edge is a directed overflow link. Fraction is the rational share of
// the source container's excess routed to the target container.
type Edge struct {
	To       int
	Fraction *big.Rat
}

// Network is a set of containers plus a DAG of overflow edges.
type Network struct {
	Capacities []*big.Rat
	Levels     []*big.Rat
	Inflows    []*big.Rat // liquid added to each container every tick
	Edges      [][]Edge
	Discharged []*big.Rat // cumulative discharge per container

	topo []int // topological order of containers
}

// Rat builds an exact rational num/den.
func Rat(num, den int64) *big.Rat {
	return big.NewRat(num, den)
}

// NewNetwork validates the model and returns a ready-to-run Network.
// containers is the number of containers; every container starts empty.
func NewNetwork(containers int, capacities, inflows []*big.Rat, edges [][]Edge) (*Network, error) {
	if containers < 0 {
		return nil, fmt.Errorf("overflow: negative container count %d", containers)
	}
	if len(capacities) != containers || len(inflows) != containers || len(edges) != containers {
		return nil, fmt.Errorf("overflow: capacities, inflows and edges must each have length %d", containers)
	}
	n := &Network{
		Capacities: make([]*big.Rat, containers),
		Levels:     make([]*big.Rat, containers),
		Inflows:    make([]*big.Rat, containers),
		Edges:      make([][]Edge, containers),
		Discharged: make([]*big.Rat, containers),
	}
	one := Rat(1, 1)
	for i := 0; i < containers; i++ {
		if capacities[i] == nil || capacities[i].Sign() < 0 {
			return nil, fmt.Errorf("overflow: container %d has nil or negative capacity", i)
		}
		if inflows[i] == nil || inflows[i].Sign() < 0 {
			return nil, fmt.Errorf("overflow: container %d has nil or negative inflow", i)
		}
		n.Capacities[i] = new(big.Rat).Set(capacities[i])
		n.Inflows[i] = new(big.Rat).Set(inflows[i])
		n.Levels[i] = new(big.Rat)
		n.Discharged[i] = new(big.Rat)

		sum := new(big.Rat)
		for _, e := range edges[i] {
			if e.To < 0 || e.To >= containers {
				return nil, fmt.Errorf("overflow: edge %d -> %d out of range", i, e.To)
			}
			if e.Fraction == nil || e.Fraction.Sign() < 0 || e.Fraction.Cmp(one) > 0 {
				return nil, fmt.Errorf("overflow: edge %d -> %d fraction must be in [0,1]", i, e.To)
			}
			sum.Add(sum, e.Fraction)
			n.Edges[i] = append(n.Edges[i], Edge{To: e.To, Fraction: new(big.Rat).Set(e.Fraction)})
		}
		if sum.Cmp(one) > 0 {
			return nil, fmt.Errorf("overflow: container %d overflow fractions sum to %s > 1", i, sum)
		}
	}

	topo, err := topoSort(n.Edges)
	if err != nil {
		return nil, err
	}
	n.topo = topo
	return n, nil
}

func topoSort(edges [][]Edge) ([]int, error) {
	n := len(edges)
	indeg := make([]int, n)
	for _, outs := range edges {
		for _, e := range outs {
			indeg[e.To]++
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
		for _, e := range edges[u] {
			indeg[e.To]--
			if indeg[e.To] == 0 {
				queue = append(queue, e.To)
			}
		}
	}
	if len(order) != n {
		return nil, fmt.Errorf("overflow: graph must be a DAG, found a cycle")
	}
	return order, nil
}

// Tick performs one simulation step: add each container's inflow, then
// propagate overflow along the DAG until the network is stable.
func (n *Network) Tick() {
	for i := range n.Levels {
		n.Levels[i].Add(n.Levels[i], n.Inflows[i])
	}
	n.Settle()
}

// Settle pushes overflow downstream in topological order until no
// container exceeds its capacity. Because the graph is acyclic this
// terminates; the loop simply re-checks stability.
func (n *Network) Settle() {
	for {
		moved := false
		for _, u := range n.topo {
			if n.Levels[u].Cmp(n.Capacities[u]) <= 0 {
				continue
			}
			excess := new(big.Rat).Sub(n.Levels[u], n.Capacities[u])
			n.Levels[u].Set(n.Capacities[u])
			routed := new(big.Rat)
			for _, e := range n.Edges[u] {
				amount := new(big.Rat).Mul(excess, e.Fraction)
				n.Levels[e.To].Add(n.Levels[e.To], amount)
				routed.Add(routed, amount)
			}
			// Unallocated remainder is discharged.
			discharge := new(big.Rat).Sub(excess, routed)
			n.Discharged[u].Add(n.Discharged[u], discharge)
			moved = true
		}
		if !moved {
			return
		}
	}
}

// Total returns the exact total liquid in the system: current levels
// plus everything ever discharged. It grows by exactly the sum of
// inflows each tick.
func (n *Network) Total() *big.Rat {
	total := new(big.Rat)
	for i := range n.Levels {
		total.Add(total, n.Levels[i])
		total.Add(total, n.Discharged[i])
	}
	return total
}

// TotalDischarged returns the exact cumulative discharge across all
// containers.
func (n *Network) TotalDischarged() *big.Rat {
	total := new(big.Rat)
	for _, d := range n.Discharged {
		total.Add(total, d)
	}
	return total
}
