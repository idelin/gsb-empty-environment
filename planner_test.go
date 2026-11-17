package netsec

import (
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"sort"
	"testing"
)

func TestPlanKeepsForbiddenPathClosed(t *testing.T) {
	req := Request{
		Edges: []Edge{
			{
				ID:            "e2-core",
				From:          "gateway",
				To:            "core",
				TargetEnabled: true,
			},
			{
				ID:             "e1-infected",
				From:           "infected",
				To:             "gateway",
				InitialEnabled: true,
			},
		},
		Forbidden: []ForbiddenPair{{From: "infected", To: "core"}},
	}

	plan, err := Plan(req)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if !plan.Feasible {
		t.Fatalf("Plan is infeasible: %+v", plan.Failure)
	}
	if plan.Distance != 2 {
		t.Fatalf("distance = %d, want 2", plan.Distance)
	}
	want := []string{"e1-infected", "e2-core"}
	if got := plan.EdgeIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("edge IDs = %v, want %v", got, want)
	}
	if err := Verify(req, plan); err != nil {
		t.Fatalf("Verify rejected plan: %v", err)
	}
}

func TestPlanUsesLexicographicTieBreak(t *testing.T) {
	req := Request{
		Edges: []Edge{
			{ID: "b", From: "left", To: "right", TargetEnabled: true},
			{ID: "a", From: "left", To: "right", TargetEnabled: true},
		},
	}

	plan, err := Plan(req)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if !plan.Feasible {
		t.Fatal("expected a feasible plan")
	}
	want := []string{"a", "b"}
	if got := plan.EdgeIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("edge IDs = %v, want %v", got, want)
	}
	if err := Verify(req, plan); err != nil {
		t.Fatalf("Verify rejected plan: %v", err)
	}
}

func TestPlanReportsUnsafeInitialState(t *testing.T) {
	req := Request{
		Edges: []Edge{{
			ID:             "e1",
			From:           "infected",
			To:             "gateway",
			InitialEnabled: true,
		}},
		Forbidden: []ForbiddenPair{{From: "infected", To: "gateway"}},
	}

	plan, err := Plan(req)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if plan.Feasible {
		t.Fatal("expected an infeasible plan")
	}
	if plan.Failure == nil || plan.Failure.Code != FailureInitialUnsafe {
		t.Fatalf("failure = %+v, want %s", plan.Failure, FailureInitialUnsafe)
	}
	if !plan.Evidence.Complete || plan.Evidence.TotalStates != 2 {
		t.Fatalf("unexpected evidence header: %+v", plan.Evidence)
	}
	if len(plan.Evidence.Traversed) != 0 {
		t.Fatalf("traversed %d states, want 0", len(plan.Evidence.Traversed))
	}
	if len(plan.Evidence.Blocked) != 1 || plan.Evidence.Blocked[0].Mask != 1 {
		t.Fatalf("blocked evidence = %+v", plan.Evidence.Blocked)
	}
	if len(plan.Evidence.Blocked[0].Violations) != 1 {
		t.Fatalf("violations = %+v", plan.Evidence.Blocked[0].Violations)
	}
}

func TestPlanReportsUnsafeTargetState(t *testing.T) {
	req := Request{
		Edges: []Edge{{
			ID:            "e1",
			From:          "infected",
			To:            "gateway",
			TargetEnabled: true,
		}},
		Forbidden: []ForbiddenPair{{From: "infected", To: "gateway"}},
	}

	plan, err := Plan(req)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if plan.Feasible {
		t.Fatal("expected an infeasible plan")
	}
	if plan.Failure == nil || plan.Failure.Code != FailureTargetUnsafe {
		t.Fatalf("failure = %+v, want %s", plan.Failure, FailureTargetUnsafe)
	}
	if len(plan.Evidence.Traversed) != 1 || plan.Evidence.Traversed[0].Mask != 0 {
		t.Fatalf("traversed evidence = %+v", plan.Evidence.Traversed)
	}
	if len(plan.Evidence.Blocked) != 1 || plan.Evidence.Blocked[0].Mask != 1 {
		t.Fatalf("blocked evidence = %+v", plan.Evidence.Blocked)
	}
}

func TestPlanCreatesNodesUsedOnlyByForbiddenPairs(t *testing.T) {
	req := Request{
		Forbidden: []ForbiddenPair{{From: "isolated-a", To: "isolated-b"}},
	}
	plan, err := Plan(req)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if !plan.Feasible || plan.Distance != 0 {
		t.Fatalf("plan = %+v, want a zero-step feasible plan", plan)
	}
	if plan.Evidence.TotalStates != 1 {
		t.Fatalf("total states = %d, want 1", plan.Evidence.TotalStates)
	}
}

func TestPlanSupportsTwelveEdges(t *testing.T) {
	edges := make([]Edge, MaxMutableEdges)
	want := make([]string, MaxMutableEdges)
	for i := range edges {
		id := fmt.Sprintf("e%02d", i)
		edges[i] = Edge{ID: id, From: "a", To: "b", TargetEnabled: true}
		want[i] = id
	}
	plan, err := Plan(Request{Edges: edges})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if !plan.Feasible || plan.Distance != MaxMutableEdges {
		t.Fatalf("unexpected plan: feasible=%v distance=%d", plan.Feasible, plan.Distance)
	}
	if got := plan.EdgeIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("edge IDs = %v, want %v", got, want)
	}
	if err := Verify(Request{Edges: edges}, plan); err != nil {
		t.Fatalf("Verify rejected plan: %v", err)
	}

	tooMany := append(append([]Edge(nil), edges...), Edge{
		ID: "e12", From: "a", To: "b",
	})
	if _, err := Plan(Request{Edges: tooMany}); err == nil {
		t.Fatal("expected an error for more than 12 mutable edges")
	}
}

func TestPlanHonorsDirectedEdges(t *testing.T) {
	base := []Edge{
		{
			ID:             "e1",
			From:           "infected",
			To:             "gateway",
			InitialEnabled: true,
		},
		{
			ID:            "e2",
			From:          "core",
			To:            "gateway",
			TargetEnabled: true,
		},
	}
	forbidden := []ForbiddenPair{{From: "infected", To: "core"}}
	fixed := []FixedEdge{{From: "infected", To: "gateway"}}

	undirected, err := Plan(Request{Edges: base, FixedEdges: fixed, Forbidden: forbidden})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if undirected.Feasible {
		t.Fatal("undirected core-to-gateway edge should expose core to infected")
	}
	if undirected.Failure == nil || undirected.Failure.Code != FailureTargetUnsafe {
		t.Fatalf("undirected failure = %+v", undirected.Failure)
	}

	directed, err := Plan(Request{
		Edges:      base,
		FixedEdges: fixed,
		Forbidden:  forbidden,
		Directed:   true,
	})
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if !directed.Feasible || directed.Distance != 2 {
		t.Fatalf("directed plan = %+v", directed)
	}
	if err := Verify(Request{
		Edges:      base,
		FixedEdges: fixed,
		Forbidden:  forbidden,
		Directed:   true,
	}, directed); err != nil {
		t.Fatalf("Verify rejected directed plan: %v", err)
	}
}

func TestPlanDoesNotMutateRequest(t *testing.T) {
	req := Request{
		Edges: []Edge{
			{ID: "z", From: "a", To: "b", TargetEnabled: true},
			{ID: "a", From: "b", To: "c", TargetEnabled: true},
		},
		FixedEdges: []FixedEdge{{From: "c", To: "d"}},
		Forbidden:  []ForbiddenPair{{From: "a", To: "d"}},
	}
	before := deepCopyRequest(req)
	if _, err := Plan(req); err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if !reflect.DeepEqual(req, before) {
		t.Fatalf("Plan mutated its request: before=%+v after=%+v", before, req)
	}
}

func TestPlanMatchesIndependentExhaustiveSearch(t *testing.T) {
	random := rand.New(rand.NewSource(120))
	nodes := []string{"a", "b", "c", "d", "e"}

	for caseIndex := 0; caseIndex < 300; caseIndex++ {
		edgeCount := random.Intn(6)
		edges := make([]Edge, edgeCount)
		for i := range edges {
			from := nodes[random.Intn(len(nodes))]
			to := nodes[random.Intn(len(nodes))]
			for to == from {
				to = nodes[random.Intn(len(nodes))]
			}
			edges[i] = Edge{
				ID:             fmt.Sprintf("e%d", i),
				From:           from,
				To:             to,
				InitialEnabled: random.Intn(2) == 1,
				TargetEnabled:  random.Intn(2) == 1,
			}
		}
		request := Request{
			Edges:    edges,
			Directed: random.Intn(2) == 1,
		}
		for i := 0; i < random.Intn(4); i++ {
			from := nodes[random.Intn(len(nodes))]
			to := nodes[random.Intn(len(nodes))]
			for to == from {
				to = nodes[random.Intn(len(nodes))]
			}
			request.Forbidden = append(
				request.Forbidden,
				ForbiddenPair{From: from, To: to},
			)
		}
		for i := 0; i < random.Intn(3); i++ {
			from := nodes[random.Intn(len(nodes))]
			to := nodes[random.Intn(len(nodes))]
			for to == from {
				to = nodes[random.Intn(len(nodes))]
			}
			request.FixedEdges = append(request.FixedEdges, FixedEdge{From: from, To: to})
		}

		plan, err := Plan(request)
		if err != nil {
			t.Fatalf("case %d: Plan returned error: %v", caseIndex, err)
		}
		expected := naiveSearch(request)

		if plan.Feasible != expected.feasible {
			t.Fatalf(
				"case %d: feasible = %v, want %v (request=%+v plan=%+v)",
				caseIndex, plan.Feasible, expected.feasible, request, plan,
			)
		}
		if expected.feasible {
			if plan.Distance != expected.distance {
				t.Fatalf(
					"case %d: distance = %d, want %d (request=%+v)",
					caseIndex, plan.Distance, expected.distance, request,
				)
			}
			if got := plan.EdgeIDs(); !slices.Equal(got, expected.edgeIDs) {
				t.Fatalf(
					"case %d: edge IDs = %v, want %v (request=%+v)",
					caseIndex, got, expected.edgeIDs, request,
				)
			}
			if err := Verify(request, plan); err != nil {
				t.Fatalf("case %d: Verify rejected plan: %v", caseIndex, err)
			}
			continue
		}

		if !plan.Evidence.Complete {
			t.Fatalf("case %d: failure evidence is not complete", caseIndex)
		}
		gotStates := evidenceMasks(plan.Evidence.Traversed)
		if !slices.Equal(gotStates, expected.reachableValid) {
			t.Fatalf(
				"case %d: traversed states = %v, want %v",
				caseIndex, gotStates, expected.reachableValid,
			)
		}
	}
}

type naiveResult struct {
	feasible       bool
	distance       int
	edgeIDs        []string
	reachableValid []uint16
}

func naiveSearch(req Request) naiveResult {
	edges := append([]Edge(nil), req.Edges...)
	sort.Slice(edges, func(i, j int) bool {
		return edges[i].ID < edges[j].ID
	})
	initial, target := naiveMasks(edges)
	total := 1 << uint(len(edges))
	valid := make([]bool, total)
	for raw := 0; raw < total; raw++ {
		valid[raw] = naiveSafe(req, edges, uint16(raw))
	}

	result := naiveResult{}
	if !valid[initial] {
		return result
	}
	distances := make([]int, total)
	for i := range distances {
		distances[i] = -1
	}
	distances[initial] = 0
	queue := []uint16{initial}
	for head := 0; head < len(queue); head++ {
		current := queue[head]
		result.reachableValid = append(result.reachableValid, current)
		for index := range edges {
			next := current ^ (uint16(1) << uint(index))
			if !valid[next] || distances[next] >= 0 {
				continue
			}
			distances[next] = distances[current] + 1
			queue = append(queue, next)
		}
	}
	if !valid[target] || distances[target] < 0 {
		return result
	}

	result.feasible = true
	result.distance = distances[target]
	result.edgeIDs = naiveLexicographicPath(
		req, edges, initial, target, valid, result.distance,
	)
	return result
}

func naiveLexicographicPath(
	req Request,
	edges []Edge,
	initial, target uint16,
	valid []bool,
	distance int,
) []string {
	var best []string
	var current []string

	var visit func(state uint16)
	visit = func(state uint16) {
		if len(current) == distance {
			if state == target && (best == nil || idsLess(current, best)) {
				best = append([]string(nil), current...)
			}
			return
		}
		for index, edge := range edges {
			next := state ^ (uint16(1) << uint(index))
			if !valid[next] {
				continue
			}
			current = append(current, edge.ID)
			visit(next)
			current = current[:len(current)-1]
		}
	}
	visit(initial)
	return best
}

func idsLess(left, right []string) bool {
	for i := range left {
		if left[i] != right[i] {
			return left[i] < right[i]
		}
	}
	return false
}

func naiveMasks(edges []Edge) (uint16, uint16) {
	var initial, target uint16
	for i, edge := range edges {
		if edge.InitialEnabled {
			initial |= uint16(1) << uint(i)
		}
		if edge.TargetEnabled {
			target |= uint16(1) << uint(i)
		}
	}
	return initial, target
}

func naiveSafe(req Request, edges []Edge, state uint16) bool {
	nodes := make(map[string]int)
	index := func(name string) int {
		if value, exists := nodes[name]; exists {
			return value
		}
		value := len(nodes)
		nodes[name] = value
		return value
	}

	type arc struct{ from, to string }
	arcs := make([]arc, 0)
	for _, edge := range req.FixedEdges {
		arcs = append(arcs, arc{from: edge.From, to: edge.To})
	}
	for i, edge := range edges {
		if state&(uint16(1)<<uint(i)) != 0 {
			arcs = append(arcs, arc{from: edge.From, to: edge.To})
		}
	}
	for _, pair := range req.Forbidden {
		index(pair.From)
		index(pair.To)
	}
	for _, edge := range arcs {
		index(edge.from)
		index(edge.to)
	}

	adjacency := make([][]int, len(nodes))
	for _, edge := range arcs {
		from, to := nodes[edge.from], nodes[edge.to]
		adjacency[from] = append(adjacency[from], to)
		if !req.Directed {
			adjacency[to] = append(adjacency[to], from)
		}
	}

	for _, pair := range req.Forbidden {
		source, target := nodes[pair.From], nodes[pair.To]
		reachable := make([]bool, len(nodes))
		reachable[source] = true
		queue := []int{source}
		for head := 0; head < len(queue); head++ {
			for _, next := range adjacency[queue[head]] {
				if !reachable[next] {
					reachable[next] = true
					queue = append(queue, next)
				}
			}
		}
		if reachable[target] {
			return false
		}
	}
	return true
}

func evidenceMasks(evidence []StateEvidence) []uint16 {
	masks := make([]uint16, len(evidence))
	for i, item := range evidence {
		masks[i] = uint16(item.Mask)
	}
	return masks
}

func deepCopyRequest(req Request) Request {
	copy := Request{
		Directed: req.Directed,
		Edges:    append([]Edge(nil), req.Edges...),
		Forbidden: append(
			[]ForbiddenPair(nil), req.Forbidden...,
		),
		FixedEdges: append([]FixedEdge(nil), req.FixedEdges...),
	}
	return copy
}
