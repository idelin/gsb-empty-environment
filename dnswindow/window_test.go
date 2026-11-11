package dnswindow

import (
	"errors"
	"math"
	"testing"
	"time"
)

func at(sec int64) time.Time { return time.Unix(sec, 0).UTC() }

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-12
}

func findGroup(t *testing.T, result WindowResult, client, registered string) GroupFeatures {
	t.Helper()
	for _, group := range result.Groups {
		if group.Client == client && group.RegisteredDomain == registered {
			return group
		}
	}
	t.Fatalf("group (%q, %q) not found in result", client, registered)
	return GroupFeatures{}
}

func TestBasicAggregationAndDedup(t *testing.T) {
	manager, err := NewManager(time.Minute)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Window [00:00, 01:00), group (c1, example.com).
	events := []Event{
		{ID: "1", Client: "c1", RegisteredDomain: "example.com", QueriedName: "abc.example.com", NXDOMAIN: true, Time: at(5)},
		{ID: "2", Client: "c1", RegisteredDomain: "example.com", QueriedName: "xy.example.com", NXDOMAIN: false, Time: at(10)},
		{ID: "3", Client: "c1", RegisteredDomain: "example.com", QueriedName: "abc.example.com.", NXDOMAIN: true, Time: at(15)},
		{ID: "4", Client: "c2", RegisteredDomain: "other.org", QueriedName: "z.other.org", NXDOMAIN: false, Time: at(20)},
	}
	for _, event := range events {
		if outcome, err := manager.Submit(event); err != nil || outcome != OutcomeAccepted {
			t.Fatalf("Submit %s: outcome=%v err=%v", event.ID, outcome, err)
		}
	}

	// Exact duplicate event ID must not be counted again.
	outcome, err := manager.Submit(events[0])
	if err != nil || outcome != OutcomeDuplicate {
		t.Fatalf("duplicate submit: outcome=%v err=%v", outcome, err)
	}

	results := manager.Advance(at(60))
	if len(results) != 1 {
		t.Fatalf("expected 1 sealed window, got %d", len(results))
	}
	result := results[0]
	if !result.Start.Equal(at(0)) || !result.End.Equal(at(60)) {
		t.Fatalf("window bounds = [%v, %v)", result.Start, result.End)
	}
	if len(result.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(result.Groups))
	}

	group := findGroup(t, result, "c1", "example.com")
	if group.TotalEvents != 3 || group.NXDOMAINEvents != 2 {
		t.Fatalf("event counts = %d/%d, want 3/2", group.TotalEvents, group.NXDOMAINEvents)
	}
	if !approxEqual(group.NXDOMAINRatio, 2.0/3.0) {
		t.Fatalf("NXDOMAIN ratio = %v, want %v", group.NXDOMAINRatio, 2.0/3.0)
	}

	// Labels: abc(3) x2, xy(2) x1, example(7) x3, com(3) x3.
	wantHist := map[int]int64{3: 5, 7: 3, 2: 1}
	if len(group.LabelLengthHist) != len(wantHist) {
		t.Fatalf("hist len = %d, want %d (%v)", len(group.LabelLengthHist), len(wantHist), group.LabelLengthHist)
	}
	for length, want := range wantHist {
		if group.LabelLengthHist[length] != want {
			t.Fatalf("hist[%d] = %d, want %d; full hist %v", length, group.LabelLengthHist[length], want, group.LabelLengthHist)
		}
	}
	// Distinct labels: abc, example, com, xy.
	if group.DistinctLabels != 4 {
		t.Fatalf("distinct labels = %d, want 4", group.DistinctLabels)
	}

	// Byte entropy independently recomputed over the three deduplicated names,
	// treating "abc.example.com." and "abc.example.com" identically.
	var freq [256]int64
	var total int64
	for _, name := range []string{"abc.example.com", "xy.example.com", "abc.example.com"} {
		total += int64(len(name))
		for i := 0; i < len(name); i++ {
			freq[name[i]]++
		}
	}
	if group.TotalNameBytes != total {
		t.Fatalf("name bytes = %d, want %d", group.TotalNameBytes, total)
	}
	if group.ByteFrequency != freq {
		t.Fatalf("byte frequency mismatch:\n got %v\nwant %v", group.ByteFrequency, freq)
	}
	wantEntropy := shannonEntropy(freq, total)
	if !approxEqual(group.ByteEntropy, wantEntropy) {
		t.Fatalf("entropy = %.12f, want %.12f", group.ByteEntropy, wantEntropy)
	}
}

func TestOutOfOrderAdmissionAndWindowAlignment(t *testing.T) {
	manager, _ := NewManager(30 * time.Second)

	// Establish watermark at 30s, sealing [00:00, 00:30).
	if results := manager.Advance(at(30)); results != nil {
		t.Fatalf("first advance should seal nothing")
	}

	// Late event for the sealed window is rejected.
	if _, err := manager.Submit(Event{ID: "late", Time: at(29)}); !errors.Is(err, ErrLate) {
		t.Fatalf("late submit err = %v, want ErrLate", err)
	}
	// Event exactly at the watermark is also late (half-open, t <= wm).
	if _, err := manager.Submit(Event{ID: "edge", Time: at(30)}); !errors.Is(err, ErrLate) {
		t.Fatalf("edge submit err = %v, want ErrLate", err)
	}

	// Out-of-order arrival inside the open window [00:30, 01:00).
	manager.Submit(Event{ID: "b", Client: "c", RegisteredDomain: "d", QueriedName: "b.d", Time: at(55)})
	manager.Submit(Event{ID: "a", Client: "c", RegisteredDomain: "d", QueriedName: "a.d", Time: at(31)})
	manager.Submit(Event{ID: "mid", Client: "c", RegisteredDomain: "d", QueriedName: "m.d", Time: at(40)})

	// Advancing to 59s seals nothing; the window ends at 60s.
	if results := manager.Advance(at(59)); results != nil {
		t.Fatalf("advance to 59s sealed %d windows", len(results))
	}
	results := manager.Advance(at(60))
	if len(results) != 1 {
		t.Fatalf("expected 1 window, got %d", len(results))
	}
	if !results[0].Start.Equal(at(30)) || !results[0].End.Equal(at(60)) {
		t.Fatalf("bounds = [%v, %v)", results[0].Start, results[0].End)
	}
	group := results[0].Groups[0]
	if group.TotalEvents != 3 {
		t.Fatalf("total events = %d, want 3", group.TotalEvents)
	}
}

func TestCorrectionWithdrawsOldContribution(t *testing.T) {
	manager, _ := NewManager(time.Minute)

	original := Event{ID: "e1", Client: "c1", RegisteredDomain: "a.test", QueriedName: "aaaa.a.test", NXDOMAIN: true, Time: at(10)}
	manager.Submit(original)

	// Correct: different group, name and rcode, same open window.
	corrected := Event{ID: "e1", Client: "c2", RegisteredDomain: "b.test", QueriedName: "x.b.test", NXDOMAIN: false, Time: at(20)}
	outcome, err := manager.Submit(corrected)
	if err != nil || outcome != OutcomeCorrected {
		t.Fatalf("correction: outcome=%v err=%v", outcome, err)
	}

	results := manager.Advance(at(60))
	result := results[0]
	if len(result.Groups) != 1 {
		t.Fatalf("expected only corrected group, got %d groups", len(result.Groups))
	}
	group := result.Groups[0]
	if group.Client != "c2" || group.RegisteredDomain != "b.test" {
		t.Fatalf("unexpected group: %+v", group)
	}
	if group.TotalEvents != 1 || group.NXDOMAINEvents != 0 {
		t.Fatalf("counts = %d/%d, want 1/0", group.TotalEvents, group.NXDOMAINEvents)
	}
	if group.DistinctLabels != 3 { // x, b, test
		t.Fatalf("distinct labels = %d, want 3", group.DistinctLabels)
	}
	// Corrected name "x.b.test": labels of length 1 (x, b) and length 4
	// (test); no length-4 label from the old name remains beyond "test".
	if group.LabelLengthHist[4] != 1 || group.LabelLengthHist[1] != 2 {
		t.Fatalf("old label contribution not withdrawn: %v", group.LabelLengthHist)
	}

	// The sealed ID is tombstoned: reusing it after sealing must fail even
	// though the timestamp falls in an open window.
	if _, err := manager.Submit(Event{ID: "e1", Time: at(65)}); !errors.Is(err, ErrSealed) {
		t.Fatalf("reuse after seal: got %v, want ErrSealed", err)
	}
}

func TestCorrectionMovesAcrossWindows(t *testing.T) {
	manager, _ := NewManager(30 * time.Second)

	manager.Submit(Event{ID: "e1", Client: "c", RegisteredDomain: "d", QueriedName: "aa.d", NXDOMAIN: true, Time: at(10)})
	// Move the event into the next, still-open window (31s >= 30s watermark).
	outcome, err := manager.Submit(Event{ID: "e1", Client: "c", RegisteredDomain: "d", QueriedName: "bb.d", NXDOMAIN: false, Time: at(35)})
	if err != nil || outcome != OutcomeCorrected {
		t.Fatalf("cross-window correction: outcome=%v err=%v", outcome, err)
	}

	first := manager.Advance(at(30))
	if len(first) != 0 {
		t.Fatalf("first window should be empty after withdrawal, got %d", len(first))
	}
	second := manager.Advance(at(60))
	if len(second) != 1 || second[0].TotalEvents() != 1 {
		t.Fatalf("second window should hold the moved event, got %+v", second)
	}
	group := second[0].Groups[0]
	if group.NXDOMAINEvents != 0 || group.LabelLengthHist[2] != 1 || group.LabelLengthHist[1] != 1 {
		t.Fatalf("moved event features wrong: %+v", group)
	}
}

func TestSealedIDsRejectCorrectionAndReuse(t *testing.T) {
	manager, _ := NewManager(30 * time.Second)
	manager.Submit(Event{ID: "e1", Client: "c", RegisteredDomain: "d", QueriedName: "a.d", Time: at(10)})
	manager.Advance(at(30))

	if _, err := manager.Submit(Event{ID: "e1", Client: "c", RegisteredDomain: "d", QueriedName: "b.d", Time: at(31)}); !errors.Is(err, ErrSealed) {
		t.Fatalf("sealed correction err = %v, want ErrSealed", err)
	}
	// Same ID with a late timestamp still reports the sealed condition.
	if _, err := manager.Submit(Event{ID: "e1", Time: at(5)}); !errors.Is(err, ErrSealed) {
		t.Fatalf("sealed late correction err = %v, want ErrSealed", err)
	}
}

func TestInvalidEventsAndOptions(t *testing.T) {
	if _, err := NewManager(0); err == nil {
		t.Fatal("zero-size constructor must fail")
	}
	if _, err := NewManager(-time.Second); err == nil {
		t.Fatal("negative-size constructor must fail")
	}
	manager, _ := NewManager(time.Minute)
	if _, err := manager.Submit(Event{Time: at(1)}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("empty ID err = %v", err)
	}
	if _, err := manager.Submit(Event{ID: "x"}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("zero time err = %v", err)
	}
	// Non-monotonic Advance is a no-op.
	manager.Advance(at(60))
	if got := manager.Advance(at(45)); got != nil {
		t.Fatalf("backward advance returned %v", got)
	}
	if !manager.Watermark().Equal(at(60)) {
		t.Fatalf("watermark = %v, want 60s", manager.Watermark())
	}
}

// TotalEvents is a test helper summing a sealed window's groups.
func (r WindowResult) TotalEvents() int64 {
	var total int64
	for _, group := range r.Groups {
		total += group.TotalEvents
	}
	return total
}
