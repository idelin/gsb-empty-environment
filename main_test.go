package main

import (
	"math/rand"
	"testing"
)

// lineMetric builds a triangle-inequality-satisfying matrix from 1D coords.
func lineMetric(coords []int) [][]int {
	n := len(coords)
	t := make([][]int, n)
	for i := range t {
		t[i] = make([]int, n)
		for j := range t {
			d := coords[i] - coords[j]
			if d < 0 {
				d = -d
			}
			t[i][j] = d
		}
	}
	return t
}

func TestSingleVehicleTwoOrders(t *testing.T) {
	in := &Input{
		Vehicle: Vehicle{WeightCapacity: 10, SeatCapacity: 4},
		Orders: []Order{
			{ID: "A", Weight: 2, Passengers: 1,
				PickupWindow: TimeWindow{0, 50}, DeliveryWindow: TimeWindow{0, 200}},
			{ID: "B", Weight: 2, Passengers: 1,
				PickupWindow: TimeWindow{0, 50}, DeliveryWindow: TimeWindow{0, 200}},
		},
		// nodes: 0 depot, 1 pA, 2 pB, 3 dA, 4 dB
		TravelTime: lineMetric([]int{0, 5, 10, 30, 20}),
	}
	if err := in.validate(); err != nil {
		t.Fatal(err)
	}
	s := newSolver(in, triangleInequalityHolds(in.TravelTime))
	s.solve()
	if !s.hasBest {
		t.Fatal("expected a solution")
	}
	if got := len(s.bestRoutes); got != 1 {
		t.Fatalf("vehicles = %d, want 1", got)
	}
	// Optimal single route: 0->1->2->4->3->0 = 5+5+10+10+30 = 60.
	if s.bestTotal != 60 {
		t.Fatalf("distance = %d, want 60", s.bestTotal)
	}
	out := s.output()
	if len(out.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", out.Warnings)
	}
	// Pickup must precede delivery for every order.
	for _, r := range out.Routes {
		seen := map[string]bool{}
		for _, st := range r.Stops {
			if st.Kind == "delivery" && !seen[st.Order] {
				t.Fatalf("delivery of %s before its pickup", st.Order)
			}
			if st.Kind == "pickup" {
				seen[st.Order] = true
			}
		}
	}
}

func TestIncompatibleWindowsForceTwoVehicles(t *testing.T) {
	in := &Input{
		Vehicle: Vehicle{WeightCapacity: 10, SeatCapacity: 4},
		Orders: []Order{
			{ID: "A", Weight: 1, Passengers: 1,
				PickupWindow: TimeWindow{0, 5}, DeliveryWindow: TimeWindow{0, 12}},
			{ID: "B", Weight: 1, Passengers: 1,
				PickupWindow: TimeWindow{0, 5}, DeliveryWindow: TimeWindow{0, 12}},
		},
		// Depot between the two pickups; both windows are tight, so one
		// vehicle cannot reach the second pickup before its window closes.
		TravelTime: lineMetric([]int{0, -3, 3, -6, 6}),
	}
	if err := in.validate(); err != nil {
		t.Fatal(err)
	}
	s := newSolver(in, true)
	s.solve()
	if !s.hasBest {
		t.Fatal("expected a solution")
	}
	if got := len(s.bestRoutes); got != 2 {
		t.Fatalf("vehicles = %d, want 2", got)
	}
}

func TestCapacityForcesTwoVehicles(t *testing.T) {
	in := &Input{
		Vehicle: Vehicle{WeightCapacity: 5, SeatCapacity: 4},
		Orders: []Order{
			{ID: "A", Weight: 4, Passengers: 1,
				PickupWindow: TimeWindow{0, 5}, DeliveryWindow: TimeWindow{20, 40}},
			{ID: "B", Weight: 4, Passengers: 1,
				PickupWindow: TimeWindow{0, 5}, DeliveryWindow: TimeWindow{20, 40}},
		},
		// Both pickups at distance 5 with tight windows force the vehicle
		// to carry both orders at once: 4+4 > 5 exceeds the capacity.
		TravelTime: lineMetric([]int{0, 5, 5, 10, 11}),
	}
	if err := in.validate(); err != nil {
		t.Fatal(err)
	}
	s := newSolver(in, true)
	s.solve()
	if !s.hasBest || len(s.bestRoutes) != 2 {
		t.Fatalf("vehicles = %d, want 2", len(s.bestRoutes))
	}
}

func TestTriangleInequalityViolationWarns(t *testing.T) {
	in := &Input{
		Vehicle: Vehicle{WeightCapacity: 10, SeatCapacity: 4},
		Orders: []Order{
			{ID: "A", Weight: 1, Passengers: 1,
				PickupWindow: TimeWindow{0, 100}, DeliveryWindow: TimeWindow{0, 200}},
		},
		// t[0][1]=10 but t[0][2]+t[2][1]=2: violates the inequality.
		TravelTime: [][]int{
			{0, 10, 1},
			{1, 0, 1},
			{1, 1, 0},
		},
	}
	if err := in.validate(); err != nil {
		t.Fatal(err)
	}
	if triangleInequalityHolds(in.TravelTime) {
		t.Fatal("matrix should violate the triangle inequality")
	}
	s := newSolver(in, triangleInequalityHolds(in.TravelTime))
	s.solve()
	if !s.hasBest {
		t.Fatal("expected a solution")
	}
	out := s.output()
	if len(out.Warnings) == 0 {
		t.Fatal("expected a triangle-inequality warning")
	}
	// Route 0->1->2->0 costs 10+1+1 = 12.
	if out.TotalDistance != 12 {
		t.Fatalf("distance = %d, want 12", out.TotalDistance)
	}
}

func TestWaitingTimeReported(t *testing.T) {
	in := &Input{
		Vehicle: Vehicle{WeightCapacity: 10, SeatCapacity: 4},
		Orders: []Order{
			{ID: "A", Weight: 1, Passengers: 1,
				PickupWindow:   TimeWindow{Earliest: 50, Latest: 60},
				DeliveryWindow: TimeWindow{Earliest: 0, Latest: 200}},
		},
		TravelTime: lineMetric([]int{0, 5, 10}),
	}
	if err := in.validate(); err != nil {
		t.Fatal(err)
	}
	s := newSolver(in, true)
	s.solve()
	out := s.output()
	if len(out.Routes) != 1 || len(out.Routes[0].Stops) != 2 {
		t.Fatalf("unexpected routes: %+v", out.Routes)
	}
	pu := out.Routes[0].Stops[0]
	if pu.Arrival != 5 || pu.Wait != 45 || pu.Depart != 50 {
		t.Fatalf("pickup stop = %+v, want arrival 5 wait 45 depart 50", pu)
	}
	dl := out.Routes[0].Stops[1]
	if dl.Arrival != 55 || dl.Wait != 0 {
		t.Fatalf("delivery stop = %+v, want arrival 55 wait 0", dl)
	}
}

func TestInfeasibleOrderDetected(t *testing.T) {
	in := &Input{
		Vehicle: Vehicle{WeightCapacity: 10, SeatCapacity: 4},
		Orders: []Order{
			{ID: "A", Weight: 1, Passengers: 1,
				PickupWindow: TimeWindow{0, 5}, DeliveryWindow: TimeWindow{0, 8}},
		},
		TravelTime: lineMetric([]int{0, 5, 20}), // pickup->delivery alone takes 15
	}
	if err := in.validate(); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := in.simulate([]int{0, 1}); ok {
		t.Fatal("solo route should be infeasible")
	}
}

// bruteForce enumerates every precedence-valid global stop sequence and
// every split of it into contiguous routes such that an order's pickup
// and delivery stay on the same vehicle (no transfers), returning the
// lexicographic optimum (vehicles, distance). Cross-checks the solver.
func bruteForce(in *Input) (int, int, bool) {
	n := len(in.Orders)
	bestV, bestD, found := 0, 0, false
	state := 0 // bit o: pickup of o placed; bit n+o: delivery of o placed
	open := 0  // orders picked up but not yet delivered in the current route
	routes := [][]int{{}}
	var rec func()
	rec = func() {
		if totalLen(routes) == 2*n {
			dist := 0
			for _, r := range routes {
				_, d, ok := in.simulate(r)
				if !ok {
					return
				}
				dist += d
			}
			if !found || len(routes) < bestV || (len(routes) == bestV && dist < bestD) {
				found, bestV, bestD = true, len(routes), dist
			}
			return
		}
		for s := 0; s < 2*n; s++ {
			o := s % n
			isPickup := s < n
			if isPickup {
				if state&(1<<uint(o)) != 0 {
					continue
				}
			} else if state&(1<<uint(o)) == 0 || state&(1<<uint(n+o)) != 0 {
				continue
			}
			bit := o
			if !isPickup {
				bit = n + o
			}
			// A route may be closed before placing s when nothing is
			// onboard, so no order changes vehicle mid-way.
			canClose := open == 0
			state ^= 1 << uint(bit)
			if isPickup {
				open++
			} else {
				open--
			}
			// Continue the current route.
			last := len(routes) - 1
			routes[last] = append(routes[last], s)
			rec()
			routes[last] = routes[last][:len(routes[last])-1]
			// Or close the current route and start a new one with s.
			if canClose && len(routes[last]) > 0 && len(routes) < n {
				routes = append(routes, []int{s})
				rec()
				routes = routes[:len(routes)-1]
			}
			if isPickup {
				open--
			} else {
				open++
			}
			state ^= 1 << uint(bit)
		}
	}
	rec()
	return bestV, bestD, found
}

func totalLen(rs [][]int) int {
	n := 0
	for _, r := range rs {
		n += len(r)
	}
	return n
}

func randomInstance(rng *rand.Rand, n int, metric bool) *Input {
	in := &Input{
		Vehicle: Vehicle{WeightCapacity: 6 + rng.Intn(6), SeatCapacity: 2 + rng.Intn(3)},
		Orders:  make([]Order, n),
	}
	dim := 2*n + 1
	if metric {
		coords := make([]int, dim)
		for i := range coords {
			coords[i] = rng.Intn(60)
		}
		in.TravelTime = lineMetric(coords)
	} else {
		in.TravelTime = make([][]int, dim)
		for i := range in.TravelTime {
			in.TravelTime[i] = make([]int, dim)
			for j := range in.TravelTime[i] {
				if i != j {
					in.TravelTime[i][j] = 1 + rng.Intn(40)
				}
			}
		}
	}
	for i := range in.Orders {
		pe := rng.Intn(40)
		in.Orders[i] = Order{
			ID:             string(rune('A' + i)),
			Weight:         rng.Intn(5),
			Passengers:     rng.Intn(3),
			PickupWindow:   TimeWindow{pe, pe + 20 + rng.Intn(60)},
			DeliveryWindow: TimeWindow{pe, pe + 60 + rng.Intn(120)},
		}
	}
	return in
}

func TestSolverMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 400; trial++ {
		metric := trial%2 == 0
		n := 3
		if trial >= 200 {
			n = 4
		}
		in := randomInstance(rng, n, metric)
		if err := in.validate(); err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		ti := triangleInequalityHolds(in.TravelTime)
		if metric && !ti {
			t.Fatalf("trial %d: line metric should satisfy the triangle inequality", trial)
		}
		s := newSolver(in, ti)
		s.solve()
		bv, bd, bok := bruteForce(in)
		if bok != s.hasBest {
			t.Fatalf("trial %d (metric=%v): feasibility mismatch: solver=%v brute=%v",
				trial, metric, s.hasBest, bok)
		}
		if !bok {
			continue
		}
		if len(s.bestRoutes) != bv || s.bestTotal != bd {
			t.Fatalf("trial %d (metric=%v): solver=(%d vehicles, %d) brute=(%d vehicles, %d)",
				trial, metric, len(s.bestRoutes), s.bestTotal, bv, bd)
		}
	}
}
