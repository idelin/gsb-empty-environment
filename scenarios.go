package main

import "fmt"

func entries(term int, idx ...int) []Entry {
	out := make([]Entry, len(idx))
	for i, id := range idx {
		out[i] = Entry{Term: term, Index: id}
	}
	return out
}

func concat(parts ...[]Entry) []Entry {
	var out []Entry
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

type scenario struct {
	name     string
	prelog   []Entry // pre-loaded replica log
	commit   int
	schedule []Delivery
	expected []Entry
}

func scenarios() []scenario {
	// 1. Empty log: the leader's requests arrive out of order; the one
	// depending on a missing predecessor is rejected until caught up.
	s1 := scenario{
		name: "empty log, out-of-order catch-up",
		schedule: []Delivery{
			{Req: AppendRequest{LeaderTerm: 2, PrevLogIndex: 2, PrevLogTerm: 1,
				Entries: entries(2, 3, 4), CommitIndex: 2}},
			{Req: AppendRequest{LeaderTerm: 2, PrevLogIndex: 0, PrevLogTerm: 0,
				Entries: entries(1, 1, 2), CommitIndex: 0}},
			{Req: AppendRequest{LeaderTerm: 2, PrevLogIndex: 2, PrevLogTerm: 1,
				Entries: entries(2, 3, 4), CommitIndex: 2}},
		},
		expected: concat(entries(1, 1, 2), entries(2, 3, 4)),
	}

	// 2. Divergent suffix lies entirely in one term: a single conflict
	// check rolls the whole segment back at once.
	s2 := scenario{
		name:   "whole divergent segment in one term, fast rollback",
		prelog: concat(entries(1, 1, 2), entries(3, 3, 4, 5)),
		schedule: []Delivery{
			{Req: AppendRequest{LeaderTerm: 4, PrevLogIndex: 5, PrevLogTerm: 2,
				Entries: entries(2, 6), CommitIndex: 2}},
			{Req: AppendRequest{LeaderTerm: 4, PrevLogIndex: 2, PrevLogTerm: 1,
				Entries: entries(2, 3, 4, 5, 6), CommitIndex: 2}},
		},
		expected: concat(entries(1, 1, 2), entries(2, 3, 4, 5, 6)),
	}

	// 3. Out-of-order delivery with duplicated retries and one stale
	// request from an old leader arriving late.
	s3 := scenario{
		name: "shuffled requests, duplicate retries, late stale request",
		schedule: []Delivery{
			{Req: AppendRequest{LeaderTerm: 3, PrevLogIndex: 2, PrevLogTerm: 1,
				Entries: entries(2, 3, 4), CommitIndex: 2}},
			{Req: AppendRequest{LeaderTerm: 3, PrevLogIndex: 0, PrevLogTerm: 0,
				Entries: entries(1, 1, 2), CommitIndex: 1}, Repeats: 1},
			{Req: AppendRequest{LeaderTerm: 3, PrevLogIndex: 4, PrevLogTerm: 2,
				Entries: entries(3, 5), CommitIndex: 4}},
			{Req: AppendRequest{LeaderTerm: 3, PrevLogIndex: 2, PrevLogTerm: 1,
				Entries: entries(2, 3, 4), CommitIndex: 2}, Repeats: 1},
			{Req: AppendRequest{LeaderTerm: 1, PrevLogIndex: 0, PrevLogTerm: 0,
				Entries: entries(1, 1), CommitIndex: 1}}, // late, stale term
			{Req: AppendRequest{LeaderTerm: 3, PrevLogIndex: 4, PrevLogTerm: 2,
				Entries: entries(3, 5), CommitIndex: 4}, Repeats: 1},
		},
		expected: concat(entries(1, 1, 2), entries(2, 3, 4), entries(3, 5)),
	}

	// 4. A conflicting request that would cut into the committed prefix
	// must be rejected, leaving the log untouched.
	s4 := scenario{
		name:   "committed entries are never truncated",
		prelog: concat(entries(1, 1, 2), entries(2, 3, 4)),
		commit: 3,
		schedule: []Delivery{
			{Req: AppendRequest{LeaderTerm: 5, PrevLogIndex: 3, PrevLogTerm: 9,
				Entries: entries(5, 4), CommitIndex: 0}},
			{Req: AppendRequest{LeaderTerm: 5, PrevLogIndex: 1, PrevLogTerm: 1,
				Entries: entries(5, 2, 3), CommitIndex: 0}},
		},
		expected: concat(entries(1, 1, 2), entries(2, 3, 4)),
	}

	return []scenario{s1, s2, s3, s4}
}

func runScenario(sc scenario) bool {
	r := &Replica{log: append([]Entry(nil), sc.prelog...), commitIndex: sc.commit}
	fmt.Printf("=== %s ===\n", sc.name)
	fmt.Printf("initial: log=%s commitIndex=%d\n", formatLog(r.log), r.commitIndex)

	NewSimulator(r).Deliver(sc.schedule)
	for _, ev := range r.events {
		fmt.Println("  " + ev)
	}

	ok := prefixEqual(r.log, sc.expected)
	status := "PASS"
	if !ok {
		status = "FAIL"
	}
	fmt.Printf("final:   log=%s commitIndex=%d\n", formatLog(r.log), r.commitIndex)
	fmt.Printf("expect prefix %s -> %s\n\n", formatLog(sc.expected), status)
	return ok
}
