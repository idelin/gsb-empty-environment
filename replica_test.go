package main

import "testing"

func TestScenarios(t *testing.T) {
	for _, sc := range scenarios() {
		r := &Replica{log: append([]Entry(nil), sc.prelog...), commitIndex: sc.commit}
		NewSimulator(r).Deliver(sc.schedule)
		if !prefixEqual(r.log, sc.expected) {
			t.Errorf("%s: final log %s does not start with expected %s",
				sc.name, formatLog(r.log), formatLog(sc.expected))
		}
	}
}

func TestDuplicateRetryIsIdempotent(t *testing.T) {
	r := &Replica{}
	req := AppendRequest{LeaderTerm: 1, Entries: entries(1, 1, 2, 3)}
	for i := 0; i < 3; i++ {
		if !r.HandleAppend(req) {
			t.Fatalf("attempt %d rejected", i)
		}
	}
	if len(r.log) != 3 {
		t.Fatalf("duplicate retries grew log to %d entries", len(r.log))
	}
}

func TestStaleRequestRejected(t *testing.T) {
	r := &Replica{currentTerm: 5}
	if r.HandleAppend(AppendRequest{LeaderTerm: 3, Entries: entries(3, 1)}) {
		t.Fatal("stale-term request accepted")
	}
	if len(r.log) != 0 {
		t.Fatal("stale request mutated the log")
	}
}

func TestCommittedEntriesSurviveConflict(t *testing.T) {
	r := &Replica{log: concat(entries(1, 1, 2), entries(2, 3)), commitIndex: 2}
	ok := r.HandleAppend(AppendRequest{
		LeaderTerm: 3, PrevLogIndex: 2, PrevLogTerm: 9, Entries: entries(3, 3)})
	if ok {
		t.Fatal("conflict reaching committed prefix was accepted")
	}
	if len(r.log) != 3 {
		t.Fatalf("committed prefix truncated, log=%s", formatLog(r.log))
	}
}

func TestFastRollbackSkipsWholeTerm(t *testing.T) {
	r := &Replica{log: concat(entries(1, 1), entries(4, 2, 3, 4, 5))}
	r.HandleAppend(AppendRequest{
		LeaderTerm: 6, PrevLogIndex: 5, PrevLogTerm: 2, Entries: entries(2, 6)})
	if len(r.log) != 1 {
		t.Fatalf("expected rollback to index 1, got log=%s", formatLog(r.log))
	}
}
