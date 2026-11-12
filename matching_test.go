package netsec

import (
	"math/rand"
	"testing"
)

func ev(kind Kind, subject, nonce, op string) Event {
	return Event{Kind: kind, Subject: subject, SessionNonce: nonce, Operation: op}
}

func begin(subject, nonce, op string) Event {
	return ev(KindBegin, subject, nonce, op)
}

func end(subject, nonce, op string) Event {
	return ev(KindEnd, subject, nonce, op)
}

func TestSimpleFeasible(t *testing.T) {
	events := []Event{
		begin("alice", "n1", "login"),
		end("alice", "n1", "login"),
	}
	report, err := Analyze(events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !report.Feasible || !ValidMatching(events, report.Matching) ||
		!IsComplete(events, report.Matching) {
		t.Fatalf("expected feasible complete matching, got %+v", report)
	}
	if len(report.Matching) != 1 || report.Matching[0] != (Pair{0, 1}) {
		t.Fatalf("unexpected matching: %+v", report.Matching)
	}
}

func TestDuplicateConsumptionHallWitness(t *testing.T) {
	events := []Event{
		begin("alice", "n1", "login"),
		end("alice", "n1", "login"),
		end("alice", "n1", "login"),
	}
	report, err := Analyze(events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Feasible {
		t.Fatal("expected infeasible due to duplicated end consumption")
	}
	if !ValidWitness(events, report.Witness) {
		t.Fatalf("witness failed independent validation: %+v", report.Witness)
	}
	if len(report.Witness.Ends) != 2 || len(report.Witness.NeighborBegins) != 1 {
		t.Fatalf("unexpected witness shape: %+v", report.Witness)
	}

	enumeration, err := EnumerateMatchings(events, 0)
	if err != nil {
		t.Fatalf("enumeration error: %v", err)
	}
	if len(enumeration.Matchings) != 0 {
		t.Fatalf("expected no complete matching, got %d", len(enumeration.Matchings))
	}
}

func TestConcurrentSessionsAndPartialOpen(t *testing.T) {
	events := []Event{
		begin("alice", "n1", "login"),
		begin("alice", "n2", "login"), // 并发会话，不同随机数
		begin("bob", "n3", "login"),   // 永不结束的 begin
		end("alice", "n1", "login"),
		end("alice", "n2", "login"),
	}
	report, err := Analyze(events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !report.Feasible {
		t.Fatalf("expected feasible, witness=%+v", report.Witness)
	}
	if len(report.UnmatchedBegins) != 1 || report.UnmatchedBegins[0] != 2 {
		t.Fatalf("unexpected unmatched begins: %v", report.UnmatchedBegins)
	}
}

func TestOrderingRequiresEarlierBegin(t *testing.T) {
	events := []Event{
		end("alice", "n1", "login"), // end 先出现，没有更早的 begin
		begin("alice", "n1", "login"),
	}
	report, err := Analyze(events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Feasible || !ValidWitness(events, report.Witness) {
		t.Fatalf("expected infeasible with valid witness, got %+v", report)
	}
}

func TestFieldMismatch(t *testing.T) {
	events := []Event{
		begin("alice", "n1", "login"),
		end("alice", "n1", "logout"), // 操作摘要不同
	}
	report, err := Analyze(events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Feasible || !ValidWitness(events, report.Witness) {
		t.Fatalf("expected infeasible with valid witness, got %+v", report)
	}
}

func TestEnumerationCounts(t *testing.T) {
	// 两个完全可互换的 begin，两个 end，存在 2! = 2 个完整匹配。
	events := []Event{
		begin("alice", "n1", "login"),
		begin("alice", "n1", "login"),
		end("alice", "n1", "login"),
		end("alice", "n1", "login"),
	}
	enumeration, err := EnumerateMatchings(events, 0)
	if err != nil {
		t.Fatalf("enumeration error: %v", err)
	}
	if len(enumeration.Matchings) != 2 || enumeration.Truncated {
		t.Fatalf("expected 2 matchings, got %d (truncated=%v)",
			len(enumeration.Matchings), enumeration.Truncated)
	}
	seen := map[Pair]bool{}
	for _, matching := range enumeration.Matchings {
		if !ValidMatching(events, matching) || !IsComplete(events, matching) {
			t.Fatalf("invalid matching enumerated: %+v", matching)
		}
		for _, pair := range matching {
			seen[pair] = true
		}
	}
	if len(seen) != 4 {
		t.Fatalf("expected 4 distinct pairs across matchings, got %d", len(seen))
	}
}

func TestEnumerationLimit(t *testing.T) {
	events := make([]Event, 0, 12)
	for range 6 {
		events = append(events, begin("a", "n", "op"))
	}
	for range 6 {
		events = append(events, end("a", "n", "op"))
	}
	enumeration, err := EnumerateMatchings(events, 7)
	if err != nil {
		t.Fatalf("enumeration error: %v", err)
	}
	if !enumeration.Truncated || len(enumeration.Matchings) != 7 {
		t.Fatalf("expected truncation at 7, got %d (truncated=%v)",
			len(enumeration.Matchings), enumeration.Truncated)
	}
}

func TestEmptyAndNoEnds(t *testing.T) {
	report, err := Analyze(nil)
	if err != nil || !report.Feasible || !IsComplete(nil, report.Matching) {
		t.Fatalf("empty stream should be feasible: %+v err=%v", report, err)
	}

	onlyBegins := []Event{begin("a", "n", "op"), begin("b", "n", "op")}
	enumeration, err := EnumerateMatchings(onlyBegins, 0)
	if err != nil || len(enumeration.Matchings) != 1 || len(enumeration.Matchings[0]) != 0 {
		t.Fatalf("expected single empty matching, got %+v err=%v", enumeration, err)
	}
}

func TestInvalidEvent(t *testing.T) {
	events := []Event{
		{Kind: KindUnknown, Subject: "a", SessionNonce: "n", Operation: "op"},
		end("a", "n", "op"),
	}
	if _, err := Analyze(events); err != ErrInvalidEvent {
		t.Fatalf("expected ErrInvalidEvent, got %v", err)
	}
	if _, err := EnumerateMatchings(events, 0); err != ErrInvalidEvent {
		t.Fatalf("expected ErrInvalidEvent, got %v", err)
	}
	if _, err := CrossCheck(events, 0); err != ErrInvalidEvent {
		t.Fatalf("expected ErrInvalidEvent, got %v", err)
	}
}

func TestValidMatchingRejectsReuse(t *testing.T) {
	events := []Event{
		begin("a", "n", "op"),
		end("a", "n", "op"),
		end("a", "n", "op"),
	}
	illegal := []Pair{{Begin: 0, End: 1}, {Begin: 0, End: 2}}
	if ValidMatching(events, illegal) {
		t.Fatal("same begin consumed twice must be rejected")
	}

	backwards := []Pair{{Begin: 1, End: 0}}
	events2 := []Event{
		end("a", "n", "op"),
		begin("a", "n", "op"),
	}
	if ValidMatching(events2, backwards) {
		t.Fatal("end before begin must be rejected")
	}
}

func TestCrossCheckRandomized(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))
	subjects := []string{"alice", "bob"}
	nonces := []string{"n1", "n2", "n3"}
	ops := []string{"login", "logout"}

	for iter := 0; iter < 300; iter++ {
		n := rng.Intn(10)
		events := make([]Event, n)
		for i := range events {
			events[i] = Event{
				Kind:         Kind(rng.Intn(2) + 1),
				Subject:      subjects[rng.Intn(len(subjects))],
				SessionNonce: nonces[rng.Intn(len(nonces))],
				Operation:    ops[rng.Intn(len(ops))],
			}
		}

		report, err := CrossCheck(events, 1000)
		if err != nil {
			t.Fatalf("iter %d: unexpected error %v", iter, err)
		}
		if !report.Consistent {
			t.Fatalf("iter %d: inconsistent report %+v for events %+v",
				iter, report, events)
		}

		// 与一份完全独立的朴素回溯实现核对可行性。
		bruteFeasible := bruteForceFeasible(events)
		if bruteFeasible != report.Feasible {
			t.Fatalf("iter %d: brute force=%v want=%v events=%+v",
				iter, bruteFeasible, report.Feasible, events)
		}

		if !report.Truncated {
			enumeration, err := EnumerateMatchings(events, 1000)
			if err != nil {
				t.Fatalf("iter %d: enumeration error %v", iter, err)
			}
			wantCount := bruteForceCount(events)
			if len(enumeration.Matchings) != wantCount {
				t.Fatalf("iter %d: enumerated %d matchings, brute force says %d",
					iter, len(enumeration.Matchings), wantCount)
			}
		}
	}
}

// bruteForceFeasible 是独立于生产代码的朴素回溯可行性判定。
func bruteForceFeasible(events []Event) bool {
	endIdx := make([]int, 0)
	for i, ev := range events {
		if ev.Kind == KindEnd {
			endIdx = append(endIdx, i)
		}
	}
	used := make(map[int]bool)

	var dfs func(k int) bool
	dfs = func(k int) bool {
		if k == len(endIdx) {
			return true
		}
		e := events[endIdx[k]]
		for bi, b := range events {
			if b.Kind != KindBegin || used[bi] || bi >= endIdx[k] {
				continue
			}
			if compatible(b, e) {
				used[bi] = true
				if dfs(k + 1) {
					return true
				}
				delete(used, bi)
			}
		}
		return false
	}
	return dfs(0)
}

// bruteForceCount 独立统计完整匹配总数。
func bruteForceCount(events []Event) int {
	endIdx := make([]int, 0)
	for i, ev := range events {
		if ev.Kind == KindEnd {
			endIdx = append(endIdx, i)
		}
	}
	used := make(map[int]bool)
	count := 0

	var dfs func(k int)
	dfs = func(k int) {
		if k == len(endIdx) {
			count++
			return
		}
		e := events[endIdx[k]]
		for bi, b := range events {
			if b.Kind != KindBegin || used[bi] || bi >= endIdx[k] {
				continue
			}
			if compatible(b, e) {
				used[bi] = true
				dfs(k + 1)
				delete(used, bi)
			}
		}
	}
	dfs(0)
	return count
}
