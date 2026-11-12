package netsec

import (
	"testing"
	"time"
)

func testManifest(generation, chunks int, checks []CheckID, timeout time.Duration) Manifest {
	specs := make([]ChunkSpec, chunks)
	for i := range specs {
		specs[i] = ChunkSpec{
			Index:  i,
			Length: int64(100 + i),
			Digest: Digest{Algorithm: "sha256", Hex: "deadbeef"},
		}
	}
	return Manifest{
		Generation: generation,
		Chunks:     specs,
		Checks:     checks,
		Timeout:    timeout,
	}
}

func passReceipt(generation, chunk int, check CheckID) Receipt {
	return Receipt{Generation: generation, ChunkIndex: chunk, Check: check, Verdict: VerdictPass}
}

func mustInstall(t *testing.T, m *machine, manifest Manifest) {
	t.Helper()
	if got := m.applyManifest(manifest); got != installApplied {
		t.Fatalf("applyManifest = %d, want %d", got, installApplied)
	}
}

func fillAll(t *testing.T, m *machine, verdict CheckVerdict) {
	t.Helper()
	state := m.current
	for _, chunk := range state.manifest.Chunks {
		for _, check := range state.manifest.Checks {
			receipt := Receipt{
				Generation: state.manifest.Generation,
				ChunkIndex: chunk.Index,
				Check:      check,
				Verdict:    verdict,
			}
			if m.current.slots[chunk.Index][checkIndexOf(t, state.manifest, check)].filled {
				continue
			}
			if !m.applyReceipt(receipt) {
				t.Fatalf("receipt %+v was not accepted", receipt)
			}
		}
	}
}

func checkIndexOf(t *testing.T, manifest Manifest, check CheckID) int {
	t.Helper()
	for i, candidate := range manifest.Checks {
		if candidate == check {
			return i
		}
	}
	t.Fatalf("check %q not in manifest", check)
	return -1
}

func TestMachineHappyPathPublishesOnlyWhenAllSlotsClear(t *testing.T) {
	m := newMachine()
	mustInstall(t, m, testManifest(1, 2, []CheckID{"av", "dlp"}, 0))

	// One chunk passing must not publish the whole object.
	if !m.applyReceipt(passReceipt(1, 0, "av")) {
		t.Fatal("first receipt rejected")
	}
	decision := m.decision()
	if decision.Terminal || decision.Status != StatusIncomplete {
		t.Fatalf("decision = %+v, want non-terminal incomplete", decision)
	}
	if len(decision.Missing) != 3 {
		t.Fatalf("missing = %d, want 3", len(decision.Missing))
	}

	fillAll(t, m, VerdictPass)
	decision = m.decision()
	if !decision.Terminal || decision.Status != StatusPass || decision.Reason != ReasonAllClear {
		t.Fatalf("decision = %+v, want terminal pass", decision)
	}
	if len(decision.Missing) != 0 {
		t.Fatalf("missing = %v, want none", decision.Missing)
	}
}

func TestMachinePositiveBlocksAndIsTerminal(t *testing.T) {
	m := newMachine()
	mustInstall(t, m, testManifest(3, 2, []CheckID{"av", "dlp"}, 0))

	positive := Receipt{Generation: 3, ChunkIndex: 1, Check: "dlp", Verdict: VerdictPositive}
	if !m.applyReceipt(positive) {
		t.Fatal("positive receipt rejected")
	}
	decision := m.decision()
	if decision.Status != StatusBlocked || decision.Reason != ReasonPositive || !decision.Terminal {
		t.Fatalf("decision = %+v, want terminal block", decision)
	}
	if len(decision.Positives) != 1 {
		t.Fatalf("positives = %v", decision.Positives)
	}

	// Further receipts cannot change a terminal decision.
	if m.applyReceipt(passReceipt(3, 0, "av")) {
		t.Fatal("receipt after terminal state must be ignored")
	}
	if got := m.decision().Status; got != StatusBlocked {
		t.Fatalf("status changed to %v after terminal", got)
	}
}

func TestMachineFailedCheckIsIncomplete(t *testing.T) {
	m := newMachine()
	mustInstall(t, m, testManifest(1, 1, []CheckID{"av"}, 0))

	failed := Receipt{Generation: 1, ChunkIndex: 0, Check: "av", Verdict: VerdictFailed}
	if !m.applyReceipt(failed) {
		t.Fatal("failed receipt rejected")
	}
	decision := m.decision()
	if decision.Status != StatusIncomplete || decision.Reason != ReasonCheckFailed || !decision.Terminal {
		t.Fatalf("decision = %+v, want terminal incomplete/failed", decision)
	}
	if len(decision.Failures) != 1 {
		t.Fatalf("failures = %v", decision.Failures)
	}
}

func TestMachineDuplicateReceiptIsIdempotentAndKeepsFirst(t *testing.T) {
	m := newMachine()
	mustInstall(t, m, testManifest(1, 1, []CheckID{"av"}, 0))

	first := Receipt{Generation: 1, ChunkIndex: 0, Check: "av", Verdict: VerdictPass}
	if !m.applyReceipt(first) {
		t.Fatal("first receipt rejected")
	}
	// Repeated delivery, then a contradictory redelivery: the first verdict
	// wins and the slot is not double counted.
	if m.applyReceipt(first) {
		t.Fatal("duplicate receipt must be ignored")
	}
	redelivery := Receipt{Generation: 1, ChunkIndex: 0, Check: "av", Verdict: VerdictPositive}
	if m.applyReceipt(redelivery) {
		t.Fatal("redelivery to filled slot must be ignored")
	}
	if m.stats.ReceiptsAccepted != 1 || m.stats.ReceiptsDuplicate != 2 {
		t.Fatalf("stats = %+v", m.stats)
	}
	decision := m.decision()
	if decision.Status != StatusPass || !decision.Terminal {
		t.Fatalf("decision = %+v, want pass from first receipt", decision)
	}
}

func TestMachineStaleGenerationAndUnknownSlotsIgnored(t *testing.T) {
	m := newMachine()
	mustInstall(t, m, testManifest(2, 1, []CheckID{"av", "dlp"}, 0))

	// Late result from the previous manifest generation.
	if m.applyReceipt(passReceipt(1, 0, "av")) {
		t.Fatal("stale generation receipt must be ignored")
	}
	// A future generation is also not the current one.
	if m.applyReceipt(passReceipt(3, 0, "av")) {
		t.Fatal("future generation receipt must be ignored")
	}
	// Unknown chunk and check within the right generation.
	if m.applyReceipt(passReceipt(2, 9, "av")) {
		t.Fatal("unknown chunk receipt must be ignored")
	}
	unknownCheck := Receipt{Generation: 2, ChunkIndex: 0, Check: "magic", Verdict: VerdictPass}
	if m.applyReceipt(unknownCheck) {
		t.Fatal("unknown check receipt must be ignored")
	}
	if m.stats.ReceiptsStale != 2 || m.stats.ReceiptsUnknown != 2 || m.stats.ReceiptsAccepted != 0 {
		t.Fatalf("stats = %+v", m.stats)
	}
}

func TestMachineFragmentReplacementResetsGeneration(t *testing.T) {
	m := newMachine()
	mustInstall(t, m, testManifest(1, 2, []CheckID{"av"}, 0))

	if !m.applyReceipt(passReceipt(1, 0, "av")) {
		t.Fatal("gen1 receipt rejected")
	}
	// A replaced fragment produces a newer, larger manifest.
	mustInstall(t, m, testManifest(2, 3, []CheckID{"av"}, 0))

	decision := m.decision()
	if decision.Generation != 2 || decision.Terminal || len(decision.Missing) != 3 {
		t.Fatalf("decision after replacement = %+v", decision)
	}
	// Old-generation results are late and ignored.
	if m.applyReceipt(passReceipt(1, 1, "av")) {
		t.Fatal("late old-generation receipt must be ignored")
	}
	fillAll(t, m, VerdictPass)
	if got := m.decision(); got.Status != StatusPass || got.Generation != 2 {
		t.Fatalf("decision = %+v, want gen2 pass", got)
	}
}

func TestMachineRedundantAndObsoleteManifests(t *testing.T) {
	m := newMachine()
	manifest := testManifest(1, 1, []CheckID{"av"}, 0)
	mustInstall(t, m, manifest)

	if got := m.applyManifest(manifest); got != installRedundant {
		t.Fatalf("identical manifest = %d, want redundant", got)
	}
	if got := m.applyManifest(testManifest(0, 1, []CheckID{"av"}, 0)); got != installRejectedObsolete {
		t.Fatalf("older manifest = %d, want obsolete", got)
	}
	conflicting := testManifest(1, 2, []CheckID{"av"}, 0)
	if got := m.applyManifest(conflicting); got != installRejectedConflict {
		t.Fatalf("conflicting manifest = %d, want conflict", got)
	}
}

func TestMachineTimeoutIsIncompleteAndStaleTimeoutIgnored(t *testing.T) {
	m := newMachine()
	mustInstall(t, m, testManifest(1, 1, []CheckID{"av"}, time.Millisecond))

	if !m.applyTimeout(1) {
		t.Fatal("current-generation timeout rejected")
	}
	if got := m.decision(); got.Status != StatusIncomplete || got.Reason != ReasonTimeout || !got.Terminal {
		t.Fatalf("decision = %+v, want timeout", got)
	}
	// Repeated timeout has no effect.
	if m.applyTimeout(1) {
		t.Fatal("repeated timeout must be ignored")
	}

	// After replacement, a callback tagged with the old generation is stale.
	mustInstall(t, m, testManifest(2, 1, []CheckID{"av"}, time.Millisecond))
	if m.applyTimeout(1) {
		t.Fatal("old-generation timeout must be ignored")
	}
	if got := m.decision(); got.Generation != 2 || got.Terminal {
		t.Fatalf("decision = %+v, want live gen2", got)
	}
}

// TestMachineLastReceiptVersusCancelRace scripts both orderings of the race
// between the final receipt and the cancellation. The reducer has no internal
// goroutines, so the observed order is deterministic and must be honored.
func TestMachineLastReceiptVersusCancelRace(t *testing.T) {
	t.Run("receipt_first_then_cancel", func(t *testing.T) {
		m := newMachine()
		mustInstall(t, m, testManifest(1, 1, []CheckID{"av"}, 0))

		if !m.applyReceipt(passReceipt(1, 0, "av")) {
			t.Fatal("final receipt rejected")
		}
		if m.applyCancel(1) {
			t.Fatal("cancel after terminal pass must be ignored")
		}
		if got := m.decision(); got.Status != StatusPass {
			t.Fatalf("decision = %+v, want pass", got)
		}
	})

	t.Run("cancel_first_then_receipt", func(t *testing.T) {
		m := newMachine()
		mustInstall(t, m, testManifest(1, 1, []CheckID{"av"}, 0))

		if !m.applyCancel(1) {
			t.Fatal("cancel rejected")
		}
		if m.applyReceipt(passReceipt(1, 0, "av")) {
			t.Fatal("receipt after cancel must be ignored")
		}
		decision := m.decision()
		if decision.Status != StatusIncomplete || decision.Reason != ReasonCancelled || !decision.Terminal {
			t.Fatalf("decision = %+v, want cancelled", decision)
		}
	})
}

func TestMachineLastReceiptVersusTimeoutRace(t *testing.T) {
	t.Run("completion_first", func(t *testing.T) {
		m := newMachine()
		mustInstall(t, m, testManifest(1, 1, []CheckID{"av"}, time.Millisecond))
		fillAll(t, m, VerdictPass)
		if m.applyTimeout(1) {
			t.Fatal("timeout after completion must be ignored")
		}
		if got := m.decision(); got.Status != StatusPass {
			t.Fatalf("decision = %+v, want pass", got)
		}
	})

	t.Run("timeout_first", func(t *testing.T) {
		m := newMachine()
		mustInstall(t, m, testManifest(1, 1, []CheckID{"av"}, time.Millisecond))
		if !m.applyTimeout(1) {
			t.Fatal("timeout rejected")
		}
		if m.applyReceipt(passReceipt(1, 0, "av")) {
			t.Fatal("receipt after timeout must be ignored")
		}
		if got := m.decision(); got.Reason != ReasonTimeout {
			t.Fatalf("decision = %+v, want timeout", got)
		}
	})
}

// TestMachineScriptedEventLog drives one logged event sequence covering
// repeated receipts, a fragment replacement and a stale late result.
func TestMachineScriptedEventLog(t *testing.T) {
	type scriptedEvent struct {
		name    string
		applied bool
		apply   func(*machine) bool
	}

	events := []scriptedEvent{
		{name: "install gen1", applied: true, apply: func(m *machine) bool {
			return m.applyManifest(testManifest(1, 2, []CheckID{"av"}, 0)) == installApplied
		}},
		{name: "gen1 chunk0 pass", applied: true, apply: func(m *machine) bool {
			return m.applyReceipt(passReceipt(1, 0, "av"))
		}},
		{name: "duplicate gen1 chunk0", applied: false, apply: func(m *machine) bool {
			return m.applyReceipt(passReceipt(1, 0, "av"))
		}},
		{name: "replace with gen2", applied: true, apply: func(m *machine) bool {
			return m.applyManifest(testManifest(2, 2, []CheckID{"av"}, 0)) == installApplied
		}},
		{name: "late gen1 chunk1", applied: false, apply: func(m *machine) bool {
			return m.applyReceipt(passReceipt(1, 1, "av"))
		}},
		{name: "gen2 chunk0 pass", applied: true, apply: func(m *machine) bool {
			return m.applyReceipt(passReceipt(2, 0, "av"))
		}},
		{name: "gen2 chunk1 pass", applied: true, apply: func(m *machine) bool {
			return m.applyReceipt(passReceipt(2, 1, "av"))
		}},
	}

	m := newMachine()
	for _, event := range events {
		if got := event.apply(m); got != event.applied {
			t.Fatalf("%s: applied = %v, want %v", event.name, got, event.applied)
		}
	}
	decision := m.decision()
	if !decision.Terminal || decision.Status != StatusPass || decision.Generation != 2 {
		t.Fatalf("final decision = %+v, want gen2 pass", decision)
	}
}
