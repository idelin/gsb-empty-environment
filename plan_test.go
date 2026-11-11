package netsec

import (
	"errors"
	"math/rand"
	"sort"
	"testing"
)

func stateOf(ids []string, enabled ...string) map[string]bool {
	on := make(map[string]bool, len(enabled))
	for _, id := range enabled {
		on[id] = true
	}
	state := make(map[string]bool, len(ids))
	for _, id := range ids {
		state[id] = on[id]
	}
	return state
}

func edgeIDs(edges []Edge) []string {
	ids := make([]string, len(edges))
	for i, edge := range edges {
		ids[i] = edge.EdgeID
	}
	return ids
}

// independentSafe recomputes safety directly from the request, mirroring the
// adjacency semantics without reusing any production helper.
func independentSafe(req *Request, state map[string]bool) bool {
	nodes := map[string]map[string]bool{}
	add := func(node string) {
		if nodes[node] == nil {
			nodes[node] = map[string]bool{}
		}
	}
	for _, edge := range req.Edges {
		add(edge.From)
		add(edge.To)
		if state[edge.EdgeID] {
			nodes[edge.From][edge.To] = true
			nodes[edge.To][edge.From] = true
		}
	}
	for _, pair := range req.ForbiddenReach {
		seen := map[string]bool{pair.From: true}
		queue := []string{pair.From}
		for len(queue) > 0 {
			node := queue[0]
			queue = queue[1:]
			for next := range nodes[node] {
				if next == pair.To {
					return false
				}
				if !seen[next] {
					seen[next] = true
					queue = append(queue, next)
				}
			}
		}
	}
	return true
}

func replay(t *testing.T, req *Request, result *Result) map[string]bool {
	t.Helper()
	state := make(map[string]bool, len(req.Initial))
	for id, value := range req.Initial {
		state[id] = value
	}
	if !independentSafe(req, state) {
		t.Fatalf("initial state already violates a constraint")
	}
	for index, step := range result.Steps {
		current, ok := state[step.EdgeID]
		if !ok {
			t.Fatalf("step %d references unknown edge %q", index, step.EdgeID)
		}
		state[step.EdgeID] = !current
		if state[step.EdgeID] != step.Enabled {
			t.Fatalf("step %d reports wrong resulting switch value", index)
		}
		if !independentSafe(req, state) {
			t.Fatalf("step %d opens a forbidden path: %v", index, step)
		}
	}
	return state
}

func mapsEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func TestPlanShortestAndLexicographic(t *testing.T) {
	edges := []Edge{
		{EdgeID: "e0", From: "a", To: "b"},
		{EdgeID: "e1", From: "c", To: "d"},
	}
	ids := edgeIDs(edges)
	req := &Request{
		Edges:   edges,
		Initial: stateOf(ids),
		Goal:    stateOf(ids, "e0", "e1"),
		// Disconnected edges mean no forbidden pair is ever connected, so the
		// constraint never prunes a state and both length-2 orders are valid.
		ForbiddenReach: []ForbiddenPair{{From: "a", To: "c"}},
	}

	result, err := Plan(req)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if !result.Feasible {
		t.Fatalf("expected feasible migration, reason=%s", result.Reason)
	}
	// Two flips are required. Both orders are safe; the lexicographically
	// smaller edge ID must come first.
	wantSteps := []string{"e0", "e1"}
	if len(result.Steps) != len(wantSteps) {
		t.Fatalf("got %d steps, want %d", len(result.Steps), len(wantSteps))
	}
	for i, id := range wantSteps {
		if result.Steps[i].EdgeID != id {
			t.Fatalf("step %d = %q, want %q", i, result.Steps[i].EdgeID, id)
		}
	}

	final := replay(t, req, result)
	if !mapsEqual(final, req.Goal) {
		t.Fatalf("replay ended at %v, want goal %v", final, req.Goal)
	}

	// The alternative order [e1, e0] must be equally short and safe.
	alt := &Request{
		Edges:          req.Edges,
		Initial:        req.Initial,
		Goal:           req.Goal,
		ForbiddenReach: req.ForbiddenReach,
	}
	altFirst := stateOf(ids, "e1")
	if !independentSafe(alt, altFirst) {
		t.Fatalf("alternative first intermediate state should be safe")
	}

	ok, err := Verify(req, result)
	if err != nil || !ok {
		t.Fatalf("Verify = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestPlanZeroSteps(t *testing.T) {
	edges := []Edge{{EdgeID: "link", From: "x", To: "y"}}
	ids := edgeIDs(edges)
	req := &Request{
		Edges:          edges,
		Initial:        stateOf(ids),
		Goal:           stateOf(ids),
		ForbiddenReach: []ForbiddenPair{{From: "x", To: "y"}},
	}
	result, err := Plan(req)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if !result.Feasible || len(result.Steps) != 0 {
		t.Fatalf("expected zero-step plan, got feasible=%v steps=%d",
			result.Feasible, len(result.Steps))
	}
	if result.Steps == nil {
		t.Fatalf("Steps should be an empty non-nil slice")
	}
	ok, _ := Verify(req, result)
	if !ok {
		t.Fatal("Verify rejected a valid zero-step plan")
	}
}

func TestPlanInfeasibleGoalEvidence(t *testing.T) {
	edges := []Edge{
		{EdgeID: "e0", From: "a", To: "b"},
		{EdgeID: "e1", From: "b", To: "c"},
	}
	ids := edgeIDs(edges)
	req := &Request{
		Edges:          edges,
		Initial:        stateOf(ids),
		Goal:           stateOf(ids, "e0", "e1"),
		ForbiddenReach: []ForbiddenPair{{From: "a", To: "c"}},
	}

	result, err := Plan(req)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if result.Feasible {
		t.Fatal("expected infeasible result")
	}
	if result.Reason == "" {
		t.Fatal("infeasible result must explain the reason")
	}

	// The two single-edge states are safe and reachable; only the all-on goal
	// connects a to c and is unsafe, so the safe component has three states.
	if result.Evidence.TotalStates != 4 {
		t.Fatalf("total states = %d, want 4", result.Evidence.TotalStates)
	}
	if len(result.Evidence.VisitedSafe) != 3 {
		t.Fatalf("visited %d safe states, want 3", len(result.Evidence.VisitedSafe))
	}
	if len(result.Evidence.Blocked) != 1 {
		t.Fatalf("blocked %d states, want 1", len(result.Evidence.Blocked))
	}
	blockedHasGoal := false
	for _, record := range result.Evidence.Blocked {
		if mapsEqual(record.State, req.Goal) {
			blockedHasGoal = true
		}
		if independentSafe(req, record.State) {
			t.Fatalf("blocked state %v is actually safe", record.State)
		}
	}
	if !blockedHasGoal {
		t.Fatal("goal state missing from blocked evidence")
	}

	ok, _ := Verify(req, result)
	if !ok {
		t.Fatal("Verify rejected consistent infeasible evidence")
	}
}

func TestPlanUnsafeInitial(t *testing.T) {
	edges := []Edge{{EdgeID: "e0", From: "a", To: "b"}}
	ids := edgeIDs(edges)
	req := &Request{
		Edges:          edges,
		Initial:        stateOf(ids, "e0"),
		Goal:           stateOf(ids),
		ForbiddenReach: []ForbiddenPair{{From: "a", To: "b"}},
	}
	result, err := Plan(req)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if result.Feasible {
		t.Fatal("unsafe initial state cannot be feasible")
	}
	if len(result.Evidence.VisitedSafe) != 0 || len(result.Evidence.Blocked) != 1 {
		t.Fatalf("want zero visited and one blocked start, got visited=%d blocked=%d",
			len(result.Evidence.VisitedSafe), len(result.Evidence.Blocked))
	}
	ok, _ := Verify(req, result)
	if !ok {
		t.Fatal("Verify rejected unsafe-initial evidence")
	}
}

func TestPlanValidationErrors(t *testing.T) {
	good := Edge{EdgeID: "e0", From: "a", To: "b"}
	cases := map[string]*Request{
		"nil request": nil,
		"too many edges": {
			Edges: func() []Edge {
				edges := make([]Edge, MaxEdges+1)
				for i := range edges {
					edges[i] = Edge{
						EdgeID: "e" + string(rune('0'+i)),
						From:   "a",
						To:     "b",
					}
				}
				return edges
			}(),
		},
		"duplicate edge id": {
			Edges: []Edge{good, {EdgeID: "e0", From: "b", To: "c"}},
		},
		"unknown forbidden node": {
			Edges:          []Edge{good},
			ForbiddenReach: []ForbiddenPair{{From: "a", To: "ghost"}},
		},
		"empty edge id": {
			Edges: []Edge{{EdgeID: "", From: "a", To: "b"}},
		},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Plan(req); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("got error %v, want ErrInvalidRequest", err)
			}
		})
	}

	ids := []string{"e0"}
	base := &Request{
		Edges:   []Edge{good},
		Initial: stateOf(ids),
		Goal:    stateOf(ids),
	}
	missingKey := &Request{
		Edges:   base.Edges,
		Initial: map[string]bool{},
		Goal:    stateOf(ids),
	}
	if _, err := Plan(missingKey); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("missing key: got %v, want ErrInvalidState", err)
	}
	extraKey := &Request{
		Edges:   base.Edges,
		Initial: map[string]bool{"e0": false, "nope": true},
		Goal:    stateOf(ids),
	}
	if _, err := Plan(extraKey); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("extra key: got %v, want ErrInvalidState", err)
	}
}

func TestPlanTwelveEdges(t *testing.T) {
	edges := make([]Edge, MaxEdges)
	ids := make([]string, MaxEdges)
	for i := range edges {
		ids[i] = "e" + string(rune('0'+i))
		edges[i] = Edge{EdgeID: ids[i], From: "root", To: "leaf" + ids[i]}
	}
	// Forbid root reaching each leaf when its edge is enabled, so every safe
	// state keeps all edges off.
	pairs := make([]ForbiddenPair, MaxEdges)
	for i, id := range ids {
		pairs[i] = ForbiddenPair{From: "root", To: "leaf" + id}
	}
	req := &Request{
		Edges:          edges,
		Initial:        stateOf(ids),
		Goal:           stateOf(ids),
		ForbiddenReach: pairs,
	}
	result, err := Plan(req)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if result.Evidence.TotalStates != 1<<MaxEdges {
		t.Fatalf("total states = %d, want %d",
			result.Evidence.TotalStates, 1<<MaxEdges)
	}
	if !result.Feasible || len(result.Steps) != 0 {
		t.Fatalf("all-off start/goal should be a zero-step plan")
	}
	if len(result.Evidence.VisitedSafe) != 1 {
		t.Fatalf("visited %d states, want 1", len(result.Evidence.VisitedSafe))
	}
	if len(result.Evidence.Blocked) != MaxEdges {
		t.Fatalf("blocked %d states, want %d",
			len(result.Evidence.Blocked), MaxEdges)
	}
	ok, _ := Verify(req, result)
	if !ok {
		t.Fatal("Verify rejected 12-edge evidence")
	}
}

func TestPlanDoesNotMutateRequest(t *testing.T) {
	edges := []Edge{{EdgeID: "e0", From: "a", To: "b"}}
	ids := edgeIDs(edges)
	req := &Request{
		Edges:          edges,
		Initial:        stateOf(ids, "e0"),
		Goal:           stateOf(ids),
		ForbiddenReach: []ForbiddenPair{{From: "a", To: "b"}},
	}
	before := map[string]bool{}
	for key, value := range req.Initial {
		before[key] = value
	}
	if _, err := Plan(req); err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if !mapsEqual(req.Initial, before) {
		t.Fatalf("Plan mutated Initial: before=%v after=%v", before, req.Initial)
	}
}

// TestPlanRandomExhaustive generates many small random networks and checks
// every produced plan against an independent full-state BFS oracle, while
// confirming Verify accepts honest results and rejects tampered ones.
func TestPlanRandomExhaustive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))
	for iteration := 0; iteration < 400; iteration++ {
		req := randomRequest(rng)
		result, err := Plan(req)
		if err != nil {
			t.Fatalf("iteration %d: unexpected error %v", iteration, err)
		}

		oracleFeasible, oracleSteps := oraclePlan(rng, req)
		if result.Feasible != oracleFeasible {
			t.Fatalf("iteration %d: feasibility got %v want %v",
				iteration, result.Feasible, oracleFeasible)
		}
		if oracleFeasible {
			if len(result.Steps) != len(oracleSteps) {
				t.Fatalf("iteration %d: length got %d want %d",
					iteration, len(result.Steps), len(oracleSteps))
			}
			for i := range oracleSteps {
				if result.Steps[i].EdgeID != oracleSteps[i] {
					t.Fatalf("iteration %d: step %d got %q want %q",
						iteration, i, result.Steps[i].EdgeID, oracleSteps[i])
				}
			}
			final := replay(t, req, result)
			if !mapsEqual(final, req.Goal) {
				t.Fatalf("iteration %d: replay missed the goal", iteration)
			}
		} else if len(result.Steps) != 0 {
			t.Fatalf("iteration %d: infeasible result carries steps", iteration)
		}

		ok, err := Verify(req, result)
		if err != nil || !ok {
			t.Fatalf("iteration %d: Verify = (%v, %v), want (true, nil)",
				iteration, ok, err)
		}

		if len(result.Steps) > 0 {
			tampered := *result
			tampered.Steps = append([]Step(nil), result.Steps...)
			tampered.Steps[0].EdgeID = req.Edges[0].EdgeID
			// When the swap happens to be identical, flip Enabled instead.
			if len(result.Steps) > 0 && tampered.Steps[0].EdgeID == result.Steps[0].EdgeID {
				tampered.Steps[0].Enabled = !tampered.Steps[0].Enabled
			}
			if ok, _ := Verify(req, &tampered); ok {
				t.Fatalf("iteration %d: Verify accepted a tampered plan", iteration)
			}
		}
	}
}

func randomRequest(rng *rand.Rand) *Request {
	edgeCount := 1 + rng.Intn(6)
	nodeCount := 2 + rng.Intn(5)
	edges := make([]Edge, edgeCount)
	ids := make([]string, edgeCount)
	nodeName := func(index int) string {
		return "n" + string(rune('a'+index))
	}
	for i := range edges {
		ids[i] = "e" + string(rune('a'+i))
		from := rng.Intn(nodeCount)
		to := rng.Intn(nodeCount)
		edges[i] = Edge{EdgeID: ids[i], From: nodeName(from), To: nodeName(to)}
	}

	usedNodes := make(map[string]bool)
	for _, edge := range edges {
		usedNodes[edge.From] = true
		usedNodes[edge.To] = true
	}
	available := make([]string, 0, len(usedNodes))
	for name := range usedNodes {
		available = append(available, name)
	}
	sort.Strings(available)

	pairCount := rng.Intn(3)
	pairs := make([]ForbiddenPair, pairCount)
	for i := range pairs {
		if len(available) < 2 {
			pairs = pairs[:i]
			break
		}
		aIndex := rng.Intn(len(available))
		bIndex := rng.Intn(len(available) - 1)
		if bIndex >= aIndex {
			bIndex++
		}
		pairs[i] = ForbiddenPair{From: available[aIndex], To: available[bIndex]}
	}

	randomState := func() map[string]bool {
		state := make(map[string]bool, edgeCount)
		for _, id := range ids {
			state[id] = rng.Intn(2) == 1
		}
		return state
	}
	return &Request{
		Edges:          edges,
		Initial:        randomState(),
		Goal:           randomState(),
		ForbiddenReach: pairs,
	}
}

// oraclePlan runs an independent BFS with randomized neighbor order but
// canonical lexicographic tie resolution, returning edge IDs of the plan.
func oraclePlan(rng *rand.Rand, req *Request) (bool, []string) {
	ordered := make([]Edge, len(req.Edges))
	copy(ordered, req.Edges)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].EdgeID < ordered[j].EdgeID
	})
	n := len(ordered)
	total := 1 << n
	maskOf := func(state map[string]bool) int {
		var mask int
		for i, edge := range ordered {
			if state[edge.EdgeID] {
				mask |= 1 << i
			}
		}
		return mask
	}
	safe := make([]bool, total)
	for mask := 0; mask < total; mask++ {
		safe[mask] = independentSafe(req, maskState(ordered, mask))
	}
	start := maskOf(req.Initial)
	goal := maskOf(req.Goal)
	if !safe[start] || !safe[goal] {
		return false, nil
	}

	parent := make([]int, total)
	for i := range parent {
		parent[i] = -1
	}
	parent[start] = start
	order := rng.Perm(n)
	queue := []int{start}
	for head := 0; head < len(queue); head++ {
		current := queue[head]
		for _, edgeIndex := range order {
			next := current ^ (1 << edgeIndex)
			if parent[next] != -1 || !safe[next] {
				continue
			}
			parent[next] = current
			queue = append(queue, next)
		}
	}
	if parent[goal] == -1 {
		return false, nil
	}

	// Random neighbor discovery may yield a non-lexicographic path; recompute
	// the canonical winner by BFS layers with ascending edge order.
	return true, canonicalOraclePath(ordered, safe, start, goal)
}

func maskState(ordered []Edge, mask int) map[string]bool {
	state := make(map[string]bool, len(ordered))
	for i, edge := range ordered {
		state[edge.EdgeID] = mask&(1<<i) != 0
	}
	return state
}

func canonicalOraclePath(ordered []Edge, safe []bool, start, goal int) []string {
	n := len(ordered)
	parent := make([]int, len(safe))
	parentEdge := make([]int, len(safe))
	for i := range parent {
		parent[i] = -1
		parentEdge[i] = -1
	}
	parent[start] = start
	queue := []int{start}
	for head := 0; head < len(queue); head++ {
		current := queue[head]
		for edgeIndex := 0; edgeIndex < n; edgeIndex++ {
			next := current ^ (1 << edgeIndex)
			if parent[next] != -1 || !safe[next] {
				continue
			}
			parent[next] = current
			parentEdge[next] = edgeIndex
			queue = append(queue, next)
		}
	}
	var reversed []string
	for mask := goal; mask != start; mask = parent[mask] {
		reversed = append(reversed, ordered[parentEdge[mask]].EdgeID)
	}
	plan := make([]string, len(reversed))
	for i, id := range reversed {
		plan[len(plan)-1-i] = id
	}
	return plan
}
