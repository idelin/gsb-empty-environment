package main

import (
	"fmt"
	"math"
	"sort"
)

// Window is a half-open service time window [Open, Close]; arrival must be
// no later than Close, service starts at max(arrival, Open).
type Window struct {
	Open  int `json:"open"`
	Close int `json:"close"`
}

// Order is a single ride request: pickup then delivery by the same vehicle.
type Order struct {
	ID             string `json:"id"`
	Pickup         int    `json:"pickup"`
	Delivery       int    `json:"delivery"`
	Weight         int    `json:"weight"`
	Seats          int    `json:"seats"`
	PickupWindow   Window `json:"pickup_window"`
	DeliveryWindow Window `json:"delivery_window"`
	Service        int    `json:"service_time"`
}

// VehicleSpec describes the homogeneous fleet capacity.
type VehicleSpec struct {
	WeightCapacity int `json:"weight_capacity"`
	SeatCapacity   int `json:"seat_capacity"`
}

// Input is the planner request. Matrix[i][j] is the travel time from node i
// to node j and must satisfy the triangle inequality.
type Input struct {
	Depot   int         `json:"depot"`
	Vehicle VehicleSpec `json:"vehicle"`
	Orders  []Order     `json:"orders"`
	Matrix  [][]int     `json:"travel_time"`
}

// Stop is one visit in a vehicle route.
type Stop struct {
	Node       int    `json:"node"`
	Action     string `json:"action"` // depart-depot | pickup | deliver | return-depot
	Order      string `json:"order,omitempty"`
	Arrival    int    `json:"arrival"`
	Wait       int    `json:"wait"`
	Departure  int    `json:"departure"`
	LoadWeight int    `json:"load_weight"`
	LoadSeats  int    `json:"load_seats"`
	LegTravel  int    `json:"leg_travel"`
}

// Route is the plan of one vehicle.
type Route struct {
	Vehicle    int      `json:"vehicle"`
	Orders     []string `json:"orders"`
	Stops      []Stop   `json:"stops"`
	TravelTime int      `json:"travel_time"`
}

// Result is the optimal plan: fewest vehicles, then shortest travel time.
type Result struct {
	Vehicles        int     `json:"vehicles"`
	TotalTravelTime int     `json:"total_travel_time"`
	Routes          []Route `json:"routes"`
}

const maxOrders = 10

// Solve validates the input and computes the lexicographically optimal plan
// (min vehicles, then min total travel time).
func Solve(in *Input) (*Result, error) {
	if err := validate(in); err != nil {
		return nil, err
	}
	s := newSolver(in)
	return s.solve()
}

func validate(in *Input) error {
	if len(in.Orders) == 0 {
		return fmt.Errorf("no orders to plan")
	}
	if len(in.Orders) > maxOrders {
		return fmt.Errorf("too many orders: %d (max %d)", len(in.Orders), maxOrders)
	}
	n := len(in.Matrix)
	if n == 0 {
		return fmt.Errorf("empty travel time matrix")
	}
	for i, row := range in.Matrix {
		if len(row) != n {
			return fmt.Errorf("travel time matrix row %d has length %d, want %d", i, len(row), n)
		}
		for j, v := range row {
			if v < 0 {
				return fmt.Errorf("negative travel time at [%d][%d]", i, j)
			}
		}
	}
	if in.Depot < 0 || in.Depot >= n {
		return fmt.Errorf("depot index %d out of range [0,%d)", in.Depot, n)
	}
	if in.Vehicle.WeightCapacity < 0 || in.Vehicle.SeatCapacity < 0 {
		return fmt.Errorf("vehicle capacity must be non-negative")
	}
	for i := range in.Orders {
		o := &in.Orders[i]
		if o.ID == "" {
			o.ID = fmt.Sprintf("order-%d", i+1)
		}
		if o.Pickup < 0 || o.Pickup >= n || o.Delivery < 0 || o.Delivery >= n {
			return fmt.Errorf("order %s: node index out of range [0,%d)", o.ID, n)
		}
		if o.Weight < 0 || o.Seats < 0 || o.Service < 0 {
			return fmt.Errorf("order %s: weight, seats and service_time must be non-negative", o.ID)
		}
		if o.PickupWindow.Open > o.PickupWindow.Close || o.DeliveryWindow.Open > o.DeliveryWindow.Close {
			return fmt.Errorf("order %s: empty time window", o.ID)
		}
		if o.Weight > in.Vehicle.WeightCapacity || o.Seats > in.Vehicle.SeatCapacity {
			return fmt.Errorf("order %s can never be served: demand (%d weight, %d seats) exceeds vehicle capacity (%d, %d)",
				o.ID, o.Weight, o.Seats, in.Vehicle.WeightCapacity, in.Vehicle.SeatCapacity)
		}
	}
	// The branch-and-bound pruning (earliest-arrival checks and group-cost
	// monotonicity) is only valid when direct travel is never slower than
	// any detour, so reject matrices that break the triangle inequality.
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			for k := 0; k < n; k++ {
				if in.Matrix[i][j] > in.Matrix[i][k]+in.Matrix[k][j] {
					return fmt.Errorf("travel time matrix violates triangle inequality: t[%d][%d]=%d > t[%d][%d]+t[%d][%d]=%d; pruning bounds would be invalid",
						i, j, in.Matrix[i][j], i, k, k, j, in.Matrix[i][k]+in.Matrix[k][j])
				}
			}
		}
	}
	return nil
}

type groupCost struct {
	cost int
	ok   bool
}

type stopRef struct {
	order  int // original order index
	pickup bool
}

type solver struct {
	in    *Input
	n     int
	order []int // processing sequence: position -> original order index
	minIn []int // min incoming travel time per node (i != j)

	cache map[uint16]groupCost

	bestVehicles int
	bestCost     int
	bestGroups   []uint16
}

func newSolver(in *Input) *solver {
	n := len(in.Orders)
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	// Hardest orders first: tighter windows, then heavier demand.
	sort.Slice(order, func(a, b int) bool {
		oa, ob := &in.Orders[order[a]], &in.Orders[order[b]]
		slackA := (oa.PickupWindow.Close - oa.PickupWindow.Open) + (oa.DeliveryWindow.Close - oa.DeliveryWindow.Open)
		slackB := (ob.PickupWindow.Close - ob.PickupWindow.Open) + (ob.DeliveryWindow.Close - ob.DeliveryWindow.Open)
		if slackA != slackB {
			return slackA < slackB
		}
		return oa.Weight+oa.Seats > ob.Weight+ob.Seats
	})
	nodes := len(in.Matrix)
	minIn := make([]int, nodes)
	for j := 0; j < nodes; j++ {
		best := math.MaxInt32
		for i := 0; i < nodes; i++ {
			if i != j && in.Matrix[i][j] < best {
				best = in.Matrix[i][j]
			}
		}
		if best == math.MaxInt32 {
			best = 0
		}
		minIn[j] = best
	}
	return &solver{
		in:    in,
		n:     n,
		order: order,
		minIn: minIn,
		cache: make(map[uint16]groupCost),
	}
}

func (s *solver) solve() (*Result, error) {
	// Seed incumbent: one dedicated vehicle per order. Also proves that every
	// order is servable in isolation (the easiest case for time windows).
	s.bestVehicles = s.n
	s.bestCost = 0
	s.bestGroups = make([]uint16, s.n)
	for p := 0; p < s.n; p++ {
		gc := s.routeCost(uint16(1) << uint(p))
		if !gc.ok {
			return nil, fmt.Errorf("order %s cannot be served even by a dedicated vehicle (time windows too tight)", s.in.Orders[s.order[p]].ID)
		}
		s.bestGroups[p] = uint16(1) << uint(p)
		s.bestCost += gc.cost
	}

	s.assign(0, nil, 0)

	res := &Result{Vehicles: len(s.bestGroups)}
	for v, mask := range s.bestGroups {
		cost, path, ok := s.routeDFS(mask, true)
		if !ok {
			return nil, fmt.Errorf("internal error: lost feasible route for group %x", mask)
		}
		route := s.buildRoute(v+1, mask, path, cost)
		res.TotalTravelTime += cost
		res.Routes = append(res.Routes, route)
	}
	return res, nil
}

// assign enumerates set partitions of orders into vehicle groups, pruning
// against the incumbent (vehicles first, then cost). Group cost is monotone
// in the group bitmask under the triangle inequality, so costSoFar never
// decreases along a branch.
func (s *solver) assign(pos int, groups []uint16, costSoFar int) {
	if len(groups) > s.bestVehicles {
		return
	}
	if len(groups) == s.bestVehicles && costSoFar >= s.bestCost {
		return
	}
	if pos == s.n {
		if len(groups) < s.bestVehicles ||
			(len(groups) == s.bestVehicles && costSoFar < s.bestCost) {
			s.bestVehicles = len(groups)
			s.bestCost = costSoFar
			s.bestGroups = append([]uint16(nil), groups...)
		}
		return
	}
	bit := uint16(1) << uint(pos)
	// Prefer merging into existing vehicles before opening a new one.
	for gi := range groups {
		merged := groups[gi] | bit
		gc := s.routeCost(merged)
		if !gc.ok {
			continue
		}
		old := s.routeCost(groups[gi])
		groups[gi] = merged
		s.assign(pos+1, groups, costSoFar-old.cost+gc.cost)
		groups[gi] = merged &^ bit
	}
	if len(groups)+1 <= s.bestVehicles {
		gc := s.routeCost(bit) // singleton, known feasible
		s.assign(pos+1, append(groups, bit), costSoFar+gc.cost)
	}
}

func (s *solver) routeCost(mask uint16) groupCost {
	if gc, ok := s.cache[mask]; ok {
		return gc
	}
	cost, _, ok := s.routeDFS(mask, false)
	gc := groupCost{cost: cost, ok: ok}
	s.cache[mask] = gc
	return gc
}

// routeDFS finds the minimum-cost feasible stop sequence for one vehicle
// serving exactly the orders in mask (pickup before delivery, capacity and
// time windows respected). With record=true it also returns an optimal path.
func (s *solver) routeDFS(mask uint16, record bool) (int, []stopRef, bool) {
	in := s.in
	m := in.Matrix
	best := math.MaxInt32
	var bestPath []stopRef
	var path []stopRef

	var rec func(picked, delivered uint16, cur, t, loadW, loadS, cost int)
	rec = func(picked, delivered uint16, cur, t, loadW, loadS, cost int) {
		if delivered == mask {
			total := cost + m[cur][in.Depot]
			if total < best {
				best = total
				if record {
					bestPath = append([]stopRef(nil), path...)
				}
			}
			return
		}
		// Lower bound: every remaining stop and the depot still need one
		// incoming leg, each costing at least its minimum incoming edge.
		lb := cost + s.minIn[in.Depot]
		for p := 0; p < s.n; p++ {
			if mask>>uint(p)&1 == 0 {
				continue
			}
			o := &in.Orders[s.order[p]]
			if picked>>uint(p)&1 == 0 {
				lb += s.minIn[o.Pickup]
			}
			if delivered>>uint(p)&1 == 0 {
				lb += s.minIn[o.Delivery]
			}
		}
		if lb >= best {
			return
		}
		// Earliest-arrival check: under the triangle inequality no detour is
		// faster than the direct leg, so any stop already unreachable within
		// its window kills this branch.
		for p := 0; p < s.n; p++ {
			if mask>>uint(p)&1 == 0 {
				continue
			}
			o := &in.Orders[s.order[p]]
			if picked>>uint(p)&1 == 0 && t+m[cur][o.Pickup] > o.PickupWindow.Close {
				return
			}
			if picked>>uint(p)&1 == 1 && delivered>>uint(p)&1 == 0 && t+m[cur][o.Delivery] > o.DeliveryWindow.Close {
				return
			}
		}
		for p := 0; p < s.n; p++ {
			if mask>>uint(p)&1 == 0 {
				continue
			}
			oi := s.order[p]
			o := &in.Orders[oi]
			bit := uint16(1) << uint(p)
			if picked&bit == 0 {
				if loadW+o.Weight > in.Vehicle.WeightCapacity || loadS+o.Seats > in.Vehicle.SeatCapacity {
					continue
				}
				arr := t + m[cur][o.Pickup]
				if arr > o.PickupWindow.Close {
					continue
				}
				dep := arr
				if dep < o.PickupWindow.Open {
					dep = o.PickupWindow.Open
				}
				dep += o.Service
				if record {
					path = append(path, stopRef{order: oi, pickup: true})
					rec(picked|bit, delivered, o.Pickup, dep, loadW+o.Weight, loadS+o.Seats, cost+m[cur][o.Pickup])
					path = path[:len(path)-1]
				} else {
					rec(picked|bit, delivered, o.Pickup, dep, loadW+o.Weight, loadS+o.Seats, cost+m[cur][o.Pickup])
				}
			} else if delivered&bit == 0 {
				arr := t + m[cur][o.Delivery]
				if arr > o.DeliveryWindow.Close {
					continue
				}
				dep := arr
				if dep < o.DeliveryWindow.Open {
					dep = o.DeliveryWindow.Open
				}
				dep += o.Service
				if record {
					path = append(path, stopRef{order: oi, pickup: false})
					rec(picked, delivered|bit, o.Delivery, dep, loadW-o.Weight, loadS-o.Seats, cost+m[cur][o.Delivery])
					path = path[:len(path)-1]
				} else {
					rec(picked, delivered|bit, o.Delivery, dep, loadW-o.Weight, loadS-o.Seats, cost+m[cur][o.Delivery])
				}
			}
		}
	}
	rec(0, 0, in.Depot, 0, 0, 0, 0)
	if best == math.MaxInt32 {
		return 0, nil, false
	}
	return best, bestPath, true
}

// buildRoute replays an optimal path to produce per-stop timing and load.
func (s *solver) buildRoute(vehicle int, mask uint16, path []stopRef, cost int) Route {
	in := s.in
	route := Route{Vehicle: vehicle, TravelTime: cost}
	cur := in.Depot
	t := 0
	loadW, loadS := 0, 0
	route.Stops = append(route.Stops, Stop{Node: cur, Action: "depart-depot"})
	seen := make(map[int]bool)
	for _, sr := range path {
		o := &in.Orders[sr.order]
		node, win, action := o.Pickup, o.PickupWindow, "pickup"
		if !sr.pickup {
			node, win, action = o.Delivery, o.DeliveryWindow, "deliver"
		}
		leg := in.Matrix[cur][node]
		arr := t + leg
		wait := 0
		if arr < win.Open {
			wait = win.Open - arr
		}
		dep := arr + wait + o.Service
		if sr.pickup {
			loadW += o.Weight
			loadS += o.Seats
		} else {
			loadW -= o.Weight
			loadS -= o.Seats
		}
		route.Stops = append(route.Stops, Stop{
			Node: node, Action: action, Order: o.ID,
			Arrival: arr, Wait: wait, Departure: dep,
			LoadWeight: loadW, LoadSeats: loadS, LegTravel: leg,
		})
		if !seen[sr.order] {
			seen[sr.order] = true
			route.Orders = append(route.Orders, o.ID)
		}
		cur, t = node, dep
	}
	leg := in.Matrix[cur][in.Depot]
	route.Stops = append(route.Stops, Stop{
		Node: in.Depot, Action: "return-depot",
		Arrival: t + leg, Departure: t + leg, LegTravel: leg,
	})
	return route
}
