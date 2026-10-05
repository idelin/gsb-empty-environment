package main

import "sort"

// Stop ids: for n orders, stop s in [0, n) is the pickup of order s and
// stop s in [n, 2n) is the delivery of order s-n. Matrix node = s+1;
// node 0 is the depot. A route is a sequence of stop ids in visit order;
// pickup-before-delivery is enforced by construction when routes are
// built, and an order never changes vehicle (no transfers).

type stopPlan struct {
	stop    int
	arrival int
	wait    int
	depart  int
	loadW   int
	loadP   int
}

type simState struct {
	time  int
	node  int
	dist  int
	loadW int
	loadP int
}

// step advances the simulation by one stop. With checkWindows false only
// capacity is enforced; that is the sound intermediate check when the
// triangle inequality does not hold, because inserting more stops may
// then shorten travel and make a late route feasible again. Capacity
// pruning is sound either way: insertions never reduce the load at a
// stop.
func (in *Input) step(st *simState, s int, checkWindows bool) (stopPlan, bool) {
	n := len(in.Orders)
	next := s + 1
	leg := in.TravelTime[st.node][next]
	st.dist += leg
	arrival := st.time + leg
	o := &in.Orders[s%n]
	var win TimeWindow
	var service int
	if s < n {
		win, service = o.PickupWindow, o.PickupService
	} else {
		win, service = o.DeliveryWindow, o.DeliveryService
	}
	if checkWindows && arrival > win.Latest {
		return stopPlan{}, false
	}
	wait := 0
	if arrival < win.Earliest {
		wait = win.Earliest - arrival
	}
	depart := arrival + wait + service
	if s < n {
		st.loadW += o.Weight
		st.loadP += o.Passengers
	} else {
		st.loadW -= o.Weight
		st.loadP -= o.Passengers
	}
	if st.loadW > in.Vehicle.WeightCapacity || st.loadP > in.Vehicle.SeatCapacity {
		return stopPlan{}, false
	}
	st.time = depart
	st.node = next
	return stopPlan{stop: s, arrival: arrival, wait: wait, depart: depart, loadW: st.loadW, loadP: st.loadP}, true
}

func (in *Input) newSimState() simState {
	return simState{time: in.DepotDeparture}
}

func (in *Input) finish(st *simState) (int, bool) {
	st.dist += in.TravelTime[st.node][0]
	return st.dist, true
}

// simulate checks a route for capacity and time-window feasibility and
// returns the per-stop plan plus the total route distance including the
// return to the depot.
func (in *Input) simulate(seq []int) ([]stopPlan, int, bool) {
	st := in.newSimState()
	plans := make([]stopPlan, 0, len(seq))
	for _, s := range seq {
		pl, ok := in.step(&st, s, true)
		if !ok {
			return nil, 0, false
		}
		plans = append(plans, pl)
	}
	dist, _ := in.finish(&st)
	return plans, dist, true
}

// check is simulate without plan collection; see step for the meaning
// of checkWindows.
func (in *Input) check(seq []int, checkWindows bool) (int, bool) {
	st := in.newSimState()
	for _, s := range seq {
		if _, ok := in.step(&st, s, checkWindows); !ok {
			return 0, false
		}
	}
	return in.finish(&st)
}

// feasInsert evaluates route with pickup pu inserted at position p and
// delivery de inserted so that d original stops precede it (d >= p),
// without allocating the inserted sequence.
func (in *Input) feasInsert(route []int, p, d, pu, de int, checkWindows bool) (int, bool) {
	st := in.newSimState()
	for _, s := range route[:p] {
		if _, ok := in.step(&st, s, checkWindows); !ok {
			return 0, false
		}
	}
	if _, ok := in.step(&st, pu, checkWindows); !ok {
		return 0, false
	}
	for _, s := range route[p:d] {
		if _, ok := in.step(&st, s, checkWindows); !ok {
			return 0, false
		}
	}
	if _, ok := in.step(&st, de, checkWindows); !ok {
		return 0, false
	}
	for _, s := range route[d:] {
		if _, ok := in.step(&st, s, checkWindows); !ok {
			return 0, false
		}
	}
	return in.finish(&st)
}

// insertStops returns a copy of route with pickup stop pu inserted at
// position p and delivery stop de inserted so that exactly d original
// stops precede it (d >= p, hence pickup always precedes delivery).
func insertStops(route []int, p, d, pu, de int) []int {
	seq := make([]int, 0, len(route)+2)
	seq = append(seq, route[:p]...)
	seq = append(seq, pu)
	seq = append(seq, route[p:d]...)
	seq = append(seq, de)
	seq = append(seq, route[d:]...)
	return seq
}

// solver is a depth-first branch-and-bound over order assignments.
// Orders are placed one at a time, either inserted anywhere feasible in
// an existing route or started on a fresh vehicle, so every order keeps
// a single vehicle end to end.
type solver struct {
	in      *Input
	n       int
	tiValid bool  // triangle inequality holds: distance pruning is sound
	perm    []int // dfs depth -> order index (tightest windows first)

	routes    [][]int
	routeDist []int

	bestRoutes [][]int
	bestTotal  int
	hasBest    bool

	expansions int
	limit      int
	aborted    bool
}

func newSolver(in *Input, tiValid bool) *solver {
	n := len(in.Orders)
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	sort.Slice(perm, func(a, b int) bool {
		oa, ob := in.Orders[perm[a]], in.Orders[perm[b]]
		if oa.PickupWindow.Earliest != ob.PickupWindow.Earliest {
			return oa.PickupWindow.Earliest < ob.PickupWindow.Earliest
		}
		return oa.PickupWindow.Latest < ob.PickupWindow.Latest
	})
	return &solver{in: in, n: n, tiValid: tiValid, perm: perm, limit: 2000000}
}

func (s *solver) solve() {
	s.greedy()
	s.routes = nil
	s.routeDist = nil
	s.dfs(0, 0)
}

// greedy builds an initial incumbent by inserting each order at its
// cheapest feasible position, opening a new vehicle when nothing fits.
func (s *solver) greedy() {
	for idx := 0; idx < s.n; idx++ {
		ord := s.perm[idx]
		pu, de := ord, ord+s.n
		bestRi, bestP, bestD, bestDist := -1, 0, 0, 0
		for ri, route := range s.routes {
			for p := 0; p <= len(route); p++ {
				for d := p; d <= len(route); d++ {
					if nd, ok := s.in.feasInsert(route, p, d, pu, de, s.tiValid); ok {
						if bestRi < 0 || nd < bestDist {
							bestRi, bestP, bestD, bestDist = ri, p, d, nd
						}
					}
				}
			}
		}
		if bestRi >= 0 {
			s.routes[bestRi] = insertStops(s.routes[bestRi], bestP, bestD, pu, de)
			s.routeDist[bestRi] = bestDist
			continue
		}
		if nd, ok := s.in.check([]int{pu, de}, s.tiValid); ok {
			s.routes = append(s.routes, []int{pu, de})
			s.routeDist = append(s.routeDist, nd)
		} else {
			return // no greedy incumbent; dfs starts without one
		}
	}
	s.record()
}

// record adopts the current routes as the incumbent if they are fully
// feasible and lexicographically better (fewer vehicles, then shorter
// total distance).
func (s *solver) record() {
	total := 0
	for _, r := range s.routes {
		_, d, ok := s.in.simulate(r)
		if !ok {
			return
		}
		total += d
	}
	if s.hasBest &&
		(len(s.routes) > len(s.bestRoutes) ||
			(len(s.routes) == len(s.bestRoutes) && total >= s.bestTotal)) {
		return
	}
	s.hasBest = true
	s.bestTotal = total
	s.bestRoutes = make([][]int, len(s.routes))
	for i, r := range s.routes {
		cp := make([]int, len(r))
		copy(cp, r)
		s.bestRoutes[i] = cp
	}
}

func (s *solver) dfs(idx, curDist int) {
	if s.aborted {
		return
	}
	if s.hasBest {
		// Vehicle count never decreases along a branch: always sound.
		if len(s.routes) > len(s.bestRoutes) {
			return
		}
		// Inserting stops can only lengthen a route when the triangle
		// inequality holds (detour cost is non-negative), so the current
		// distance is a valid lower bound. Without it this prune is
		// unsound and must be skipped. It is also only valid once the
		// branch already uses as many vehicles as the incumbent: with
		// fewer vehicles a completion is lexicographically better at
		// any distance.
		if s.tiValid && len(s.routes) >= len(s.bestRoutes) && curDist >= s.bestTotal {
			return
		}
	}
	if idx == s.n {
		s.record()
		return
	}
	s.expansions++
	if s.expansions > s.limit {
		s.aborted = true
		return
	}

	ord := s.perm[idx]
	pu, de := ord, ord+s.n
	type cand struct {
		route int
		p, d  int
		dist  int
	}
	var cands []cand
	for ri, route := range s.routes {
		for p := 0; p <= len(route); p++ {
			for d := p; d <= len(route); d++ {
				// Pruning window-infeasible intermediate routes is
				// only sound under the triangle inequality: without
				// it, later insertions can shorten the travel time
				// and repair a late route.
				if nd, ok := s.in.feasInsert(route, p, d, pu, de, s.tiValid); ok {
					cands = append(cands, cand{ri, p, d, nd})
				}
			}
		}
	}
	// Cheapest insertions first finds good incumbents early.
	sort.Slice(cands, func(a, b int) bool { return cands[a].dist < cands[b].dist })
	for _, c := range cands {
		route := s.routes[c.route]
		seq := insertStops(route, c.p, c.d, pu, de)
		old := s.routeDist[c.route]
		s.routes[c.route] = seq
		s.routeDist[c.route] = c.dist
		s.dfs(idx+1, curDist-old+c.dist)
		s.routes[c.route] = route
		s.routeDist[c.route] = old
		if s.aborted {
			return
		}
	}

	// Open a new vehicle, but never more than the incumbent uses.
	if !s.hasBest || len(s.routes)+1 <= len(s.bestRoutes) {
		seq := []int{pu, de}
		if nd, ok := s.in.check(seq, s.tiValid); ok {
			s.routes = append(s.routes, seq)
			s.routeDist = append(s.routeDist, nd)
			s.dfs(idx+1, curDist+nd)
			s.routes = s.routes[:len(s.routes)-1]
			s.routeDist = s.routeDist[:len(s.routeDist)-1]
		}
	}
}

func (s *solver) output() Output {
	out := Output{
		Vehicles:      len(s.bestRoutes),
		TotalDistance: s.bestTotal,
		Optimal:       !s.aborted,
	}
	if !s.tiValid {
		out.Warnings = append(out.Warnings,
			"travel time matrix violates the triangle inequality; distance-based pruning disabled")
	}
	if s.aborted {
		out.Warnings = append(out.Warnings,
			"search limit reached; reported solution may be suboptimal")
	}
	for vi, route := range s.bestRoutes {
		plans, dist, _ := s.in.simulate(route)
		ro := RouteOutput{Vehicle: vi + 1, Distance: dist}
		for _, pl := range plans {
			o := s.in.Orders[pl.stop%s.n]
			kind := "pickup"
			if pl.stop >= s.n {
				kind = "delivery"
			}
			ro.Stops = append(ro.Stops, StopOutput{
				Node:           pl.stop + 1,
				Order:          o.ID,
				Kind:           kind,
				Arrival:        pl.arrival,
				Wait:           pl.wait,
				Depart:         pl.depart,
				LoadWeight:     pl.loadW,
				LoadPassengers: pl.loadP,
			})
		}
		out.Routes = append(out.Routes, ro)
	}
	return out
}
