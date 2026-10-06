package posetmatch

import (
	"fmt"
	"math/rand"
	"testing"
)

func buildPoset(t *testing.T, events []Event, edges [][2]EventID) *Poset {
	t.Helper()
	p := NewPoset()
	for _, ev := range events {
		if err := p.AddEvent(ev); err != nil {
			t.Fatalf("AddEvent(%s): %v", ev.ID, err)
		}
	}
	for _, e := range edges {
		if p.Reaches(e[1], e[0]) {
			t.Fatalf("test poset has a cycle via %s -> %s", e[0], e[1])
		}
		p.addEdge(e[0], e[1])
	}
	return p
}

func checkWitness(t *testing.T, p *Poset, m *Match) {
	t.Helper()
	if m == nil {
		t.Fatal("expected a match, got none")
	}
	if !p.IsLinearExtension(m.Witness) {
		t.Errorf("witness %v is not a linear extension", m.Witness)
	}
	if m.Start < 0 || m.End > len(m.Witness) || m.End-m.Start != len(m.Pattern) {
		t.Fatalf("bad segment [%d,%d) in witness of %d events", m.Start, m.End, len(m.Witness))
	}
	seen := make(map[EventID]bool)
	for i, lbl := range m.Pattern {
		id := m.Witness[m.Start+i]
		if got := p.Event(id).Label; got != lbl {
			t.Errorf("segment position %d: event %s has label %q, want %q", i, id, got, lbl)
		}
		if seen[id] {
			t.Errorf("event %s used twice in segment", id)
		}
		seen[id] = true
	}
}

func countExtensions(t *testing.T, p *Poset) int {
	t.Helper()
	n, complete := p.LinearExtensions(1<<30, func([]EventID) bool { return true })
	if !complete {
		t.Fatal("extension count did not complete")
	}
	return n
}

// Concurrent events may be reorderable against their timestamps: the matcher
// must find what the naive time sort misses, and must reject what the naive
// time sort hallucinates.
func TestConcurrentEventsAndTimeSort(t *testing.T) {
	// z<x in the poset, y concurrent with both; timestamps order z,x,y.
	p := buildPoset(t,
		[]Event{{ID: "z", Label: "z", Time: 5}, {ID: "x", Label: "x", Time: 10}, {ID: "y", Label: "y", Time: 20}},
		[][2]EventID{{"z", "x"}})
	pattern := []Label{"y", "z", "x"}
	out := p.FindPattern(pattern)
	if !out.Found {
		t.Fatal("pattern feasible via concurrent reordering was not found")
	}
	checkWitness(t, p, out.Match)
	if p.TimeOrderCheck(pattern) {
		t.Error("naive time-order check should have missed this feasible pattern")
	}

	// Timestamps say a before b, but the poset forces b before a.
	q := buildPoset(t,
		[]Event{{ID: "p", Label: "a", Time: 1}, {ID: "q", Label: "b", Time: 2}},
		[][2]EventID{{"q", "p"}})
	pat2 := []Label{"a", "b"}
	if !q.TimeOrderCheck(pat2) {
		t.Error("naive time-order check should 'find' the infeasible pattern")
	}
	out2 := q.FindPattern(pat2)
	if out2.Found {
		t.Error("pattern violating precedence must not be found")
	}
	if out2.Report == nil || !out2.Report.Complete || out2.Report.ExtensionsTotal != 1 {
		t.Errorf("bad exhaustion report: %+v", out2.Report)
	}
}

// Duplicate labels: the pattern must be realised by distinct events, and the
// answer must agree with the full-permutation filter.
func TestDuplicateLabels(t *testing.T) {
	p := buildPoset(t,
		[]Event{
			{ID: "e1", Label: "a", Time: 1}, {ID: "e2", Label: "a", Time: 2},
			{ID: "e3", Label: "b", Time: 3}, {ID: "e4", Label: "b", Time: 4},
		},
		[][2]EventID{{"e1", "e3"}, {"e2", "e4"}})

	out := p.FindPattern([]Label{"a", "b", "a"})
	if !out.Found {
		t.Fatal("a,b,a should be feasible (e.g. e1,e3,e2,e4)")
	}
	checkWitness(t, p, out.Match)
	if _, _, ok := p.PermutationFind([]Label{"a", "b", "a"}); !ok {
		t.Error("full-permutation filter disagrees: a,b,a should exist")
	}

	neg := []Label{"b", "b", "a"}
	out2 := p.FindPattern(neg)
	if out2.Found {
		t.Fatalf("b,b,a should be infeasible, witness %v", out2.Match.Witness)
	}
	if _, _, ok := p.PermutationFind(neg); ok {
		t.Error("full-permutation filter disagrees: b,b,a should not exist")
	}
	rep := out2.Report
	if rep == nil || !rep.Complete {
		t.Fatalf("search space must be closed for 4 events: %+v", rep)
	}
	if want := countExtensions(t, p); rep.ExtensionsTotal != want {
		t.Errorf("report counted %d extensions, independent count is %d", rep.ExtensionsTotal, want)
	}
	if len(rep.Sample) == 0 || rep.Digest == 0 {
		t.Error("report should carry sample orders and a digest")
	}
}

// A 12-event case: a 6-chain plus 6 concurrent events. The extension space
// is 12!/6! = 665280, large but enumerable, so the no-match report closes.
func TestTwelveEvents(t *testing.T) {
	var events []Event
	var edges [][2]EventID
	labels := []Label{"a", "b", "c", "d", "e", "f"}
	for i, lbl := range labels {
		id := EventID(fmt.Sprintf("c%d", i+1))
		events = append(events, Event{ID: id, Label: lbl, Time: int64(i)})
		if i > 0 {
			edges = append(edges, [2]EventID{EventID(fmt.Sprintf("c%d", i)), id})
		}
	}
	for i := 0; i < 6; i++ {
		events = append(events, Event{ID: EventID(fmt.Sprintf("f%d", i+1)), Label: "x", Time: int64(100 + i)})
	}
	p := buildPoset(t, events, edges)

	out := p.FindPattern(labels)
	if !out.Found {
		t.Fatal("the chain labels should appear contiguously")
	}
	checkWitness(t, p, out.Match)
	for i, id := range out.Match.Segment() {
		if want := EventID(fmt.Sprintf("c%d", i+1)); id != want {
			t.Errorf("segment[%d] = %s, want %s", i, id, want)
		}
	}
	if _, _, ok := p.BruteForceFind(labels); !ok {
		t.Error("brute-force verifier disagrees on the positive case")
	}

	neg := p.FindPattern([]Label{"f", "e"})
	if neg.Found {
		t.Fatal("f before e contradicts the chain")
	}
	if !neg.Report.Complete {
		t.Fatal("665280 extensions should enumerate within the cap")
	}
	if neg.Report.ExtensionsTotal != 665280 {
		t.Errorf("ExtensionsTotal = %d, want 665280", neg.Report.ExtensionsTotal)
	}
}

// Randomised cross-check: for small posets with duplicate labels and
// concurrent events, FindPattern must agree with the full-permutation
// filter, and no-match reports must account for every linear extension.
func TestRandomCrossCheck(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	alphabet := []Label{"a", "b", "c"}
	for iter := 0; iter < 200; iter++ {
		n := 2 + r.Intn(7) // 2..8 events
		p := NewPoset()
		ids := make([]EventID, n)
		for i := range ids {
			ids[i] = EventID(fmt.Sprintf("e%d", i))
			p.AddEvent(Event{ID: ids[i], Label: alphabet[r.Intn(len(alphabet))], Time: r.Int63n(1000)})
		}
		perm := r.Perm(n) // acyclic by construction: edges only forward in perm
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				if r.Float64() < 0.3 {
					p.addEdge(ids[perm[i]], ids[perm[j]])
				}
			}
		}
		m := 1 + r.Intn(4)
		pattern := make([]Label, m)
		for i := range pattern {
			pattern[i] = alphabet[r.Intn(len(alphabet))]
		}

		out := p.FindPattern(pattern)
		_, _, bruteOK := p.PermutationFind(pattern)
		if out.Found != bruteOK {
			t.Fatalf("iter %d: FindPattern=%v but permutation filter=%v (pattern %v)",
				iter, out.Found, bruteOK, pattern)
		}
		if out.Found {
			checkWitness(t, p, out.Match)
			continue
		}
		if !out.Report.Complete {
			t.Fatalf("iter %d: small poset must close its search space", iter)
		}
		if want := countExtensions(t, p); out.Report.ExtensionsTotal != want {
			t.Fatalf("iter %d: report has %d extensions, want %d", iter, out.Report.ExtensionsTotal, want)
		}
	}
}

func TestEdgeCases(t *testing.T) {
	p := buildPoset(t,
		[]Event{{ID: "u", Label: "a"}, {ID: "v", Label: "b"}, {ID: "w", Label: "c"}},
		[][2]EventID{{"u", "v"}, {"v", "w"}})

	empty := p.FindPattern(nil)
	if !empty.Found || empty.Match.Start != empty.Match.End {
		t.Errorf("empty pattern should trivially match at a point: %+v", empty.Match)
	}
	if !p.IsLinearExtension(empty.Match.Witness) {
		t.Error("empty-pattern witness should still be a linear extension")
	}

	tooLong := p.FindPattern([]Label{"a", "b", "c", "a"})
	if tooLong.Found || tooLong.Report == nil || !tooLong.Report.Complete {
		t.Errorf("pattern longer than the poset must fail with a closed report: %+v", tooLong)
	}

	unknown := p.FindPattern([]Label{"z"})
	if unknown.Found {
		t.Error("unknown label must not match")
	}

	one := p.FindPattern([]Label{"b"})
	if !one.Found || one.Match.End-one.Match.Start != 1 {
		t.Fatalf("single label should match a single event: %+v", one.Match)
	}
	checkWitness(t, p, one.Match)

	if _, agree := p.CrossCheck([]Label{"a", "c"}); !agree {
		t.Error("CrossCheck disagreement on a,c")
	}
	if _, agree := p.CrossCheck([]Label{"c", "a"}); !agree {
		t.Error("CrossCheck disagreement on c,a")
	}
}
