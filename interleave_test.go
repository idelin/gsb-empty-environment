package netsec

import (
	"errors"
	"testing"
)

func TestShortestTornExecutionWitness(t *testing.T) {
	old := makeUnit(t, "u", 1, 0, "A")
	newer := makeUnit(t, "u", 2, 1, "B")
	staged, err := NewStagedPublication(old, newer, 0x10)
	if err != nil {
		t.Fatal(err)
	}
	w, err := staged.InitialWorld("auditor").ShortestTornWitness(Explorer{})
	if err != nil {
		t.Fatalf("ShortestTornWitness: %v", err)
	}
	if len(w.Steps) != 6 {
		t.Fatalf("witness length = %d, want 6:\n%s", len(w.Steps), w)
	}
	t.Logf("shortest torn-execution witness:\n%s", w)

	var publishes, enters, stackLookups, exceptionLookups, callLookups, executes int
	for _, step := range w.Steps {
		switch step.Op {
		case OpPublishCode:
			publishes++
		case OpPublishStackMap, OpPublishExceptionTable, OpPublishCallTargets:
			t.Fatalf("shortest witness unexpectedly published metadata before executing:\n%s", w)
		case OpEnter:
			enters++
		case OpLookupStackMap:
			stackLookups++
		case OpLookupExceptionTable:
			exceptionLookups++
		case OpLookupCallTarget:
			callLookups++
		case OpExecute:
			executes++
		default:
			t.Fatalf("unexpected step %s", step)
		}
	}
	if publishes != 1 || enters != 1 || stackLookups != 1 || exceptionLookups != 1 || callLookups != 1 || executes != 1 {
		t.Fatalf("witness shape = publish %d enter %d stack %d exc %d call %d execute %d",
			publishes, enters, stackLookups, exceptionLookups, callLookups, executes)
	}

	thread, ok := w.Final.TornThread()
	if !ok {
		t.Fatalf("final state is not torn but successful:\n%s", w)
	}
	if thread.Code.ContentRoot() != newer.ContentRoot() {
		t.Fatal("witness did not execute new code")
	}
	if thread.Stack.Generation != old.Generation() || thread.Stack.Digest != old.Digests().StackMap {
		t.Fatal("witness did not use old stack map")
	}
	if thread.Exception.Generation != old.Generation() || thread.Exception.Digest != old.Digests().ExceptionTable {
		t.Fatal("witness did not use old exception table")
	}
	if thread.CallTarget.Generation != old.Generation() || thread.CallTarget.Digest != old.Digests().CallTargets {
		t.Fatal("witness did not use old call targets")
	}
	if thread.Code.Digests().Code != newer.Digests().Code {
		t.Fatal("witness code digest is not the new code digest")
	}
}

func TestWitnessIsShortestAndReplayable(t *testing.T) {
	old := makeUnit(t, "u", 1, 0, "A")
	newer := makeUnit(t, "u", 2, 1, "B")
	staged, err := NewStagedPublication(old, newer, 0x10)
	if err != nil {
		t.Fatal(err)
	}
	start := staged.InitialWorld("auditor")
	if _, err := start.ShortestTornWitness(Explorer{MaxDepth: 5}); !errors.Is(err, ErrWitnessNotFound) {
		t.Fatalf("depth-5 search error = %v, want ErrWitnessNotFound", err)
	}
	w, err := start.ShortestTornWitness(Explorer{})
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := start.Replay(w.Steps)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !replayed.TornGoal() {
		t.Fatal("replayed witness does not reach the torn goal")
	}
	if replayed.Key() != w.Final.Key() {
		t.Fatal("replay produced a different final state")
	}
}

func TestNewThreadJoiningMidPublicationHasTornWitness(t *testing.T) {
	old := makeUnit(t, "u", 1, 0, "A")
	newer := makeUnit(t, "u", 2, 1, "B")
	staged, err := NewStagedPublication(old, newer, 0x10)
	if err != nil {
		t.Fatal(err)
	}
	w := staged.InitialWorld("placeholder")
	delete(w.Threads, "placeholder")
	w.AddUnspawnedThread("late")

	witness, err := w.ShortestTornWitness(Explorer{})
	if err != nil {
		t.Fatalf("ShortestTornWitness: %v", err)
	}
	if len(witness.Steps) != 7 {
		t.Fatalf("witness length = %d, want 7:\n%s", len(witness.Steps), witness)
	}
	var spawned bool
	for _, step := range witness.Steps {
		if step.Op == OpSpawnThread && step.Thread == "late" {
			spawned = true
		}
	}
	if !spawned {
		t.Fatalf("witness did not join the late thread:\n%s", witness)
	}
	if thread, ok := witness.Final.TornThread(); !ok || thread.ID != "late" {
		t.Fatalf("torn thread = %+v", thread)
	}
}

func TestStagedPublicationRequiresOldMetadataCoverage(t *testing.T) {
	old := makeUnit(t, "u", 1, 0, "A")
	newer := makeUnit(t, "u", 2, 1, "B")
	if _, err := NewStagedPublication(old, newer, 0x30); !errors.Is(err, ErrInvalidUnit) {
		t.Fatalf("uncovered PC error = %v, want ErrInvalidUnit", err)
	}
}
