package pdp

import (
	"math/rand"
	"testing"
)

// completeDist builds a symmetric matrix; unlisted pairs get def.
func completeDist(n, def int, edges ...[3]int) [][]int {
	d := make([][]int, n)
	for i := range d {
		d[i] = make([]int, n)
		for j := range d[i] {
			d[i][j] = def
		}
		d[i][i] = 0
	}
	for _, e := range edges {
		d[e[0]][e[1]] = e[2]
		d[e[1]][e[0]] = e[2]
	}
	return d
}

// checkRoute verifies legality: precedence, capacity, the load
// trajectory, full coverage, and the reported total distance.
func checkRoute(t *testing.T, p Problem, r Route) {
	t.Helper()
	if r.Start != p.Start {
		t.Errorf("route starts at %d, depot is %d", r.Start, p.Start)
	}
	reqs := make(map[int]Request)
	for _, q := range p.Requests {
		reqs[q.ID] = q
	}
	load := 0
	picked := make(map[int]bool)
	done := make(map[int]bool)
	prev, dist := p.Start, 0
	for _, st := range r.Stops {
		dist += p.Dist[prev][st.Node]
		q, ok := reqs[st.Request]
		if !ok {
			t.Fatalf("stop for unknown request %d", st.Request)
		}
		switch st.Kind {
		case PickupStop:
			if picked[st.Request] {
				t.Errorf("request %d picked up twice", st.Request)
			}
			if st.Node != q.Pickup {
				t.Errorf("request %d picked at %d, want %d", st.Request, st.Node, q.Pickup)
			}
			picked[st.Request] = true
			load += q.Weight
		case DeliverStop:
			if !picked[st.Request] || done[st.Request] {
				t.Errorf("request %d delivered before pickup", st.Request)
			}
			if st.Node != q.Drop {
				t.Errorf("request %d delivered at %d, want %d", st.Request, st.Node, q.Drop)
			}
			done[st.Request] = true
			load -= q.Weight
		default:
			t.Errorf("unexpected stop kind %v in static route", st.Kind)
		}
		if load > p.Capacity {
			t.Errorf("load %d exceeds capacity %d after %+v", load, p.Capacity, st)
		}
		if st.Load != load {
			t.Errorf("load trajectory: stop %+v reports load %d, want %d", st, st.Load, load)
		}
		prev = st.Node
	}
	dist += p.Dist[prev][p.Start]
	if dist != r.TotalDist {
		t.Errorf("TotalDist %d, recomputed %d", r.TotalDist, dist)
	}
	for _, q := range p.Requests {
		if !done[q.ID] {
			t.Errorf("request %d never delivered", q.ID)
		}
	}
}

func randomProblem(rng *rand.Rand, n int) Problem {
	nodes := 2 + rng.Intn(5)
	d := make([][]int, nodes)
	for i := range d {
		d[i] = make([]int, nodes)
		for j := range d[i] {
			if i != j {
				d[i][j] = rng.Intn(20)
			}
		}
	}
	p := Problem{
		NumNodes: nodes,
		Dist:     d,
		Capacity: rng.Intn(8),
		Start:    rng.Intn(nodes),
	}
	for i := 0; i < n; i++ {
		p.Requests = append(p.Requests, Request{
			ID:     i,
			Pickup: rng.Intn(nodes),
			Drop:   rng.Intn(nodes),
			Weight: rng.Intn(6),
		})
	}
	return p
}

func TestSolveMatchesBruteRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 300; trial++ {
		p := randomProblem(rng, 1+rng.Intn(4))
		got, errDP := Solve(p)
		want, errBF := SolveBrute(p)
		if (errDP == nil) != (errBF == nil) {
			t.Fatalf("trial %d: Solve err %v, SolveBrute err %v", trial, errDP, errBF)
		}
		if errDP != nil {
			continue
		}
		if got.TotalDist != want.TotalDist {
			t.Fatalf("trial %d: Solve cost %d, brute force %d", trial, got.TotalDist, want.TotalDist)
		}
		if !equalSeq(got.Nodes(), want.Nodes()) {
			t.Fatalf("trial %d: Solve visits %v, brute force %v", trial, got.Nodes(), want.Nodes())
		}
		checkRoute(t, p, got)
	}
}

func TestSolveMatchesBruteSixRequests(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	p := randomProblem(rng, MaxRequests)
	p.Capacity = 6 // keep most permutations legal, worst case for both solvers
	got, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	want, err := SolveBrute(p)
	if err != nil {
		t.Fatalf("SolveBrute: %v", err)
	}
	if got.TotalDist != want.TotalDist || !equalSeq(got.Nodes(), want.Nodes()) {
		t.Fatalf("Solve (%d, %v) != brute force (%d, %v)",
			got.TotalDist, got.Nodes(), want.TotalDist, want.Nodes())
	}
	checkRoute(t, p, got)
}

func TestTieBreakByVisitedNodeIDs(t *testing.T) {
	p := Problem{
		NumNodes: 4,
		Dist: completeDist(4, 99,
			[3]int{0, 1, 1}, [3]int{0, 2, 1}, [3]int{0, 3, 1},
			[3]int{1, 2, 1}, [3]int{1, 3, 2}, [3]int{2, 3, 2}),
		Capacity: 2,
		Start:    0,
		Requests: []Request{
			{ID: 0, Pickup: 1, Drop: 3, Weight: 1},
			{ID: 1, Pickup: 2, Drop: 3, Weight: 1},
		},
	}
	r, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.TotalDist != 5 {
		t.Fatalf("TotalDist %d, want 5", r.TotalDist)
	}
	// [1 2 3 3] and [2 1 3 3] both cost 5; the smaller id sequence wins.
	if want := []int{1, 2, 3, 3}; !equalSeq(r.Nodes(), want) {
		t.Fatalf("nodes %v, want %v", r.Nodes(), want)
	}
	b, err := SolveBrute(p)
	if err != nil {
		t.Fatal(err)
	}
	if !equalSeq(b.Nodes(), r.Nodes()) || b.TotalDist != r.TotalDist {
		t.Fatalf("brute force disagrees: (%d, %v)", b.TotalDist, b.Nodes())
	}
}

func TestJointRouteBeatsIndependentShortestPaths(t *testing.T) {
	p := Problem{
		NumNodes: 3,
		Dist:     completeDist(3, 99, [3]int{0, 1, 1}, [3]int{1, 2, 1}, [3]int{0, 2, 2}),
		Capacity: 2,
		Start:    0,
		Requests: []Request{
			{ID: 0, Pickup: 1, Drop: 2, Weight: 1},
			{ID: 1, Pickup: 2, Drop: 1, Weight: 1},
		},
	}
	joint, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	// Sharing legs: 0->1->2->1->0 costs 4.
	if joint.TotalDist != 4 {
		t.Fatalf("joint cost %d, want 4", joint.TotalDist)
	}
	independent := 0
	for _, q := range p.Requests {
		solo, err := Solve(Problem{
			NumNodes: p.NumNodes, Dist: p.Dist, Capacity: p.Capacity,
			Start: p.Start, Requests: []Request{q},
		})
		if err != nil {
			t.Fatal(err)
		}
		independent += solo.TotalDist
	}
	if independent != 8 {
		t.Fatalf("independent sum %d, want 8", independent)
	}
	if joint.TotalDist >= independent {
		t.Fatalf("joint cost %d not below independent sum %d", joint.TotalDist, independent)
	}
}

func TestCapacityShapesLoadTrajectory(t *testing.T) {
	p := Problem{
		NumNodes: 3,
		Dist:     completeDist(3, 99, [3]int{0, 1, 1}, [3]int{1, 2, 1}, [3]int{0, 2, 5}),
		Capacity: 3,
		Start:    0,
		Requests: []Request{
			{ID: 0, Pickup: 1, Drop: 2, Weight: 3},
			{ID: 1, Pickup: 2, Drop: 1, Weight: 3},
		},
	}
	r, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	// The two items never fit together, so the vehicle must drop the
	// first before picking the second: loads 3,0,3,0.
	if want := []int{3, 0, 3, 0}; !equalSeq(r.Loads(), want) {
		t.Fatalf("loads %v, want %v", r.Loads(), want)
	}
	if r.TotalDist != 4 {
		t.Fatalf("TotalDist %d, want 4", r.TotalDist)
	}
	checkRoute(t, p, r)
}

func TestInfeasibleWhenItemExceedsCapacity(t *testing.T) {
	p := Problem{
		NumNodes: 2,
		Dist:     completeDist(2, 1),
		Capacity: 2,
		Start:    0,
		Requests: []Request{{ID: 0, Pickup: 1, Drop: 0, Weight: 3}},
	}
	if _, err := Solve(p); err != ErrInfeasible {
		t.Fatalf("Solve err %v, want ErrInfeasible", err)
	}
	if _, err := SolveBrute(p); err != ErrInfeasible {
		t.Fatalf("SolveBrute err %v, want ErrInfeasible", err)
	}
}

func TestValidation(t *testing.T) {
	good := Problem{
		NumNodes: 2,
		Dist:     completeDist(2, 1),
		Capacity: 1,
		Start:    0,
		Requests: []Request{{ID: 0, Pickup: 0, Drop: 1, Weight: 1}},
	}
	if _, err := Solve(good); err != nil {
		t.Fatalf("valid instance rejected: %v", err)
	}
	cases := map[string]Problem{
		"negative distance": {NumNodes: 2, Dist: [][]int{{0, -1}, {1, 0}}, Capacity: 1},
		"ragged matrix":     {NumNodes: 2, Dist: [][]int{{0, 1}}, Capacity: 1},
		"bad start":         {NumNodes: 2, Dist: completeDist(2, 1), Capacity: 1, Start: 5},
		"too many requests": {NumNodes: 2, Dist: completeDist(2, 1), Capacity: 1, Start: 0,
			Requests: []Request{{}, {}, {}, {}, {}, {}, {}}},
		"duplicate ids": {NumNodes: 2, Dist: completeDist(2, 1), Capacity: 1, Start: 0,
			Requests: []Request{{ID: 1, Pickup: 0, Drop: 1}, {ID: 1, Pickup: 0, Drop: 1}}},
		"node out of range": {NumNodes: 2, Dist: completeDist(2, 1), Capacity: 1, Start: 0,
			Requests: []Request{{ID: 0, Pickup: 0, Drop: 9}}},
	}
	for name, p := range cases {
		if _, err := Solve(p); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}
