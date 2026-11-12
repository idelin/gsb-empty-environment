package netsec

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func newFakeAggregator(t *testing.T) (*Aggregator, *FakeScheduler) {
	t.Helper()
	clock := NewFakeScheduler(time.Unix(1_700_000_000, 0))
	return NewAggregator(WithScheduler(clock)), clock
}

func TestAggregatorValidation(t *testing.T) {
	aggregator := NewAggregator()

	badManifest := Manifest{Generation: -1}
	if !errors.Is(aggregator.InstallManifest(badManifest), ErrInvalidManifest) {
		t.Fatal("invalid manifest must return ErrInvalidManifest")
	}

	good := testManifest(1, 1, []CheckID{"av"}, 0)
	if err := aggregator.InstallManifest(good); err != nil {
		t.Fatalf("install: %v", err)
	}
	if !errors.Is(aggregator.InstallManifest(testManifest(0, 1, []CheckID{"av"}, 0)), ErrObsoleteManifest) {
		t.Fatal("older generation must return ErrObsoleteManifest")
	}
	if err := aggregator.InstallManifest(testManifest(1, 2, []CheckID{"av"}, 0)); !errors.Is(err, ErrConflictingManifest) {
		t.Fatalf("conflicting generation err = %v, want ErrConflictingManifest", err)
	}
	if err := aggregator.InstallManifest(good); err != nil {
		t.Fatalf("redundant identical manifest: %v", err)
	}

	malformed := Receipt{Generation: 1, ChunkIndex: -1, Check: "av", Verdict: VerdictPass}
	if applied, err := aggregator.Submit(malformed); err == nil || applied {
		t.Fatalf("malformed receipt = (%v, %v), want error and false", applied, err)
	}
}

func TestAggregatorMultipleDetectorsSameChunk(t *testing.T) {
	aggregator, _ := newFakeAggregator(t)
	manifest := testManifest(1, 2, []CheckID{"av", "dlp", "sandbox"}, time.Second)
	if err := aggregator.InstallManifest(manifest); err != nil {
		t.Fatalf("install: %v", err)
	}

	// Every detector must answer for every chunk; three passing detectors on
	// one chunk still leaves the second chunk outstanding.
	for _, check := range manifest.Checks {
		if applied, err := aggregator.Submit(passReceipt(1, 0, check)); err != nil || !applied {
			t.Fatalf("chunk0 %s submit = (%v, %v)", check, applied, err)
		}
	}
	decision := aggregator.Decision()
	if decision.Terminal || len(decision.Missing) != 3 {
		t.Fatalf("decision = %+v, want 3 missing slots on chunk 1", decision)
	}

	for _, check := range manifest.Checks {
		if _, err := aggregator.Submit(passReceipt(1, 1, check)); err != nil {
			t.Fatalf("chunk1 %s submit: %v", check, err)
		}
	}

	select {
	case decision := <-aggregator.Final():
		if decision.Status != StatusPass || !decision.Terminal {
			t.Fatalf("published decision = %+v, want pass", decision)
		}
	case <-time.After(time.Second):
		t.Fatal("Final channel never published the all-clear decision")
	}
}

func TestAggregatorDuplicateReceiptsDoNotBlockPublish(t *testing.T) {
	aggregator, _ := newFakeAggregator(t)
	if err := aggregator.InstallManifest(testManifest(1, 1, []CheckID{"av", "dlp"}, 0)); err != nil {
		t.Fatalf("install: %v", err)
	}

	receipt := passReceipt(1, 0, "av")
	for i := 0; i < 3; i++ {
		applied, err := aggregator.Submit(receipt)
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
		if i == 0 && !applied {
			t.Fatal("first receipt must be applied")
		}
		if i > 0 && applied {
			t.Fatalf("receipt %d must be ignored as duplicate", i)
		}
	}
	if _, err := aggregator.Submit(passReceipt(1, 0, "dlp")); err != nil {
		t.Fatalf("final receipt: %v", err)
	}

	decision := <-aggregator.Final()
	if decision.Status != StatusPass {
		t.Fatalf("decision = %+v, want pass despite duplicates", decision)
	}
	stats := aggregator.Stats()
	if stats.ReceiptsAccepted != 2 || stats.ReceiptsDuplicate != 2 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestAggregatorTimeoutPublishesIncompleteWithMissing(t *testing.T) {
	aggregator, clock := newFakeAggregator(t)
	if err := aggregator.InstallManifest(testManifest(1, 2, []CheckID{"av", "dlp"}, 5*time.Second)); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := aggregator.Submit(passReceipt(1, 0, "av")); err != nil {
		t.Fatalf("submit: %v", err)
	}

	clock.Advance(4 * time.Second)
	if decision := aggregator.Decision(); decision.Terminal {
		t.Fatalf("decision = %+v, must stay live before deadline", decision)
	}

	clock.Advance(time.Second)
	decision := <-aggregator.Final()
	if decision.Status != StatusIncomplete || decision.Reason != ReasonTimeout {
		t.Fatalf("decision = %+v, want timeout incomplete", decision)
	}
	if len(decision.Missing) != 3 {
		t.Fatalf("missing = %v, want 3 outstanding slots", decision.Missing)
	}

	// A late receipt after the timeout cannot resurrect the object.
	applied, err := aggregator.Submit(passReceipt(1, 0, "dlp"))
	if err != nil || applied {
		t.Fatalf("post-timeout submit = (%v, %v), want ignored", applied, err)
	}
}

func TestAggregatorReplacementCancelsOldTimerAndIgnoresLateResults(t *testing.T) {
	aggregator, clock := newFakeAggregator(t)
	if err := aggregator.InstallManifest(testManifest(1, 1, []CheckID{"av"}, 10*time.Second)); err != nil {
		t.Fatalf("install gen1: %v", err)
	}
	// Replace the fragment before the first deadline.
	if err := aggregator.InstallManifest(testManifest(2, 1, []CheckID{"av"}, 10*time.Second)); err != nil {
		t.Fatalf("install gen2: %v", err)
	}
	if clock.Pending() != 1 {
		t.Fatalf("pending timers = %d, want exactly one gen2 timer", clock.Pending())
	}

	// Passing the old deadline must not fire the canceled gen1 timer; stay
	// strictly before the freshly armed gen2 deadline as well.
	clock.Advance(9 * time.Second)
	if decision := aggregator.Decision(); decision.Terminal {
		t.Fatalf("decision = %+v, gen1 timer must be dead", decision)
	}

	// Late old-generation receipt is ignored.
	if applied, err := aggregator.Submit(passReceipt(1, 0, "av")); err != nil || applied {
		t.Fatalf("stale submit = (%v, %v)", applied, err)
	}

	clock.Advance(2 * time.Second)
	decision := <-aggregator.Final()
	if decision.Generation != 2 || decision.Reason != ReasonTimeout {
		t.Fatalf("decision = %+v, want gen2 timeout", decision)
	}
}

// TestAggregatorLastReceiptVersusCancelRace drives the cancellation through
// the same serialized entry points as detector receipts. Both possible
// orderings are asserted.
func TestAggregatorLastReceiptVersusCancelRace(t *testing.T) {
	t.Run("receipt_wins", func(t *testing.T) {
		aggregator, _ := newFakeAggregator(t)
		if err := aggregator.InstallManifest(testManifest(7, 1, []CheckID{"av"}, time.Second)); err != nil {
			t.Fatalf("install: %v", err)
		}
		if _, err := aggregator.Submit(passReceipt(7, 0, "av")); err != nil {
			t.Fatalf("submit: %v", err)
		}
		if aggregator.Cancel(7) {
			t.Fatal("cancel after publish must be ignored")
		}
		decision := <-aggregator.Final()
		if decision.Status != StatusPass {
			t.Fatalf("decision = %+v, want pass", decision)
		}
	})

	t.Run("cancel_wins", func(t *testing.T) {
		aggregator, _ := newFakeAggregator(t)
		if err := aggregator.InstallManifest(testManifest(7, 1, []CheckID{"av"}, time.Second)); err != nil {
			t.Fatalf("install: %v", err)
		}
		if !aggregator.Cancel(7) {
			t.Fatal("cancel must apply on live generation")
		}
		if applied, err := aggregator.Submit(passReceipt(7, 0, "av")); err != nil || applied {
			t.Fatalf("post-cancel submit = (%v, %v)", applied, err)
		}
		decision := <-aggregator.Final()
		if decision.Status != StatusIncomplete || decision.Reason != ReasonCancelled {
			t.Fatalf("decision = %+v, want cancelled incomplete", decision)
		}
	})
}

func TestAggregatorCancelStaleGenerationIgnored(t *testing.T) {
	aggregator, _ := newFakeAggregator(t)
	if err := aggregator.InstallManifest(testManifest(2, 1, []CheckID{"av"}, 0)); err != nil {
		t.Fatalf("install: %v", err)
	}
	if aggregator.Cancel(1) {
		t.Fatal("cancel targeting stale generation must be ignored")
	}
	if decision := aggregator.Decision(); decision.Terminal {
		t.Fatalf("decision = %+v, must remain live", decision)
	}
	if _, err := aggregator.Submit(passReceipt(2, 0, "av")); err != nil {
		t.Fatalf("submit: %v", err)
	}
	decision := <-aggregator.Final()
	if decision.Status != StatusPass {
		t.Fatalf("decision = %+v, want pass", decision)
	}
}

func TestAggregatorConcurrentSubmitsAreSerialized(t *testing.T) {
	aggregator := NewAggregator()
	const chunks, detectors = 8, 4
	checks := make([]CheckID, 0, detectors)
	for i := 0; i < detectors; i++ {
		checks = append(checks, CheckID("det"+string(rune('0'+i))))
	}
	if err := aggregator.InstallManifest(testManifest(1, chunks, checks, 0)); err != nil {
		t.Fatalf("install: %v", err)
	}

	var wg sync.WaitGroup
	for chunk := 0; chunk < chunks; chunk++ {
		for _, check := range checks {
			receipt := passReceipt(1, chunk, check)
			wg.Add(2)
			go func() {
				defer wg.Done()
				_, _ = aggregator.Submit(receipt)
			}()
			go func() {
				defer wg.Done()
				// Duplicate concurrent delivery must not corrupt counters.
				_, _ = aggregator.Submit(receipt)
			}()
		}
	}

	decision := <-aggregator.Final()
	wg.Wait()
	if decision.Status != StatusPass || len(decision.Missing) != 0 {
		t.Fatalf("decision = %+v, want pass with no missing slots", decision)
	}
	stats := aggregator.Stats()
	if stats.ReceiptsAccepted != chunks*detectors {
		t.Fatalf("accepted = %d, want %d", stats.ReceiptsAccepted, chunks*detectors)
	}
	if stats.ReceiptsAccepted+stats.ReceiptsDuplicate != 2*chunks*detectors {
		t.Fatalf("stats = %+v, every submission must be accepted or duplicated", stats)
	}
}

func TestAggregatorTerminalDisarmsTimer(t *testing.T) {
	aggregator, clock := newFakeAggregator(t)
	if err := aggregator.InstallManifest(testManifest(1, 1, []CheckID{"av"}, time.Minute)); err != nil {
		t.Fatalf("install: %v", err)
	}
	if clock.Pending() != 1 {
		t.Fatalf("pending = %d, want armed timeout", clock.Pending())
	}

	// Completing the object must disarm the timeout.
	if _, err := aggregator.Submit(passReceipt(1, 0, "av")); err != nil {
		t.Fatalf("submit: %v", err)
	}
	<-aggregator.Final()
	clock.Advance(time.Minute)
	if pending := clock.Pending(); pending != 0 {
		t.Fatalf("pending = %d, timer must be canceled at terminal", pending)
	}
}

func TestAggregatorCancelDisarmsTimer(t *testing.T) {
	aggregator, clock := newFakeAggregator(t)
	if err := aggregator.InstallManifest(testManifest(1, 1, []CheckID{"av"}, time.Minute)); err != nil {
		t.Fatalf("install: %v", err)
	}
	if !aggregator.Cancel(1) {
		t.Fatal("cancel rejected on live generation")
	}
	decision := <-aggregator.Final()
	if decision.Reason != ReasonCancelled {
		t.Fatalf("decision = %+v, want cancel", decision)
	}
	clock.Advance(time.Minute)
	if pending := clock.Pending(); pending != 0 {
		t.Fatalf("pending = %d, timer must be canceled at terminal", pending)
	}
}

func TestAggregatorDecisionBeforeManifest(t *testing.T) {
	aggregator := NewAggregator()
	decision := aggregator.Decision()
	if decision.Status != StatusIncomplete || decision.Reason != ReasonAwaitingManifest || decision.Generation != -1 {
		t.Fatalf("decision = %+v, want awaiting-manifest incomplete", decision)
	}
	if applied, err := aggregator.Submit(passReceipt(0, 0, "av")); err != nil || applied {
		t.Fatalf("submit without manifest = (%v, %v), want ignored", applied, err)
	}
}
