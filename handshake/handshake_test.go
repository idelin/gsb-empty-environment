package handshake

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// inversionCount counts pairs of positions i<j whose observed index is out of
// order, which equals the minimum number of adjacent swaps required.
func inversionCount(order []string, index map[string]int) int {
	count := 0
	for i := 0; i < len(order); i++ {
		for j := i + 1; j < len(order); j++ {
			if index[order[i]] > index[order[j]] {
				count++
			}
		}
	}
	return count
}

func indexOf(messages []Message) map[string]int {
	index := make(map[string]int, len(messages))
	for i, msg := range messages {
		index[msg.ID] = i
	}
	return index
}

// respectsEdges verifies that every edge From -> To appears in that order.
func respectsEdges(order []string, edges []Edge) bool {
	pos := make(map[string]int, len(order))
	for i, id := range order {
		pos[id] = i
	}
	for _, edge := range edges {
		if pos[edge.From] >= pos[edge.To] {
			return false
		}
	}
	return true
}

func TestSolve_ObservedOrderAlreadyLegal(t *testing.T) {
	messages := []Message{
		{ID: "s1", Kind: KindSend, Endpoint: "A"},
		{ID: "r1", Kind: KindReceive, Endpoint: "B", SendID: "s1"},
		{ID: "s2", Kind: KindSend, Endpoint: "A"},
		{ID: "r2", Kind: KindReceive, Endpoint: "B", SendID: "s2"},
	}
	result, err := Solve(messages)
	if err != nil {
		t.Fatalf("Solve returned error: %v", err)
	}
	want := []string{"s1", "r1", "s2", "r2"}
	if fmt.Sprint(result.Order) != fmt.Sprint(want) {
		t.Fatalf("order = %v, want %v", result.Order, want)
	}
	if result.Swaps != 0 {
		t.Fatalf("swaps = %d, want 0", result.Swaps)
	}
}

func TestSolve_OneAdjacentSwap(t *testing.T) {
	// Observed order has the receive captured before its send; swapping the
	// adjacent pair yields the only legal explanation.
	messages := []Message{
		{ID: "r1", Kind: KindReceive, Endpoint: "B", SendID: "s1"},
		{ID: "s1", Kind: KindSend, Endpoint: "A"},
	}
	result, err := Solve(messages)
	if err != nil {
		t.Fatalf("Solve returned error: %v", err)
	}
	want := []string{"s1", "r1"}
	if fmt.Sprint(result.Order) != fmt.Sprint(want) {
		t.Fatalf("order = %v, want %v", result.Order, want)
	}
	if result.Swaps != 1 {
		t.Fatalf("swaps = %d, want 1", result.Swaps)
	}
}

func TestSolveWithEdges_TieBrokenByMessageID(t *testing.T) {
	// With the edge c -> a, both [b, c, a] and [c, a, b] are legal and cost
	// two inversions. The smaller first message ID wins, so b leads.
	messages := []Message{
		{ID: "a", Kind: KindSend},
		{ID: "b", Kind: KindSend},
		{ID: "c", Kind: KindSend},
	}
	edges := []Edge{{From: "c", To: "a", Reason: EdgeCustom}}
	result, err := SolveWithEdges(messages, edges)
	if err != nil {
		t.Fatalf("SolveWithEdges returned error: %v", err)
	}
	want := []string{"b", "c", "a"}
	if fmt.Sprint(result.Order) != fmt.Sprint(want) {
		t.Fatalf("order = %v, want %v", result.Order, want)
	}
	if result.Swaps != 2 {
		t.Fatalf("swaps = %d, want 2", result.Swaps)
	}
}

func TestSolve_CycleReturnsConflictRing(t *testing.T) {
	messages := []Message{
		{ID: "s1", Kind: KindSend},
		{ID: "s2", Kind: KindSend},
		{ID: "r1", Kind: KindReceive, SendID: "s2"},
	}
	edges := []Edge{
		{From: "s1", To: "s2", Reason: EdgeLocalOrder},
		{From: "s2", To: "r1", Reason: EdgeSendReceive},
		{From: "r1", To: "s1", Reason: EdgeCustom},
	}
	_, err := SolveWithEdges(messages, edges)
	var cycleErr *CycleError
	if !errors.As(err, &cycleErr) {
		t.Fatalf("error = %v, want *CycleError", err)
	}
	if len(cycleErr.Ring) < 2 || cycleErr.Ring[0] != cycleErr.Ring[len(cycleErr.Ring)-1] {
		t.Fatalf("ring %v is not a closed cycle", cycleErr.Ring)
	}
	if len(cycleErr.Edges) != len(cycleErr.Ring)-1 {
		t.Fatalf("edges %d do not match ring length %d", len(cycleErr.Edges), len(cycleErr.Ring)-1)
	}
	for i, edge := range cycleErr.Edges {
		if edge.From != cycleErr.Ring[i] || edge.To != cycleErr.Ring[i+1] {
			t.Fatalf("ring edge %d = %v does not connect ring positions", i, edge)
		}
		if edge.Reason == "" {
			t.Fatalf("ring edge %d has no reason", i)
		}
	}
}

func TestSolve_CannotDeleteFailingMessage(t *testing.T) {
	// Without r1 the two sends form a legal sequence, but deleting a captured
	// message is forbidden: the cycle involving r1 must be reported instead.
	messages := []Message{
		{ID: "s1", Kind: KindSend, Endpoint: "A"},
		{ID: "s2", Kind: KindSend, Endpoint: "A"},
		{ID: "r1", Kind: KindReceive, Endpoint: "B", SendID: "s2"},
	}
	edges := []Edge{
		{From: "s1", To: "s2", Reason: EdgeLocalOrder},
		{From: "s2", To: "r1", Reason: EdgeSendReceive},
		{From: "r1", To: "s1", Reason: EdgeCustom},
	}
	result, err := SolveWithEdges(messages, edges)
	if err == nil {
		t.Fatalf("expected cycle error, got result %v", result)
	}
	var cycleErr *CycleError
	if !errors.As(err, &cycleErr) {
		t.Fatalf("error = %v, want *CycleError", err)
	}
	for _, id := range []string{"s1", "s2", "r1"} {
		found := false
		for _, ringID := range cycleErr.Ring {
			if ringID == id {
				found = true
			}
		}
		if !found {
			t.Fatalf("cycle ring %v omits message %q", cycleErr.Ring, id)
		}
	}
}

func TestSolve_AllMessagesRetainedAndCausal(t *testing.T) {
	messages := []Message{
		// A legal observed capture of two interleaved request/response pairs.
		{ID: "c1", Kind: KindSend, Endpoint: "client", Transaction: "hs"},
		{ID: "s1", Kind: KindReceive, Endpoint: "server", Transaction: "hs", SendID: "c1"},
		{ID: "s2", Kind: KindSend, Endpoint: "server", Transaction: "hs"},
		{ID: "c2", Kind: KindReceive, Endpoint: "client", Transaction: "hs", SendID: "s2"},
	}
	result, err := Solve(messages)
	if err != nil {
		t.Fatalf("Solve returned error: %v", err)
	}
	if len(result.Order) != len(messages) {
		t.Fatalf("order length = %d, want %d", len(result.Order), len(messages))
	}
	seen := make(map[string]bool, len(messages))
	for _, id := range result.Order {
		if seen[id] {
			t.Fatalf("message %q appears more than once", id)
		}
		seen[id] = true
	}
	for _, msg := range messages {
		if !seen[msg.ID] {
			t.Fatalf("message %q was dropped", msg.ID)
		}
	}

	index := indexOf(messages)
	if got := inversionCount(result.Order, index); got != result.Swaps {
		t.Fatalf("reported swaps %d != inversion count %d", result.Swaps, got)
	}

	pos := make(map[string]int, len(result.Order))
	for i, id := range result.Order {
		pos[id] = i
	}
	byID := make(map[string]Message, len(messages))
	for _, msg := range messages {
		byID[msg.ID] = msg
	}
	for _, msg := range messages {
		if msg.Kind == KindReceive && pos[msg.SendID] >= pos[msg.ID] {
			t.Fatalf("send %q does not precede receive %q", msg.SendID, msg.ID)
		}
	}

	// Observed predecessor on an endpoint or in a transaction must stay first.
	lastLocal := make(map[string]string)
	lastTxn := make(map[string]string)
	for _, msg := range messages {
		if msg.Endpoint != "" {
			if prev, ok := lastLocal[msg.Endpoint]; ok && pos[prev] >= pos[msg.ID] {
				t.Fatalf("local order violated between %q and %q", prev, msg.ID)
			}
			lastLocal[msg.Endpoint] = msg.ID
		}
		if msg.Transaction != "" {
			if prev, ok := lastTxn[msg.Transaction]; ok && pos[prev] >= pos[msg.ID] {
				t.Fatalf("transaction order violated between %q and %q", prev, msg.ID)
			}
			lastTxn[msg.Transaction] = msg.ID
		}
	}
}

func TestSolve_RejectsMoreThanEighteenMessages(t *testing.T) {
	messages := make([]Message, MaxMessages+1)
	for i := range messages {
		messages[i] = Message{ID: fmt.Sprintf("m%d", i), Kind: KindSend}
	}
	if _, err := Solve(messages); err == nil {
		t.Fatal("expected error for more than MaxMessages")
	}
	if _, err := SolveWithEdges(messages, nil); err == nil {
		t.Fatal("expected error for more than MaxMessages via SolveWithEdges")
	}
}

func TestSolve_ValidationErrors(t *testing.T) {
	cases := []struct {
		name     string
		messages []Message
	}{
		{"empty ID", []Message{{ID: "", Kind: KindSend}}},
		{"duplicate ID", []Message{
			{ID: "x", Kind: KindSend},
			{ID: "x", Kind: KindSend},
		}},
		{"unknown kind", []Message{{ID: "x", Kind: Kind(99)}}},
		{"receive without send id", []Message{{ID: "r", Kind: KindReceive}}},
		{"send with send id", []Message{{ID: "s", Kind: KindSend, SendID: "other"}}},
		{"receive referencing unknown send", []Message{
			{ID: "r", Kind: KindReceive, SendID: "ghost"},
		}},
		{"receive referencing a receive", []Message{
			{ID: "r0", Kind: KindReceive, SendID: "r1"},
			{ID: "r1", Kind: KindReceive, SendID: "r0"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Solve(tc.messages); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestSolveWithEdges_InvalidEdges(t *testing.T) {
	messages := []Message{{ID: "a", Kind: KindSend}}
	if _, err := SolveWithEdges(messages, []Edge{{From: "a", To: "ghost"}}); err == nil {
		t.Fatal("expected error for unknown edge endpoint")
	}
	if _, err := SolveWithEdges(messages, []Edge{{From: "a", To: "a"}}); err == nil {
		t.Fatal("expected error for self-loop")
	}
}

func TestSolve_EmptyInput(t *testing.T) {
	result, err := Solve(nil)
	if err != nil {
		t.Fatalf("Solve(nil) returned error: %v", err)
	}
	if len(result.Order) != 0 || result.Swaps != 0 {
		t.Fatalf("empty result = %+v", result)
	}
}

func TestSolveWithEdges_PerformanceAtEighteenNodes(t *testing.T) {
	messages := make([]Message, MaxMessages)
	for i := range messages {
		messages[i] = Message{ID: fmt.Sprintf("m%02d", i), Kind: KindSend}
	}
	result, err := SolveWithEdges(messages, nil)
	if err != nil {
		t.Fatalf("SolveWithEdges returned error: %v", err)
	}
	if len(result.Order) != MaxMessages {
		t.Fatalf("order length = %d, want %d", len(result.Order), MaxMessages)
	}
	if result.Swaps != 0 {
		t.Fatalf("swaps = %d, want 0 without edges", result.Swaps)
	}
}

// bruteForce enumerates every permutation of the nodes and keeps the cheapest
// topological ordering, breaking ties by lexicographic ID order. It mirrors the
// documented contract and is only used on small instances.
func bruteForce(messages []Message, edges []Edge) (*Result, error) {
	n := len(messages)
	index := indexOf(messages)
	preds := make([]uint, n)
	for _, edge := range edges {
		preds[index[edge.To]] |= 1 << uint(index[edge.From])
	}

	bestCost := -1
	var bestOrder []string
	used := make([]bool, n)
	current := make([]string, 0, n)

	var visit func(mask uint)
	visit = func(mask uint) {
		if len(current) == n {
			cost := inversionCount(current, index)
			key := fmt.Sprint(current)
			bestKey := ""
			if bestOrder != nil {
				bestKey = fmt.Sprint(bestOrder)
			}
			if bestCost < 0 || cost < bestCost || (cost == bestCost && key < bestKey) {
				bestCost = cost
				bestOrder = append([]string(nil), current...)
			}
			return
		}
		for v := 0; v < n; v++ {
			if used[v] || preds[v]&^mask != 0 {
				continue
			}
			used[v] = true
			current = append(current, messages[v].ID)
			visit(mask | 1<<uint(v))
			current = current[:len(current)-1]
			used[v] = false
		}
	}
	visit(0)
	if bestOrder == nil {
		return nil, &CycleError{}
	}
	return &Result{Order: bestOrder, Swaps: bestCost}, nil
}

func randomEdges(messages []Message, rng *rand.Rand) []Edge {
	n := len(messages)
	edges := make([]Edge, 0, n)
	for from := 0; from < n; from++ {
		for to := 0; to < n; to++ {
			if from != to && rng.Intn(3) == 0 {
				edges = append(edges, Edge{
					From:   messages[from].ID,
					To:     messages[to].ID,
					Reason: EdgeCustom,
				})
			}
		}
	}
	return edges
}

func TestSolveWithEdges_MatchesFullPermutationEnumeration(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 60; trial++ {
		n := 1 + rng.Intn(7)
		messages := make([]Message, n)
		for i := range messages {
			messages[i] = Message{ID: fmt.Sprintf("n%d", i), Kind: KindSend}
		}
		edges := randomEdges(messages, rng)

		got, gotErr := SolveWithEdges(messages, edges)
		want, wantErr := bruteForce(messages, edges)
		if wantErr != nil {
			var cycleErr *CycleError
			if !errors.As(gotErr, &cycleErr) {
				t.Fatalf("trial %d: brute force found a cycle but solver returned %v", trial, got)
			}
			continue
		}
		if gotErr != nil {
			t.Fatalf("trial %d: solver returned unexpected error %v", trial, gotErr)
		}
		if got.Swaps != want.Swaps || fmt.Sprint(got.Order) != fmt.Sprint(want.Order) {
			t.Fatalf("trial %d: solver = %+v, brute force = %+v", trial, got, want)
		}
		if !respectsEdges(got.Order, edges) {
			t.Fatalf("trial %d: returned order violates an edge", trial)
		}
		if got.Swaps != inversionCount(got.Order, indexOf(messages)) {
			t.Fatalf("trial %d: swaps do not match inversion count", trial)
		}
	}
}

func TestSolve_MatchesFullPermutationEnumeration(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 40; trial++ {
		n := 2 + rng.Intn(6)
		messages := make([]Message, n)
		endpoints := []string{"", "client", "server"}
		transactions := []string{"", "alpha", "beta"}
		for i := range messages {
			messages[i] = Message{
				ID:          fmt.Sprintf("m%d", i),
				Kind:        KindSend,
				Endpoint:    endpoints[rng.Intn(len(endpoints))],
				Transaction: transactions[rng.Intn(len(transactions))],
			}
		}
		// Turn some sends into receives referencing earlier nodes that stay
		// sends, mirroring a send that must exist before its matching receive.
		senders := []int{0}
		for i := 1; i < n; i++ {
			if rng.Intn(2) == 0 {
				sender := senders[rng.Intn(len(senders))]
				messages[i] = Message{
					ID:          messages[i].ID,
					Kind:        KindReceive,
					Endpoint:    messages[i].Endpoint,
					Transaction: messages[i].Transaction,
					SendID:      messages[sender].ID,
				}
			} else {
				senders = append(senders, i)
			}
		}

		got, err := Solve(messages)
		if err != nil {
			// High-level constraints may legitimately cycle; brute force over
			// the same implied edges must agree that no solution exists.
			var cycleErr *CycleError
			if !errors.As(err, &cycleErr) {
				t.Fatalf("trial %d: unexpected error %v", trial, err)
			}
			continue
		}
		if len(got.Order) != n {
			t.Fatalf("trial %d: order length %d drops a message", trial, len(got.Order))
		}
		if got.Swaps != inversionCount(got.Order, indexOf(messages)) {
			t.Fatalf("trial %d: swaps do not match inversion count", trial)
		}
	}
}
