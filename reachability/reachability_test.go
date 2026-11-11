package reachability

import (
	"fmt"
	"math/rand"
	"testing"
)

func mustModel(t *testing.T, entries []Entry, stmts []Statement, rules []TagRule) *Model {
	t.Helper()
	m, err := NewModel(entries, stmts, rules)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	return m
}

// A class is public only when one access point satisfies all three
// layers on its own; permissions of different access points must never
// be combined.
func TestNoCrossAccessPointCombination(t *testing.T) {
	m := mustModel(t,
		[]Entry{
			{ID: "ap-east", Open: true},
			{ID: "ap-west", Open: true},
		},
		[]Statement{
			// ap-east's policy allows "media" but its tag rule does not match.
			{ID: "st-east", Entry: "ap-east", Allows: true, Grants: []ClassID{"media"}},
			// ap-west's tag rule matches, but its policy does not grant "media".
			{ID: "st-west", Entry: "ap-west", Allows: true, Grants: []ClassID{"logs"}},
		},
		[]TagRule{
			{ID: "tag-east", Stmt: "st-east", Class: "media", Match: false},
			{ID: "tag-west", Stmt: "st-west", Class: "logs", Match: true},
		},
	)
	got := FullRecompute(m)
	if _, ok := got["media"]; ok {
		t.Fatalf("media must not be public: no single access point completes the path, got %v", got["media"])
	}
	p, ok := got["logs"]
	if !ok {
		t.Fatalf("logs must be public via ap-west, got %v", got)
	}
	if p.Entry != "ap-west" || p.Stmt != "st-west" || p.Rule != "tag-west" || p.Hops != 4 {
		t.Fatalf("unexpected proof for logs: %s", p)
	}
}

// When a statement has no tag rule for a class the tag layer is
// vacuously open, yielding a shorter (3-hop) proof that wins over any
// tag-gated (4-hop) alternative.
func TestShortestProofPrefersDirectPath(t *testing.T) {
	m := mustModel(t,
		[]Entry{{ID: "ap-1", Open: true}, {ID: "ap-2", Open: true}},
		[]Statement{
			{ID: "st-direct", Entry: "ap-2", Allows: true, Grants: []ClassID{"media"}},
			{ID: "st-gated", Entry: "ap-1", Allows: true, Grants: []ClassID{"media"}},
		},
		[]TagRule{{ID: "tag-1", Stmt: "st-gated", Class: "media", Match: true}},
	)
	p, ok := FullRecompute(m)["media"]
	if !ok {
		t.Fatal("media must be public")
	}
	if p.Hops != 3 || p.Stmt != "st-direct" || p.Rule != "" {
		t.Fatalf("expected the 3-hop direct proof, got %s", p)
	}
}

// A tag-gated class flips with its tag rule, and closing the entry cuts
// every path through it even when policy and tags still hold.
func TestEntryCloseAndTagChange(t *testing.T) {
	m := mustModel(t,
		[]Entry{{ID: "ap-1", Open: true}},
		[]Statement{{ID: "st-1", Entry: "ap-1", Allows: true, Grants: []ClassID{"media"}}},
		[]TagRule{{ID: "tag-1", Stmt: "st-1", Class: "media", Match: true}},
	)
	eng := NewEngine(m)
	if _, ok := eng.Proof("media"); !ok {
		t.Fatal("media must start public")
	}

	if err := eng.Apply(Event{Kind: EventTagUnmatched, Rule: "tag-1"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := eng.Proof("media"); ok {
		t.Fatal("media must vanish when its only tag rule stops matching")
	}

	if err := eng.Apply(Event{Kind: EventTagMatched, Rule: "tag-1"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := eng.Proof("media"); !ok {
		t.Fatal("media must return when the tag rule matches again")
	}

	if err := eng.Apply(Event{Kind: EventEntryClosed, Entry: "ap-1"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := eng.Proof("media"); ok {
		t.Fatal("media must vanish when the entry closes")
	}

	if err := eng.Apply(Event{Kind: EventEntryOpened, Entry: "ap-1"}); err != nil {
		t.Fatal(err)
	}
	p, ok := eng.Proof("media")
	if !ok || p.Hops != 4 {
		t.Fatalf("media must be republished with its 4-hop proof, got ok=%v p=%s", ok, p)
	}
	if err := eng.Verify(); err != nil {
		t.Fatal(err)
	}
}

// Denied statements contribute no path even when entry and tags hold.
func TestStatementDeny(t *testing.T) {
	m := mustModel(t,
		[]Entry{{ID: "ap-1", Open: true}},
		[]Statement{{ID: "st-1", Entry: "ap-1", Allows: false, Grants: []ClassID{"media"}}},
		nil,
	)
	if got := FullRecompute(m); len(got) != 0 {
		t.Fatalf("denied statement must publish nothing, got %v", got)
	}
	if err := m.Apply(Event{Kind: EventStatementAllowed, Stmt: "st-1"}); err != nil {
		t.Fatal(err)
	}
	p, ok := FullRecompute(m)["media"]
	if !ok || p.Hops != 3 {
		t.Fatalf("allowing the statement must publish media with a 3-hop proof, got %v", FullRecompute(m))
	}
}

// Randomized differential test: after every batch of entry open/close,
// statement allow/deny and tag match/unmatch events, the incrementally
// maintained public set and shortest proofs must equal a full recompute.
func TestIncrementalMatchesFullRecompute(t *testing.T) {
	rng := rand.New(rand.NewSource(20261111))
	for trial := 0; trial < 200; trial++ {
		m := randomModel(rng)
		eng := NewEngine(m)
		if err := eng.Verify(); err != nil {
			t.Fatalf("trial %d: initial state: %v", trial, err)
		}
		for round := 0; round < 30; round++ {
			batch := randomEvents(rng, m, 1+rng.Intn(4))
			if err := eng.Apply(batch...); err != nil {
				t.Fatalf("trial %d round %d: apply %v: %v", trial, round, batch, err)
			}
			if err := eng.Verify(); err != nil {
				t.Fatalf("trial %d round %d: after %v: %v", trial, round, batch, err)
			}
		}
	}
}

// CheckEvents must agree with applying the events to a plain clone.
func TestCheckEvents(t *testing.T) {
	m := mustModel(t,
		[]Entry{{ID: "ap-1", Open: true}, {ID: "ap-2", Open: false}},
		[]Statement{
			{ID: "st-1", Entry: "ap-1", Allows: true, Grants: []ClassID{"media", "logs"}},
			{ID: "st-2", Entry: "ap-2", Allows: true, Grants: []ClassID{"media"}},
		},
		[]TagRule{{ID: "tag-1", Stmt: "st-1", Class: "media", Match: false}},
	)
	events := []Event{
		{Kind: EventEntryClosed, Entry: "ap-1"},
		{Kind: EventEntryOpened, Entry: "ap-2"},
		{Kind: EventTagMatched, Rule: "tag-1"},
	}
	got, err := CheckEvents(m, events...)
	if err != nil {
		t.Fatal(err)
	}
	// ap-1 is closed, so only ap-2's direct grant of media survives.
	if len(got) != 1 {
		t.Fatalf("expected exactly one public class, got %v", got)
	}
	p := got["media"]
	if p.Entry != "ap-2" || p.Hops != 3 {
		t.Fatalf("expected the 3-hop proof via ap-2, got %s", p)
	}
	// The original model must be untouched by CheckEvents.
	if _, ok := FullRecompute(m)["logs"]; !ok {
		t.Fatal("CheckEvents mutated the source model")
	}
}

func randomModel(rng *rand.Rand) *Model {
	nEntries := 1 + rng.Intn(3)
	nStmts := 1 + rng.Intn(5)
	nClasses := 1 + rng.Intn(4)

	entries := make([]Entry, nEntries)
	for i := range entries {
		entries[i] = Entry{ID: EntryID(fmt.Sprintf("ap-%d", i)), Open: rng.Intn(2) == 0}
	}
	classes := make([]ClassID, nClasses)
	for i := range classes {
		classes[i] = ClassID(fmt.Sprintf("cls-%d", i))
	}
	stmts := make([]Statement, nStmts)
	for i := range stmts {
		grants := make([]ClassID, 0, nClasses)
		for _, c := range classes {
			if rng.Intn(2) == 0 {
				grants = append(grants, c)
			}
		}
		if len(grants) == 0 {
			grants = append(grants, classes[rng.Intn(len(classes))])
		}
		stmts[i] = Statement{
			ID:     StmtID(fmt.Sprintf("st-%d", i)),
			Entry:  entries[rng.Intn(len(entries))].ID,
			Allows: rng.Intn(2) == 0,
			Grants: grants,
		}
	}
	var rules []TagRule
	for i, s := range stmts {
		for _, c := range s.Grants {
			if rng.Intn(3) == 0 {
				rules = append(rules, TagRule{
					ID:    RuleID(fmt.Sprintf("tag-%d-%s", i, c)),
					Stmt:  s.ID,
					Class: c,
					Match: rng.Intn(2) == 0,
				})
			}
		}
	}
	m, err := NewModel(entries, stmts, rules)
	if err != nil {
		panic(err)
	}
	return m
}

func randomEvents(rng *rand.Rand, m *Model, n int) []Event {
	entries := sortedEntries(m)
	stmts := make([]StmtID, 0, len(m.stmts))
	for id := range m.stmts {
		stmts = append(stmts, id)
	}
	rules := make([]RuleID, 0, len(m.rules))
	for id := range m.rules {
		rules = append(rules, id)
	}
	events := make([]Event, 0, n)
	for i := 0; i < n; i++ {
		switch rng.Intn(6) {
		case 0:
			events = append(events, Event{Kind: EventEntryOpened, Entry: entries[rng.Intn(len(entries))]})
		case 1:
			events = append(events, Event{Kind: EventEntryClosed, Entry: entries[rng.Intn(len(entries))]})
		case 2:
			events = append(events, Event{Kind: EventStatementAllowed, Stmt: stmts[rng.Intn(len(stmts))]})
		case 3:
			events = append(events, Event{Kind: EventStatementDenied, Stmt: stmts[rng.Intn(len(stmts))]})
		case 4, 5:
			if len(rules) == 0 {
				continue
			}
			kind := EventTagMatched
			if rng.Intn(2) == 0 {
				kind = EventTagUnmatched
			}
			events = append(events, Event{Kind: kind, Rule: rules[rng.Intn(len(rules))]})
		}
	}
	return events
}
