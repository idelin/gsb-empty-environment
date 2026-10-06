// Package pdp solves small capacitated pickup-and-delivery problems (PDP)
// with a single vehicle that starts and ends at the same depot.
//
// The instance supports at most MaxRequests pickup/delivery pairs. Every
// request has a weight; pickups add to the vehicle load and deliveries
// release it, and the load may never exceed the vehicle capacity. Each
// pickup must precede its delivery. Edge costs are given as a complete,
// non-negative distance matrix.
//
// The optimal route is found by exhaustive enumeration of all legal
// pickup/delivery orders (depth-first search with precedence and capacity
// pruning), so the result is jointly optimal over all requests and is
// never decomposed into independent per-request shortest paths. Ties in
// total cost are broken by the lexicographically smallest sequence of
// visited point ids (start point, every stop node in order, final depot).
package pdp

import (
	"errors"
	"fmt"
)

// MaxRequests is the maximum number of pickup/delivery pairs supported.
const MaxRequests = 6

// Instance describes the vehicle, depot and distance matrix.
type Instance struct {
	Depot    int     // start and end point of the vehicle
	Capacity int     // maximum load the vehicle can carry
	Dist     [][]int // complete non-negative cost matrix, Dist[i][j] = cost i->j
}

// Request is a single pickup/delivery pair.
type Request struct {
	ID       int // unique request id
	Pickup   int // pickup point id
	Delivery int // delivery point id
	Weight   int // load units occupied between pickup and delivery
}

// EventKind identifies what happens at a stop.
type EventKind int

const (
	PickupEvent   EventKind = iota // load the request
	DeliveryEvent                  // deliver the request, releasing capacity
	ReturnEvent                    // deliver a cancelled request to its return point
)

func (k EventKind) String() string {
	switch k {
	case PickupEvent:
		return "pickup"
	case DeliveryEvent:
		return "delivery"
	case ReturnEvent:
		return "return"
	}
	return fmt.Sprintf("EventKind(%d)", int(k))
}

// Stop is one visit on a route.
type Stop struct {
	Node      int       // point id visited
	Kind      EventKind // what happens here
	ReqID     int       // request served
	LoadAfter int       // vehicle load immediately after this stop
}

// Solution is an optimal route together with its load trajectory.
type Solution struct {
	Cost  int    // total travel cost of the remaining route
	Nodes []int  // visited point id sequence: start, every stop node, final depot
	Stops []Stop // one entry per pickup/delivery/return event
	Loads []int  // load trajectory: load before any stop, then after each stop
}

// Validate checks the instance for structural sanity.
func (in Instance) Validate() error {
	n := len(in.Dist)
	if n == 0 {
		return errors.New("pdp: empty distance matrix")
	}
	for i, row := range in.Dist {
		if len(row) != n {
			return fmt.Errorf("pdp: distance row %d has length %d, want %d", i, len(row), n)
		}
		for j, d := range row {
			if d < 0 {
				return fmt.Errorf("pdp: negative distance at (%d,%d)", i, j)
			}
		}
	}
	if in.Depot < 0 || in.Depot >= n {
		return fmt.Errorf("pdp: depot %d out of range [0,%d)", in.Depot, n)
	}
	if in.Capacity < 0 {
		return fmt.Errorf("pdp: negative capacity %d", in.Capacity)
	}
	return nil
}

func (in Instance) validateRequests(reqs []Request) error {
	if len(reqs) > MaxRequests {
		return fmt.Errorf("pdp: %d requests exceed the maximum of %d", len(reqs), MaxRequests)
	}
	seen := make(map[int]bool, len(reqs))
	for _, r := range reqs {
		if seen[r.ID] {
			return fmt.Errorf("pdp: duplicate request id %d", r.ID)
		}
		seen[r.ID] = true
		if r.Pickup < 0 || r.Pickup >= len(in.Dist) {
			return fmt.Errorf("pdp: request %d pickup point %d out of range", r.ID, r.Pickup)
		}
		if r.Delivery < 0 || r.Delivery >= len(in.Dist) {
			return fmt.Errorf("pdp: request %d delivery point %d out of range", r.ID, r.Delivery)
		}
		if r.Weight < 0 {
			return fmt.Errorf("pdp: request %d has negative weight %d", r.ID, r.Weight)
		}
		if r.Weight > in.Capacity {
			return fmt.Errorf("pdp: request %d weight %d exceeds capacity %d", r.ID, r.Weight, in.Capacity)
		}
	}
	return nil
}

// task is the solver-internal view of one request's remaining work.
type task struct {
	reqID    int
	pickup   int // pickup node, or -1 if the request is already onboard
	drop     int // node where the request leaves the vehicle
	dropKind EventKind
	weight   int
}

// Solve computes an optimal route serving all requests from the depot and
// back, jointly over all requests.
func Solve(in Instance, reqs []Request) (Solution, error) {
	if err := in.Validate(); err != nil {
		return Solution{}, err
	}
	if err := in.validateRequests(reqs); err != nil {
		return Solution{}, err
	}
	tasks := make([]task, len(reqs))
	for i, r := range reqs {
		tasks[i] = task{reqID: r.ID, pickup: r.Pickup, drop: r.Delivery, dropKind: DeliveryEvent, weight: r.Weight}
	}
	return solveTasks(in, tasks, in.Depot, 0), nil
}

// solveTasks enumerates every legal order of the remaining pickups and drops
// via DFS with precedence and capacity pruning, keeping the cheapest route
// (ties broken by lexicographically smallest visited point id sequence).
// load is the vehicle load already onboard at start.
func solveTasks(in Instance, tasks []task, start, load int) Solution {
	m := len(tasks)
	state := make([]int, m) // 0 = needs pickup, 1 = onboard, 2 = done
	remaining := 0
	for i, t := range tasks {
		if t.pickup < 0 {
			state[i] = 1
		}
		remaining++
	}

	best := Solution{Cost: -1}
	stops := make([]Stop, 0, 2*m)
	nodes := []int{start}
	loads := []int{load}

	var dfs func(node, curLoad, cost int)
	dfs = func(node, curLoad, cost int) {
		if remaining == 0 {
			total := cost + in.Dist[node][in.Depot]
			cand := append(append([]int{}, nodes...), in.Depot)
			if best.Cost < 0 || total < best.Cost || (total == best.Cost && lexLess(cand, best.Nodes)) {
				best.Cost = total
				best.Nodes = cand
				best.Stops = append([]Stop{}, stops...)
				best.Loads = append([]int{}, loads...)
			}
			return
		}
		for i := range tasks {
			t := tasks[i]
			switch state[i] {
			case 0: // pickup still needed
				if curLoad+t.weight > in.Capacity {
					continue
				}
				state[i] = 1
				stops = append(stops, Stop{Node: t.pickup, Kind: PickupEvent, ReqID: t.reqID, LoadAfter: curLoad + t.weight})
				nodes = append(nodes, t.pickup)
				loads = append(loads, curLoad+t.weight)
				dfs(t.pickup, curLoad+t.weight, cost+in.Dist[node][t.pickup])
				stops = stops[:len(stops)-1]
				nodes = nodes[:len(nodes)-1]
				loads = loads[:len(loads)-1]
				state[i] = 0
			case 1: // onboard, drop it (delivery or return)
				state[i] = 2
				remaining--
				stops = append(stops, Stop{Node: t.drop, Kind: t.dropKind, ReqID: t.reqID, LoadAfter: curLoad - t.weight})
				nodes = append(nodes, t.drop)
				loads = append(loads, curLoad-t.weight)
				dfs(t.drop, curLoad-t.weight, cost+in.Dist[node][t.drop])
				stops = stops[:len(stops)-1]
				nodes = nodes[:len(nodes)-1]
				loads = loads[:len(loads)-1]
				remaining++
				state[i] = 1
			}
		}
	}
	dfs(start, load, 0)
	return best
}

func lexLess(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

func copySolution(sol Solution) Solution {
	out := sol
	out.Nodes = append([]int{}, sol.Nodes...)
	out.Stops = append([]Stop{}, sol.Stops...)
	out.Loads = append([]int{}, sol.Loads...)
	return out
}
