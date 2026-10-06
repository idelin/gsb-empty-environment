package pdp_test

import (
	"math/rand"
	"testing"

	"pdp"
)

// bfTask mirrors the remaining work of one request for the brute-force
// reference enumerator used only in tests.
type bfTask struct {
	reqID  int
	pickup int // -1 if already onboard
	drop   int
	weight int
}

// bruteForce enumerates all (2n)! permutations of the remaining events,
// filters out illegal orders (delivery before pickup, capacity overflow)
// and keeps the cheapest route, breaking ties by the lexicographically
// smallest visited point id sequence. It is deliberately independent of
// the library's DFS so the two cross-check each other.
func bruteForce(in pdp.Instance, tasks []bfTask, start, load int) pdp.Solution {
	type event struct {
		task   int
		pickup bool
	}
	var events []event
	for i, t := range tasks {
		if t.pickup >= 0 {
			events = append(events, event{i, true})
		}
		events = append(events, event{i, false})
	}

	best := pdp.Solution{Cost: -1}
	perm := make([]int, len(events))
	for i := range perm {
		perm[i] = i
	}

	var rec func(pos int)
	rec = func(pos int) {
		if pos == len(perm) {
			state := make([]int, len(tasks)) // 0 pending, 1 onboard, 2 done
			for i, t := range tasks {
				if t.pickup < 0 {
					state[i] = 1
				}
			}
			node, curLoad, cost := start, load, 0
			nodes := []int{start}
			stops := []pdp.Stop{}
			loads := []int{load}
			for _, ei := range perm {
				e := events[ei]
				t := tasks[e.task]
				if e.pickup {
					if state[e.task] != 0 || curLoad+t.weight > in.Capacity {
						return
					}
					cost += in.Dist[node][t.pickup]
					node = t.pickup
					curLoad += t.weight
					state[e.task] = 1
					stops = append(stops, pdp.Stop{Node: node, Kind: pdp.PickupEvent, ReqID: t.reqID, LoadAfter: curLoad})
				} else {
					if state[e.task] != 1 {
						return
					}
					cost += in.Dist[node][t.drop]
					node = t.drop
					curLoad -= t.weight
					state[e.task] = 2
					stops = append(stops, pdp.Stop{Node: node, Kind: pdp.DeliveryEvent, ReqID: t.reqID, LoadAfter: curLoad})
				}
				nodes = append(nodes, node)
				loads = append(loads, curLoad)
			}
			cost += in.Dist[node][in.Depot]
			nodes = append(nodes, in.Depot)
			if best.Cost < 0 || cost < best.Cost || (cost == best.Cost && lexLess(nodes, best.Nodes)) {
				best = pdp.Solution{Cost: cost, Nodes: nodes, Stops: stops, Loads: loads}
			}
			return
		}
		for i := pos; i < len(perm); i++ {
			perm[pos], perm[i] = perm[i], perm[pos]
			rec(pos + 1)
			perm[pos], perm[i] = perm[i], perm[pos]
		}
	}
	rec(0)
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

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func randomInstance(rng *rand.Rand, pairs int) (pdp.Instance, []pdp.Request) {
	numNodes := 2*pairs + 2
	dist := make([][]int, numNodes)
	for i := range dist {
		dist[i] = make([]int, numNodes)
		for j := range dist[i] {
			if i != j {
				dist[i][j] = rng.Intn(20) // asymmetric, non-negative
			}
		}
	}
	cap := 1 + rng.Intn(5)
	in := pdp.Instance{Depot: 0, Capacity: cap, Dist: dist}
	reqs := make([]pdp.Request, pairs)
	for i := range reqs {
		reqs[i] = pdp.Request{
			ID:       i + 1,
			Pickup:   rng.Intn(numNodes),
			Delivery: rng.Intn(numNodes),
			Weight:   1 + rng.Intn(cap),
		}
	}
	return in, reqs
}

// The library's joint optimum must match the independent full-permutation
// enumeration on many random small instances, both in cost and in the
// tie-broken visited point id sequence.
func TestSolveMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 300; trial++ {
		pairs := 1 + rng.Intn(4)
		in, reqs := randomInstance(rng, pairs)
		got, err := pdp.Solve(in, reqs)
		if err != nil {
			t.Fatalf("trial %d: Solve: %v", trial, err)
		}
		tasks := make([]bfTask, len(reqs))
		for i, r := range reqs {
			tasks[i] = bfTask{reqID: r.ID, pickup: r.Pickup, drop: r.Delivery, weight: r.Weight}
		}
		want := bruteForce(in, tasks, in.Depot, 0)
		if got.Cost != want.Cost {
			t.Fatalf("trial %d: cost %d, brute force says %d", trial, got.Cost, want.Cost)
		}
		if !equalInts(got.Nodes, want.Nodes) {
			t.Fatalf("trial %d: nodes %v, brute force says %v", trial, got.Nodes, want.Nodes)
		}
	}
}

// The reported load trajectory must be internally consistent: it starts
// empty, tracks every stop, never exceeds capacity, and deliveries release
// exactly the request's weight.
func TestLoadTrajectory(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	weights := map[int]int{}
	for trial := 0; trial < 200; trial++ {
		pairs := 1 + rng.Intn(4)
		in, reqs := randomInstance(rng, pairs)
		for _, r := range reqs {
			weights[r.ID] = r.Weight
		}
		sol, err := pdp.Solve(in, reqs)
		if err != nil {
			t.Fatalf("trial %d: Solve: %v", trial, err)
		}
		if len(sol.Loads) != len(sol.Stops)+1 {
			t.Fatalf("trial %d: %d loads for %d stops", trial, len(sol.Loads), len(sol.Stops))
		}
		if sol.Loads[0] != 0 {
			t.Fatalf("trial %d: initial load %d, want 0", trial, sol.Loads[0])
		}
		if len(sol.Nodes) != len(sol.Stops)+2 || sol.Nodes[0] != in.Depot || sol.Nodes[len(sol.Nodes)-1] != in.Depot {
			t.Fatalf("trial %d: malformed node sequence %v", trial, sol.Nodes)
		}
		load := 0
		cost := 0
		prev := in.Depot
		for i, st := range sol.Stops {
			switch st.Kind {
			case pdp.PickupEvent:
				load += weights[st.ReqID]
			default:
				load -= weights[st.ReqID]
			}
			if load < 0 || load > in.Capacity {
				t.Fatalf("trial %d: load %d out of [0,%d] after stop %d", trial, load, in.Capacity, i)
			}
			if sol.Loads[i+1] != load || st.LoadAfter != load {
				t.Fatalf("trial %d: load evidence mismatch at stop %d: loads=%v stop=%d recomputed=%d",
					trial, i, sol.Loads, st.LoadAfter, load)
			}
			if sol.Nodes[i+1] != st.Node {
				t.Fatalf("trial %d: node sequence mismatch at stop %d", trial, i)
			}
			cost += in.Dist[prev][st.Node]
			prev = st.Node
		}
		cost += in.Dist[prev][in.Depot]
		if cost != sol.Cost {
			t.Fatalf("trial %d: cost %d, recomputed %d", trial, sol.Cost, cost)
		}
	}
}

// The joint optimum must be strictly better than serving each request with
// its own independent shortest tour when requests can share a trip.
func TestJointOptimizationBeatsIndependentTours(t *testing.T) {
	// Nodes: 0 depot, 1 -> 2 for request A, 2 -> 3 for request B.
	// Detouring back to the depot between the two requests costs 5+5.
	in := pdp.Instance{
		Depot:    0,
		Capacity: 2,
		Dist: [][]int{
			{0, 1, 5, 1},
			{1, 0, 1, 3},
			{5, 1, 0, 1},
			{1, 3, 1, 0},
		},
	}
	a := pdp.Request{ID: 1, Pickup: 1, Delivery: 2, Weight: 1}
	b := pdp.Request{ID: 2, Pickup: 2, Delivery: 3, Weight: 1}

	joint, err := pdp.Solve(in, []pdp.Request{a, b})
	if err != nil {
		t.Fatal(err)
	}
	soloA, err := pdp.Solve(in, []pdp.Request{a})
	if err != nil {
		t.Fatal(err)
	}
	soloB, err := pdp.Solve(in, []pdp.Request{b})
	if err != nil {
		t.Fatal(err)
	}
	if joint.Cost != 4 {
		t.Fatalf("joint cost %d, want 4 (route 0-1-2-3-0)", joint.Cost)
	}
	if independent := soloA.Cost + soloB.Cost; joint.Cost >= independent {
		t.Fatalf("joint cost %d not better than independent tours %d", joint.Cost, independent)
	}
}

// Equal-cost routes must be tie-broken by the lexicographically smallest
// visited point id sequence.
func TestTieBreakByVisitedPointIDs(t *testing.T) {
	in := pdp.Instance{
		Depot:    0,
		Capacity: 2,
		Dist: [][]int{
			{0, 1, 1, 1, 1},
			{1, 0, 1, 3, 2},
			{1, 1, 0, 2, 3},
			{1, 3, 2, 0, 1},
			{1, 2, 3, 1, 0},
		},
	}
	reqs := []pdp.Request{
		{ID: 1, Pickup: 1, Delivery: 2, Weight: 1},
		{ID: 2, Pickup: 3, Delivery: 4, Weight: 1},
	}
	sol, err := pdp.Solve(in, reqs)
	if err != nil {
		t.Fatal(err)
	}
	// Both 0-1-2-3-4-0 and 0-3-4-1-2-0 cost 6; the former is
	// lexicographically smaller.
	want := []int{0, 1, 2, 3, 4, 0}
	if sol.Cost != 6 {
		t.Fatalf("cost %d, want 6", sol.Cost)
	}
	if !equalInts(sol.Nodes, want) {
		t.Fatalf("nodes %v, want %v", sol.Nodes, want)
	}
}

func TestValidation(t *testing.T) {
	good := pdp.Instance{Depot: 0, Capacity: 1, Dist: [][]int{{0, 1}, {1, 0}}}
	req := pdp.Request{ID: 1, Pickup: 0, Delivery: 1, Weight: 1}
	if _, err := pdp.Solve(good, []pdp.Request{req}); err != nil {
		t.Fatalf("valid instance rejected: %v", err)
	}
	bad := pdp.Instance{Depot: 0, Capacity: 1, Dist: [][]int{{0, -1}, {1, 0}}}
	if _, err := pdp.Solve(bad, []pdp.Request{req}); err == nil {
		t.Fatal("negative edge accepted")
	}
	overweight := pdp.Request{ID: 2, Pickup: 0, Delivery: 1, Weight: 2}
	if _, err := pdp.Solve(good, []pdp.Request{overweight}); err == nil {
		t.Fatal("over-capacity request accepted")
	}
	many := make([]pdp.Request, pdp.MaxRequests+1)
	for i := range many {
		many[i] = pdp.Request{ID: i + 1, Pickup: 0, Delivery: 1, Weight: 1}
	}
	if _, err := pdp.Solve(good, many); err == nil {
		t.Fatal("more than MaxRequests accepted")
	}
}
