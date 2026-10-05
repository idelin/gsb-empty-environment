package sched

import (
	"fmt"
	"reflect"
	"testing"
)

func issue(emp, qual string, start, end int64) Event {
	return Event{Kind: Issue, Employee: emp, Qualification: qual, Start: start, End: end}
}

func revoke(emp, qual string, start, end int64) Event {
	return Event{Kind: Revoke, Employee: emp, Qualification: qual, Start: start, End: end}
}

func gapsOf(t *testing.T, res []ShiftResult, shiftID string) []Gap {
	t.Helper()
	for _, r := range res {
		if r.ShiftID == shiftID {
			return r.Gaps
		}
	}
	t.Fatalf("shift %s not in results", shiftID)
	return nil
}

// Cross-midnight shift: 22:00 -> 02:00 next day, expressed in plain
// minutes (1320..1560). A certificate spanning midnight covers it fully.
func TestCrossMidnightShift(t *testing.T) {
	store := NewStore(
		[]Position{{ID: "night", Required: []string{"license"}}},
		[]Shift{{ID: "s-night", Employee: "e1", Position: "night", Start: 1320, End: 1560}},
		[]Event{issue("e1", "license", 1200, 1600)},
	)
	res := store.Evaluate(2) // horizon 2 sees event seq 1
	if len(res) != 1 || !res[0].Schedulable {
		t.Fatalf("cross-midnight shift should be schedulable, got %+v", res)
	}
	// Certificate that ends exactly at midnight leaves a 02:00-02:00? no:
	// [1200,1440) leaves (1440,1560) uncovered.
	store2 := NewStore(
		[]Position{{ID: "night", Required: []string{"license"}}},
		[]Shift{{ID: "s-night", Employee: "e1", Position: "night", Start: 1320, End: 1560}},
		[]Event{issue("e1", "license", 1200, 1440)},
	)
	got := gapsOf(t, store2.Evaluate(2), "s-night")
	want := []Gap{{Qualification: "license", Start: 1440, End: 1560}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gaps = %+v, want %+v", got, want)
	}
}

// Two certificates chaining seamlessly: [0,100) + [100,200) covers [0,200)
// with no gap at the shared boundary.
func TestSeamlessCertificateChaining(t *testing.T) {
	store := NewStore(
		[]Position{{ID: "p", Required: []string{"q"}}},
		[]Shift{{ID: "s", Employee: "e1", Position: "p", Start: 50, End: 150}},
		[]Event{issue("e1", "q", 0, 100), issue("e1", "q", 100, 200)},
	)
	if res := store.Evaluate(3); !res[0].Schedulable {
		t.Fatalf("chained certificates must not leave a gap, got %+v", res[0])
	}
	// A one-minute break (100,101) is a real gap.
	store2 := NewStore(
		[]Position{{ID: "p", Required: []string{"q"}}},
		[]Shift{{ID: "s", Employee: "e1", Position: "p", Start: 50, End: 150}},
		[]Event{issue("e1", "q", 0, 100), issue("e1", "q", 101, 200)},
	)
	got := gapsOf(t, store2.Evaluate(3), "s")
	want := []Gap{{Qualification: "q", Start: 100, End: 101}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gaps = %+v, want %+v", got, want)
	}
}

// Overlapping revocations union together; coverage before/after survives.
// A revocation not yet recorded must not affect earlier queries.
func TestOverlappingRevocations(t *testing.T) {
	events := []Event{
		issue("e1", "q", 0, 300),    // seq 1
		revoke("e1", "q", 50, 120),  // seq 2
		revoke("e1", "q", 100, 200), // seq 3, overlaps the previous revoke
	}
	mk := func() *Store {
		return NewStore(
			[]Position{{ID: "p", Required: []string{"q"}}},
			[]Shift{{ID: "s", Employee: "e1", Position: "p", Start: 0, End: 300}},
			events,
		)
	}
	// Horizon 2: only the issue is known.
	if res := mk().Evaluate(2); !res[0].Schedulable {
		t.Fatalf("before revocations are known, shift must be schedulable: %+v", res[0])
	}
	// Horizon 3: first revoke known.
	got := gapsOf(t, mk().Evaluate(3), "s")
	want := []Gap{{Qualification: "q", Start: 50, End: 120}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gaps = %+v, want %+v", got, want)
	}
	// Horizon 4: overlapping revokes merge into [50,200).
	got = gapsOf(t, mk().Evaluate(4), "s")
	want = []Gap{{Qualification: "q", Start: 50, End: 200}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gaps = %+v, want %+v", got, want)
	}
	// Coverage itself: [0,50) and [200,300) remain.
	cov := mk().Coverage("e1", "q", 4)
	wantCov := []Interval{{0, 50}, {200, 300}}
	if !reflect.DeepEqual(cov, wantCov) {
		t.Fatalf("coverage = %+v, want %+v", cov, wantCov)
	}
}

// Retroactive supplement: a certificate recorded late but valid in the past
// fixes new queries while old queries (smaller horizon) stay unchanged.
func TestRetroactiveSupplementKeepsOldQueries(t *testing.T) {
	positions := []Position{{ID: "p", Required: []string{"q"}}}
	shifts := []Shift{{ID: "s", Employee: "e1", Position: "p", Start: 0, End: 100}}
	store := NewStore(positions, shifts, []Event{issue("e1", "q", 0, 50)}) // seq 1

	before := gapsOf(t, store.Evaluate(2), "s")
	wantGap := []Gap{{Qualification: "q", Start: 50, End: 100}}
	if !reflect.DeepEqual(before, wantGap) {
		t.Fatalf("gaps before supplement = %+v, want %+v", before, wantGap)
	}

	// Late-recorded certificate, business interval entirely in the past.
	store.AddEvent(issue("e1", "q", 50, 100)) // seq 2

	// Old query at horizon 2 is unchanged.
	after := gapsOf(t, store.Evaluate(2), "s")
	if !reflect.DeepEqual(after, wantGap) {
		t.Fatalf("old query changed after retroactive supplement: %+v, want %+v", after, wantGap)
	}
	// New query at horizon 3 sees the supplement.
	if res := store.Evaluate(3); !res[0].Schedulable {
		t.Fatalf("new query should see retroactive supplement: %+v", res[0])
	}
	if got := store.AffectedShifts(3); len(got) != 0 {
		t.Fatalf("affected shifts at horizon 3 = %v, want none", got)
	}
	if got := store.AffectedShifts(2); !reflect.DeepEqual(got, []string{"s"}) {
		t.Fatalf("affected shifts at horizon 2 = %v, want [s]", got)
	}
}

// One employee holding multiple positions: each shift is checked against
// its own position's required qualifications, independently.
func TestEmployeeWithMultiplePositions(t *testing.T) {
	store := NewStore(
		[]Position{
			{ID: "driver", Required: []string{"drive"}},
			{ID: "medic", Required: []string{"firstaid", "cpr"}},
		},
		[]Shift{
			{ID: "s-drive", Employee: "e1", Position: "driver", Start: 0, End: 100},
			{ID: "s-medic", Employee: "e1", Position: "medic", Start: 200, End: 300},
		},
		[]Event{
			issue("e1", "drive", 0, 500),
			issue("e1", "firstaid", 0, 500),
			// no cpr certificate
		},
	)
	res := store.Evaluate(3)
	if !res[0].Schedulable {
		t.Fatalf("driver shift should be schedulable: %+v", res[0])
	}
	if res[1].Schedulable {
		t.Fatalf("medic shift should miss cpr: %+v", res[1])
	}
	got := gapsOf(t, res, "s-medic")
	want := []Gap{{Qualification: "cpr", Start: 200, End: 300}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gaps = %+v, want %+v", got, want)
	}
	if got := store.AffectedShifts(3); !reflect.DeepEqual(got, []string{"s-medic"}) {
		t.Fatalf("affected = %v, want [s-medic]", got)
	}
}

// The interval index must agree with the full-scan reference, both for
// candidate selection and for recheck results, across many events.
func TestIndexMatchesFullScan(t *testing.T) {
	var shifts []Shift
	for i := 0; i < 40; i++ {
		emp := fmt.Sprintf("e%d", i%4)
		shifts = append(shifts, Shift{
			ID:       fmt.Sprintf("s%d", i),
			Employee: emp,
			Position: "p" + fmt.Sprint(i%3),
			Start:    int64(i * 37),
			End:      int64(i*37 + 90),
		})
	}
	positions := []Position{
		{ID: "p0", Required: []string{"q0"}},
		{ID: "p1", Required: []string{"q1"}},
		{ID: "p2", Required: []string{"q0", "q1"}},
	}
	store := NewStore(positions, shifts, nil)
	var events []Event
	for i := 0; i < 60; i++ {
		kind := Issue
		if i%3 == 2 {
			kind = Revoke
		}
		events = append(events, Event{
			Kind:          kind,
			Employee:      fmt.Sprintf("e%d", i%4),
			Qualification: fmt.Sprintf("q%d", i%2),
			Start:         int64((i * 53) % 1400),
			End:           int64((i*53)%1400 + 120),
		})
	}
	for _, ev := range events {
		store.AddEvent(ev)
		// Candidate sets must match at every step.
		got := store.CandidateShifts(ev.Employee, ev.Start, ev.End)
		want := store.CandidateShiftsFullScan(ev.Employee, ev.Start, ev.End)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("candidates for %+v: index=%v scan=%v", ev, got, want)
		}
	}
	// Recheck results must match too, at several horizons.
	for _, horizon := range []int{10, 30, 61} {
		for _, ev := range events {
			got := store.Recheck(ev, horizon)
			want := store.RecheckFullScan(ev, horizon)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("recheck horizon %d event %+v:\nindex=%+v\nscan =%+v", horizon, ev, got, want)
			}
		}
	}
}

// Interval arithmetic primitives.
func TestIntervalOps(t *testing.T) {
	if got := MergeUnion([]Interval{{5, 8}, {0, 5}, {2, 3}, {10, 12}, {11, 20}}); !reflect.DeepEqual(got, []Interval{{0, 8}, {10, 20}}) {
		t.Fatalf("MergeUnion = %v", got)
	}
	if got := Subtract([]Interval{{0, 100}}, []Interval{{10, 20}, {20, 30}, {90, 200}}); !reflect.DeepEqual(got, []Interval{{0, 10}, {30, 90}}) {
		t.Fatalf("Subtract = %v", got)
	}
	if got := Gaps(50, 150, []Interval{{0, 60}, {60, 140}, {145, 200}}); !reflect.DeepEqual(got, []Interval{{140, 145}}) {
		t.Fatalf("Gaps = %v", got)
	}
	if got := Gaps(0, 100, []Interval{{0, 100}}); len(got) != 0 {
		t.Fatalf("Gaps full cover = %v", got)
	}
}
