package replayhandoff

import (
	"sync"
	"testing"
)

const testDevice DeviceID = "sensor-7"

func acceptAllCheckpoints(device DeviceID, generation KeyGeneration, fullSequence uint64, signature []byte) bool {
	return string(signature) == "signed"
}

func newTestReceiver(t *testing.T, bits int) *Receiver {
	t.Helper()
	receiver, err := New(Config{Bits: bits, Verifier: acceptAllCheckpoints})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return receiver
}

func checkpoint(full uint64) Checkpoint {
	return Checkpoint{FullSequence: full, Signature: []byte("signed")}
}

func snapshotWindow(receiver *Receiver, device DeviceID, generation KeyGeneration) *deviceWindow {
	source := receiver.state(StateKey{Device: device, Generation: generation})
	source.mu.Lock()
	defer source.mu.Unlock()

	snapshot := &deviceWindow{top: source.top, hasTop: source.hasTop, seen: map[uint64]struct{}{}}
	for value := range source.seen {
		snapshot.seen[value] = struct{}{}
	}
	return snapshot
}

func frame(low uint64) Frame {
	return Frame{Device: testDevice, Generation: 1, LowSequence: low}
}

func material(anchors []uint64, logs ...NodeLog) HandoffMaterial {
	checkpoints := make([]Checkpoint, 0, len(anchors))
	for _, anchor := range anchors {
		checkpoints = append(checkpoints, checkpoint(anchor))
	}
	return HandoffMaterial{
		Device:      testDevice,
		Generation:  1,
		Checkpoints: checkpoints,
		NodeLogs:    logs,
	}
}

func TestHalfWindowAmbiguityShowsOppositeFullAssignments(t *testing.T) {
	receiver := newTestReceiver(t, 4)

	first := receiver.Admit(frame(0), true, material([]uint64{16}))
	if first.Outcome != OutcomeAccepted {
		t.Fatalf("first admission outcome = %s, conflicts = %+v", first.Outcome, first.Conflicts)
	}

	ambiguous := receiver.Admit(frame(8), true, material([]uint64{16}))
	if ambiguous.Outcome != OutcomeNeedAnchor {
		t.Fatalf("ambiguous outcome = %s, want need_anchor", ambiguous.Outcome)
	}
	if ambiguous.NeedAnchorLow != 8 || len(ambiguous.Alternatives) != 2 {
		t.Fatalf("ambiguous decision = %+v", ambiguous)
	}
	if ambiguous.Alternatives[0].CandidateFull != 8 || ambiguous.Alternatives[0].CandidateFresh {
		t.Fatalf("first alternative = %+v, want old full sequence 8", ambiguous.Alternatives[0])
	}
	if ambiguous.Alternatives[1].CandidateFull != 24 || !ambiguous.Alternatives[1].CandidateFresh {
		t.Fatalf("second alternative = %+v, want fresh full sequence 24", ambiguous.Alternatives[1])
	}

	state := receiver.state(StateKey{Device: testDevice, Generation: 1})
	state.mu.Lock()
	top := state.top
	_, seen8 := state.seen[8]
	_, seen24 := state.seen[24]
	state.mu.Unlock()
	if top != 16 || seen8 || seen24 {
		t.Fatalf("ambiguous admission changed state: top=%d seen8=%v seen24=%v", top, seen8, seen24)
	}

	exhaustive := exhaustiveInterpret(material([]uint64{16}), 8, 8, 16, state)
	if len(exhaustive.assignments) == 0 || len(exhaustive.fresh) != 1 || len(exhaustive.old) != 1 {
		t.Fatalf("exhaustive interpretation = %+v", exhaustive)
	}
	if exhaustive.fresh[0] != 24 || exhaustive.old[0] != 8 {
		t.Fatalf("exhaustive fresh/old = %v/%v", exhaustive.fresh, exhaustive.old)
	}
}

func TestAllOldAssignmentRejectsAndAllFreshAcceptsAcrossTwoWraps(t *testing.T) {
	receiver := newTestReceiver(t, 4)
	logs := []NodeLog{
		{Node: "a", Entries: []LogEntry{{LowSequence: 0, Admitted: true}, {LowSequence: 2, Admitted: true}}},
		{Node: "b", Entries: []LogEntry{{LowSequence: 2, Admitted: true}}},
	}

	imported := receiver.Import(material([]uint64{34}, logs...))
	if imported.Outcome != OutcomeAccepted {
		t.Fatalf("import outcome = %s, conflicts = %+v", imported.Outcome, imported.Conflicts)
	}

	allOld := receiver.Admit(frame(8), true, material([]uint64{16}))
	if allOld.Outcome != OutcomeRejected {
		t.Fatalf("all-old outcome = %s, want rejected", allOld.Outcome)
	}
	if len(allOld.FullSequences) != 2 || allOld.FullSequences[0] != 8 || allOld.FullSequences[1] != 24 {
		t.Fatalf("all-old assignments = %+v", allOld)
	}

	beforeFresh := snapshotWindow(receiver, testDevice, 1)
	allFresh := receiver.Admit(frame(8), true, material([]uint64{56}))
	if allFresh.Outcome != OutcomeAccepted {
		t.Fatalf("all-fresh outcome = %s, conflicts = %+v", allFresh.Outcome, allFresh.Conflicts)
	}
	if len(allFresh.FullSequences) != 1 || allFresh.FullSequences[0] != 56 {
		t.Fatalf("all-fresh full sequences = %v", allFresh.FullSequences)
	}

	state := receiver.state(StateKey{Device: testDevice, Generation: 1})
	state.mu.Lock()
	top := state.top
	state.mu.Unlock()
	if top != 56 {
		t.Fatalf("top = %d, want 56 after two wraps", top)
	}

	exhaustiveOld := exhaustiveInterpret(material([]uint64{16}), 8, 8, 16, state)
	if len(exhaustiveOld.fresh) != 0 || len(exhaustiveOld.old) != 2 {
		t.Fatalf("exhaustive old = %+v", exhaustiveOld)
	}
	exhaustiveFresh := exhaustiveInterpret(material([]uint64{56}), 8, 8, 16, beforeFresh)
	if len(exhaustiveFresh.fresh) != 1 || len(exhaustiveFresh.old) != 0 || exhaustiveFresh.fresh[0] != 56 {
		t.Fatalf("exhaustive fresh = %+v", exhaustiveFresh)
	}
}

func TestNodeObservationOrderAndSeparateStateConflict(t *testing.T) {
	receiver := newTestReceiver(t, 4)
	logs := []NodeLog{
		{Node: "a", Entries: []LogEntry{{LowSequence: 1, Admitted: true}, {LowSequence: 2, Admitted: true}}},
		{Node: "b", Entries: []LogEntry{{LowSequence: 14, Admitted: true}, {LowSequence: 1, Admitted: true}}},
	}
	handoff := material([]uint64{15}, logs...)

	imported := receiver.Import(handoff)
	if imported.Outcome != OutcomeAccepted {
		t.Fatalf("ordered import outcome = %s, conflicts = %+v", imported.Outcome, imported.Conflicts)
	}

	conflicting := material([]uint64{24},
		NodeLog{Node: "a", Entries: []LogEntry{{LowSequence: 0, Admitted: true}}},
		NodeLog{Node: "b", Entries: []LogEntry{{LowSequence: 0, Admitted: false}}},
	)
	oldCheckpoint := receiver.Import(material([]uint64{23}))
	if oldCheckpoint.Outcome != OutcomeAccepted {
		t.Fatalf("old checkpoint setup outcome = %s", oldCheckpoint.Outcome)
	}
	oldFrame := receiver.Import(material([]uint64{23},
		NodeLog{Node: "old", Entries: []LogEntry{{LowSequence: 0, Admitted: true}}},
	))
	if oldFrame.Outcome != OutcomeAccepted {
		t.Fatalf("old frame setup outcome = %s", oldFrame.Outcome)
	}
	stateSnapshot := snapshotWindow(receiver, testDevice, 1)
	decision := receiver.Admit(frame(0), true, conflicting)
	if decision.Outcome != OutcomeNeedAnchor {
		t.Fatalf("soft state conflict outcome = %s, want need_anchor", decision.Outcome)
	}
	if decision.NeedAnchorLow != 0 || len(decision.Alternatives) != 2 {
		t.Fatalf("soft state conflict decision = %+v", decision)
	}
	if decision.Alternatives[0].CandidateFull != 16 || decision.Alternatives[0].CandidateFresh {
		t.Fatalf("old alternative = %+v", decision.Alternatives[0])
	}
	if decision.Alternatives[1].CandidateFull != 32 || !decision.Alternatives[1].CandidateFresh {
		t.Fatalf("fresh alternative = %+v", decision.Alternatives[1])
	}
	if !stateSnapshot.isOld(16, 8) || stateSnapshot.isOld(32, 8) {
		t.Fatalf("window snapshot old16=%v old32=%v", stateSnapshot.isOld(16, 8), stateSnapshot.isOld(32, 8))
	}

	state := receiver.state(StateKey{Device: testDevice, Generation: 1})
	exhaustive := exhaustiveInterpret(handoff, 4, 8, 16, state)
	if len(exhaustive.assignments) == 0 || len(exhaustive.fresh) != 1 || exhaustive.fresh[0] != 20 {
		t.Fatalf("exhaustive node-order result = %+v", exhaustive)
	}

	hardConflict := material([]uint64{16},
		NodeLog{Node: "a", Entries: []LogEntry{{LowSequence: 0, Admitted: false}}},
	)
	hardDecision := receiver.Admit(Frame{Device: testDevice, Generation: 1, LowSequence: 0}, true, hardConflict)
	if hardDecision.Outcome != OutcomeConflict {
		t.Fatalf("hard conflict outcome = %s, want conflict", hardDecision.Outcome)
	}
	if len(hardDecision.Conflicts) == 0 || hardDecision.Conflicts[0].Code != ConflictNoCompatibleSequence {
		t.Fatalf("hard conflicts = %+v", hardDecision.Conflicts)
	}
}

func TestParallelKeyGenerationsAndConcurrentSameFrame(t *testing.T) {
	receiver := newTestReceiver(t, 4)
	var wait sync.WaitGroup

	for generation := KeyGeneration(1); generation <= 8; generation++ {
		wait.Add(1)
		go func(generation KeyGeneration) {
			defer wait.Done()
			handoff := HandoffMaterial{
				Device:      testDevice,
				Generation:  generation,
				Checkpoints: []Checkpoint{checkpoint(16)},
			}
			decision := receiver.Admit(Frame{Device: testDevice, Generation: generation, LowSequence: 1}, true, handoff)
			if decision.Outcome != OutcomeAccepted {
				t.Errorf("generation %d outcome = %s", generation, decision.Outcome)
			}
		}(generation)
	}
	wait.Wait()

	handoff := material([]uint64{16})
	var start sync.WaitGroup
	var done sync.WaitGroup
	accepted := 0
	rejected := 0
	var resultMu sync.Mutex

	for i := 0; i < 64; i++ {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			decision := receiver.Admit(frame(2), true, handoff)
			resultMu.Lock()
			defer resultMu.Unlock()
			if decision.Outcome == OutcomeAccepted {
				accepted++
			}
			if decision.Outcome == OutcomeRejected {
				rejected++
			}
		}()
	}
	start.Add(1)
	start.Done()
	done.Wait()

	if accepted != 1 || rejected != 63 {
		t.Fatalf("same-frame races accepted=%d rejected=%d", accepted, rejected)
	}

	for generation := KeyGeneration(1); generation <= 8; generation++ {
		state := receiver.state(StateKey{Device: testDevice, Generation: generation})
		state.mu.Lock()
		top := state.top
		state.mu.Unlock()
		wantTop := uint64(17)
		if generation == 1 {
			wantTop = 18
		}
		if top != wantTop {
			t.Fatalf("generation %d top = %d, want %d", generation, top, wantTop)
		}
	}
}

func TestInvalidAuthenticationHasNoState(t *testing.T) {
	receiver := newTestReceiver(t, 4)
	decision := receiver.Admit(frame(3), false, material([]uint64{16}))
	if decision.Outcome != OutcomeInvalidAuthentication {
		t.Fatalf("invalid authentication outcome = %s", decision.Outcome)
	}

	state := receiver.state(StateKey{Device: testDevice, Generation: 1})
	state.mu.Lock()
	hasTop := state.hasTop
	seenCount := len(state.seen)
	state.mu.Unlock()
	if hasTop || seenCount != 0 {
		t.Fatalf("invalid authentication created state: hasTop=%v seen=%d", hasTop, seenCount)
	}
}

func TestBadSignatureAndImportAmbiguityDoNotMutateState(t *testing.T) {
	receiver, err := New(Config{Bits: 4, Verifier: func(device DeviceID, generation KeyGeneration, fullSequence uint64, signature []byte) bool {
		return string(signature) == "valid"
	}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	bad := receiver.Import(HandoffMaterial{
		Device:      testDevice,
		Generation:  2,
		Checkpoints: []Checkpoint{{FullSequence: 16, Signature: []byte("invalid")}},
	})
	if bad.Outcome != OutcomeConflict || len(bad.Conflicts) != 1 || bad.Conflicts[0].Code != ConflictBadSignature {
		t.Fatalf("bad signature decision = %+v", bad)
	}

	state := receiver.state(StateKey{Device: testDevice, Generation: 2})
	state.mu.Lock()
	hasTop := state.hasTop
	seenCount := len(state.seen)
	state.mu.Unlock()
	if hasTop || seenCount != 0 {
		t.Fatalf("bad signature created state: hasTop=%v seen=%d", hasTop, seenCount)
	}

	ambiguous := receiver.Import(HandoffMaterial{
		Device:      testDevice,
		Generation:  2,
		Checkpoints: []Checkpoint{{FullSequence: 24, Signature: []byte("valid")}},
		NodeLogs: []NodeLog{
			{Node: "a", Entries: []LogEntry{{LowSequence: 0, Admitted: true}}},
		},
	})
	if ambiguous.Outcome != OutcomeNeedAnchor {
		t.Fatalf("ambiguous import outcome = %s", ambiguous.Outcome)
	}

	state.mu.Lock()
	hasTop = state.hasTop
	seenCount = len(state.seen)
	state.mu.Unlock()
	if hasTop || seenCount != 0 {
		t.Fatalf("ambiguous import created state: hasTop=%v seen=%d", hasTop, seenCount)
	}
}

func TestThirtyTwoBitHalfWindowBoundary(t *testing.T) {
	receiver := newTestReceiver(t, 32)
	anchor := uint64(1) << 32
	first := receiver.Admit(Frame{Device: testDevice, Generation: 9, LowSequence: 0}, true, HandoffMaterial{
		Device:      testDevice,
		Generation:  9,
		Checkpoints: []Checkpoint{checkpoint(anchor)},
	})
	if first.Outcome != OutcomeAccepted || len(first.FullSequences) != 1 || first.FullSequences[0] != anchor {
		t.Fatalf("32-bit first decision = %+v", first)
	}

	decision := receiver.Admit(Frame{Device: testDevice, Generation: 9, LowSequence: 1 << 31}, true, HandoffMaterial{
		Device:      testDevice,
		Generation:  9,
		Checkpoints: []Checkpoint{checkpoint(anchor)},
	})
	if decision.Outcome != OutcomeNeedAnchor || len(decision.Alternatives) != 2 {
		t.Fatalf("32-bit boundary decision = %+v", decision)
	}
	if decision.Alternatives[0].CandidateFull != 1<<31 || decision.Alternatives[1].CandidateFull != anchor+(1<<31) {
		t.Fatalf("32-bit alternatives = %+v", decision.Alternatives)
	}
}

func TestThirtyTwoBitCheckpointNearMaximumDoesNotOverflow(t *testing.T) {
	receiver := newTestReceiver(t, 32)
	anchor := ^uint64(0) - 3
	decision := receiver.Admit(Frame{Device: testDevice, Generation: 10, LowSequence: 0xfffffff8}, true, HandoffMaterial{
		Device:      testDevice,
		Generation:  10,
		Checkpoints: []Checkpoint{checkpoint(anchor)},
	})
	if decision.Outcome != OutcomeAccepted {
		t.Fatalf("near-maximum decision = %+v", decision)
	}
	want := anchor - 4
	if len(decision.FullSequences) != 1 || decision.FullSequences[0] != want {
		t.Fatalf("near-maximum full sequences = %v, want %d", decision.FullSequences, want)
	}
}
