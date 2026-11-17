package netsec

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

func credEvents() []Event {
	// Arrival order is deliberately scrambled; sequences inside each channel
	// are the only trustworthy ordering signal.
	return []Event{
		{ID: "u-late", Kind: KindUsage, CredID: "cred-1", Channel: "svc-a", Sequence: 30, Lo: 8, Hi: 12},
		{ID: "rev-1", Kind: KindRevocation, CredID: "cred-1", Channel: "svc-a", Sequence: 20, Lo: 4, Hi: 6},
		{ID: "u-early", Kind: KindUsage, CredID: "cred-1", Channel: "svc-a", Sequence: 10, Lo: 0, Hi: 3},
		{ID: "rev-2", Kind: KindRevocation, CredID: "cred-2", Channel: "svc-b", Sequence: 5, Lo: 5, Hi: 5},
		{ID: "u-cross-before", Kind: KindUsage, CredID: "cred-2", Channel: "svc-c", Sequence: 1, Lo: 0, Hi: 3},
		{ID: "u-cross-after", Kind: KindUsage, CredID: "cred-3", Channel: "svc-c", Sequence: 2, Lo: 9, Hi: 12},
		{ID: "rev-3", Kind: KindRevocation, CredID: "cred-3", Channel: "svc-b", Sequence: 6, Lo: 5, Hi: 8},
		{ID: "u-cross-overlap", Kind: KindUsage, CredID: "cred-4", Channel: "svc-c", Sequence: 3, Lo: 7, Hi: 11},
		{ID: "rev-4", Kind: KindRevocation, CredID: "cred-4", Channel: "svc-b", Sequence: 7, Lo: 7, Hi: 10},
	}
}

func findingIndex(report Report) map[[2]string]Finding {
	index := make(map[[2]string]Finding, len(report.Findings))
	for _, finding := range report.Findings {
		index[[2]string{finding.Usage.Event.ID, finding.Revocation.Event.ID}] = finding
	}
	return index
}

func TestNarrowUsesSequenceConstraints(t *testing.T) {
	c, err := NewCorrelator(credEvents())
	if err != nil {
		t.Fatalf("NewCorrelator: %v", err)
	}
	narrowed, feasible := c.Narrow()

	byKey := make(map[string]NarrowedEvent, len(narrowed))
	for _, ne := range narrowed {
		byKey[ne.Event.ID] = ne
		if !feasible[ne.Event.Channel] || !ne.Feasible {
			t.Fatalf("event %q unexpectedly infeasible", ne.Event.ID)
		}
	}

	// svc-a sorted by sequence: u-early [0,3], rev-1 [4,6], u-late [8,12].
	// The forward/backward passes must reproduce those bounds even though
	// nothing needs shrinking here, and re-order the scrambled arrivals.
	wantOrder := []string{"u-early", "rev-1", "u-late"}
	var gotOrder []string
	for _, ne := range narrowed {
		if ne.Event.Channel == "svc-a" {
			gotOrder = append(gotOrder, ne.Event.ID)
		}
	}
	if fmt.Sprint(gotOrder) != fmt.Sprint(wantOrder) {
		t.Fatalf("svc-a order = %v, want %v", gotOrder, wantOrder)
	}

	// A loose interval gets squeezed: if rev-1 could occur in [4,20], the
	// later usage's lower bound still forces nothing, but its own upper bound
	// is capped by u-late's Lo through backward propagation when inverted.
	loose := []Event{
		{ID: "e1", Kind: KindUsage, CredID: "c", Channel: "ch", Sequence: 1, Lo: 0, Hi: 100},
		{ID: "e2", Kind: KindRevocation, CredID: "c", Channel: "ch", Sequence: 2, Lo: 0, Hi: 100},
		{ID: "e3", Kind: KindUsage, CredID: "c", Channel: "ch", Sequence: 3, Lo: 10, Hi: 20},
	}
	looseCorr, err := NewCorrelator(loose)
	if err != nil {
		t.Fatalf("NewCorrelator loose: %v", err)
	}
	looseNarrowed, _ := looseCorr.Narrow()
	looseByKey := map[string]NarrowedEvent{}
	for _, ne := range looseNarrowed {
		looseByKey[ne.Event.ID] = ne
	}
	// e3 forces t3 >= 10; strict order leaves t1 <= 18 and t2 <= 19 at most.
	if got := looseByKey["e1"].NarrowHi; got != 18 {
		t.Fatalf("e1 NarrowHi = %d, want 18", got)
	}
	if got := looseByKey["e2"].NarrowHi; got != 19 {
		t.Fatalf("e2 NarrowHi = %d, want 19", got)
	}
	if got := looseByKey["e3"].NarrowLo; got != 10 {
		t.Fatalf("e3 NarrowLo = %d, want 10", got)
	}
}

func TestCorrelateVerdicts(t *testing.T) {
	c, err := NewCorrelator(credEvents())
	if err != nil {
		t.Fatalf("NewCorrelator: %v", err)
	}
	report := c.Correlate()
	findings := findingIndex(report)

	cases := []struct {
		usage, revocation string
		verdict           Verdict
		hasConflict       bool
	}{
		{"u-early", "rev-1", VerdictBefore, false},
		{"u-late", "rev-1", VerdictAfter, false},
		{"u-cross-before", "rev-2", VerdictBefore, false},
		{"u-cross-after", "rev-3", VerdictAfter, false},
		{"u-cross-overlap", "rev-4", VerdictStraddle, true},
	}
	for _, tc := range cases {
		finding, ok := findings[[2]string{tc.usage, tc.revocation}]
		if !ok {
			t.Fatalf("missing finding for (%s,%s)", tc.usage, tc.revocation)
		}
		if finding.Verdict != tc.verdict {
			t.Fatalf("(%s,%s) verdict = %s, want %s", tc.usage, tc.revocation, finding.Verdict, tc.verdict)
		}
		if (finding.Conflict != "") != tc.hasConflict {
			t.Fatalf("(%s,%s) conflict = %q, want present=%v", tc.usage, tc.revocation, finding.Conflict, tc.hasConflict)
		}
	}
}

func TestInfeasibleChannelIsRetainedAsConflict(t *testing.T) {
	// Sequence says e1 <= e2 <= e3, but the reported intervals cannot host a
	// monotone assignment. The events must survive in the report instead of
	// being dropped.
	events := []Event{
		{ID: "u1", Kind: KindUsage, CredID: "cred", Channel: "ch", Sequence: 1, Lo: 0, Hi: 2},
		{ID: "u2", Kind: KindUsage, CredID: "cred", Channel: "ch", Sequence: 2, Lo: 8, Hi: 9},
		{ID: "r1", Kind: KindRevocation, CredID: "cred", Channel: "ch", Sequence: 3, Lo: 3, Hi: 4},
	}
	c, err := NewCorrelator(events)
	if err != nil {
		t.Fatalf("NewCorrelator: %v", err)
	}
	report := c.Correlate()
	if report.ChannelFeasible["ch"] {
		t.Fatal("channel ch unexpectedly reported feasible")
	}
	if len(report.Events) != 3 {
		t.Fatalf("retained events = %d, want 3", len(report.Events))
	}
	for _, finding := range report.Findings {
		if finding.Verdict != VerdictStraddle || finding.Conflict == "" {
			t.Fatalf("finding %s->%s = %s conflict=%q, want retained conflict",
				finding.Usage.Event.ID, finding.Revocation.Event.ID, finding.Verdict, finding.Conflict)
		}
	}

	cross := EnumerateOrderings(report, 0)
	if len(cross.Mismatches) != 0 {
		t.Fatalf("infeasible channel produced mismatches: %+v", cross.Mismatches)
	}
	for _, outcome := range cross.Outcomes {
		if !outcome.Infeasible {
			t.Fatalf("outcome %+v should be marked infeasible", outcome)
		}
	}
}

func TestEnumerationMatchesAnalytical(t *testing.T) {
	c, err := NewCorrelator(credEvents())
	if err != nil {
		t.Fatalf("NewCorrelator: %v", err)
	}
	report, cross := c.CrossCheck(0)
	if len(cross.TruncatedChannels) != 0 {
		t.Fatalf("small domain should enumerate exhaustively, truncated=%v", cross.TruncatedChannels)
	}
	if len(cross.Mismatches) != 0 {
		t.Fatalf("analytical/enumeration mismatch: %+v", cross.Mismatches)
	}

	byPair := map[[2]string]EnumOutcome{}
	for _, outcome := range cross.Outcomes {
		byPair[[2]string{outcome.UsageID, outcome.RevocationID}] = outcome
		if !outcome.Exhaustive {
			t.Fatalf("outcome %+v not exhaustive", outcome)
		}
	}

	overlap := byPair[[2]string{"u-cross-overlap", "rev-4"}]
	if overlap.Before != 0 || overlap.After == 0 || overlap.Straddle == 0 {
		t.Fatalf("overlapping pair should realize later and equal orders but never earlier, got %+v", overlap)
	}
	before := byPair[[2]string{"u-cross-before", "rev-2"}]
	if before.After != 0 || before.Straddle != 0 || before.Before == 0 {
		t.Fatalf("necessarily-before pair realized other orders: %+v", before)
	}
	after := byPair[[2]string{"u-cross-after", "rev-3"}]
	if after.Before != 0 || after.Straddle != 0 || after.After == 0 {
		t.Fatalf("necessarily-after pair realized other orders: %+v", after)
	}

	if len(report.Findings) != len(cross.Outcomes) {
		t.Fatalf("findings=%d outcomes=%d", len(report.Findings), len(cross.Outcomes))
	}
}

// TestRandomizedCrossCheck builds random small time domains and verifies that
// the analytical classifier never disagrees with exhaustive enumeration.
func TestRandomizedCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(20261118))
	for iteration := 0; iteration < 300; iteration++ {
		events := randomScenario(rng)
		c, err := NewCorrelator(events)
		if err != nil {
			t.Fatalf("iteration %d: %v", iteration, err)
		}
		report := c.Correlate()
		allFeasible := true
		for _, ne := range report.Events {
			if !ne.Feasible {
				allFeasible = false
			}
		}
		if !allFeasible {
			continue
		}
		cross := EnumerateOrderings(report, 50_000)
		if len(cross.TruncatedChannels) != 0 {
			t.Fatalf("iteration %d: random domain too large for budget: %v", iteration, cross.TruncatedChannels)
		}
		if len(cross.Mismatches) != 0 {
			t.Fatalf("iteration %d: %+v", iteration, cross.Mismatches)
		}
	}
}

func randomScenario(rng *rand.Rand) []Event {
	const channels = 3
	const perChannel = 4
	const domain = 6

	events := make([]Event, 0, channels*perChannel)
	for ch := 0; ch < channels; ch++ {
		channelName := fmt.Sprintf("ch-%d", ch)
		for seq := 0; seq < perChannel; seq++ {
			lo := rng.Int63n(domain)
			hi := lo + rng.Int63n(domain)
			kind := KindUsage
			if rng.Intn(2) == 0 {
				kind = KindRevocation
			}
			events = append(events, Event{
				ID:       fmt.Sprintf("%s-e%d", channelName, seq),
				Kind:     kind,
				CredID:   fmt.Sprintf("cred-%d", rng.Intn(3)),
				Channel:  channelName,
				Sequence: int64(seq),
				Lo:       lo,
				Hi:       hi,
			})
		}
	}
	return events
}

func TestValidationErrors(t *testing.T) {
	base := Event{ID: "e", Kind: KindUsage, CredID: "c", Channel: "ch", Sequence: 1, Lo: 0, Hi: 1}

	t.Run("empty interval", func(t *testing.T) {
		ev := base
		ev.Hi = -1
		_, err := NewCorrelator([]Event{ev})
		if !errors.Is(err, ErrInvalidEvent) {
			t.Fatalf("err = %v, want ErrInvalidEvent", err)
		}
	})
	t.Run("duplicate sequence", func(t *testing.T) {
		other := base
		other.ID = "e2"
		_, err := NewCorrelator([]Event{base, other})
		if !errors.Is(err, ErrInvalidEvent) {
			t.Fatalf("err = %v, want ErrInvalidEvent", err)
		}
	})
	t.Run("duplicate id", func(t *testing.T) {
		other := base
		other.Sequence = 2
		_, err := NewCorrelator([]Event{base, other})
		if !errors.Is(err, ErrInvalidEvent) {
			t.Fatalf("err = %v, want ErrInvalidEvent", err)
		}
	})
}

func ExampleCorrelator() {
	events := []Event{
		{ID: "use", Kind: KindUsage, CredID: "key-7", Channel: "iam", Sequence: 2, Lo: 9, Hi: 12},
		{ID: "delete", Kind: KindRevocation, CredID: "key-7", Channel: "iam", Sequence: 1, Lo: 4, Hi: 8},
	}
	c, _ := NewCorrelator(events)
	report, cross := c.CrossCheck(0)
	fmt.Println(report.Findings[0].Verdict, len(cross.Mismatches) == 0)
	// Output: after true
}
