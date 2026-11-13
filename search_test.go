package netsec

import (
	"reflect"
	"testing"
)

func TestShortestWitnessAndTieBreak(t *testing.T) {
	p := NewProgram(
		[]Event{
			Enter(),
			LoadRoot(),
			Exit(),
			Enter(),
			Dereference(),
		},
		[]Event{
			ReplaceRoot(1),
			RetireObject(1),
			TryAdvance(),
			TryAdvance(),
		},
	)

	w, err := FindShortestWitness(p)
	if err != nil {
		t.Fatalf("FindShortestWitness: %v", err)
	}
	if w == nil {
		t.Fatal("expected a use-after-free witness")
	}
	wantActors := []int{0, 1, 0, 0, 1, 1, 1, 0, 0}
	if got := w.Actors(); !reflect.DeepEqual(got, wantActors) {
		t.Fatalf("actors = %v, want %v\n%s", got, wantActors, w)
	}
	if w.Length() != len(wantActors) {
		t.Fatalf("length = %d, want %d", w.Length(), len(wantActors))
	}
	wantKinds := []EventKind{
		EventEnter, EventReplaceRoot, EventLoadRoot, EventExit, EventRetire,
		EventTryAdvance, EventTryAdvance, EventEnter, EventDereference,
	}
	gotKinds := make([]EventKind, len(w.Steps))
	for i, step := range w.Steps {
		gotKinds[i] = step.Event.Kind
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Fatalf("event kinds = %v, want %v\n%s", gotKinds, wantKinds, w)
	}
	if w.Violation.Ref != (ObjectRef{ID: 1, Gen: 0}) {
		t.Fatalf("violation ref = %s, want 1@g0", w.Violation.Ref)
	}
	if got := w.Steps[6].Reclaimed; !reflect.DeepEqual(got, []ObjectRef{{ID: 1, Gen: 0}}) {
		t.Fatalf("reclamation at second advance = %v", got)
	}
}

func TestNoWitnessForCorrectSleepingActiveReader(t *testing.T) {
	// The reader sleeps while still active. Its published epoch is stale after
	// the first advance, so the second advance is blocked and the generation
	// never reaches two safe advances.
	p := NewProgram(
		[]Event{Enter(), LoadRoot(), Dereference()},
		[]Event{
			ReplaceRoot(2),
			RetireObject(2),
			TryAdvance(),
			TryAdvance(),
			TryAdvance(),
			TryAdvance(),
		},
	)
	w, err := FindShortestWitness(p)
	if err != nil {
		t.Fatalf("FindShortestWitness: %v", err)
	}
	if w != nil {
		t.Fatalf("correct model should not reclaim while the reader is active:\n%s", w)
	}
}

func TestWitnessRequiresActiveDereference(t *testing.T) {
	p := NewProgram(
		[]Event{Enter(), LoadRoot(), Exit(), Dereference()},
		[]Event{
			ReplaceRoot(3),
			RetireObject(3),
			TryAdvance(),
			TryAdvance(),
		},
	)
	w, err := FindShortestWitness(p)
	if err != nil {
		t.Fatalf("FindShortestWitness: %v", err)
	}
	if w != nil {
		t.Fatalf("inactive Dereference should be rejected, not reported as UAF:\n%s", w)
	}
}

func TestLexicographicTieUsesLowerThread(t *testing.T) {
	// Both threads can become stale readers. The shortest witness must use
	// thread 0 because thread numbers break equal-length ties.
	p := NewProgram(
		[]Event{Enter(), LoadRoot(), Exit(), Enter(), Dereference()},
		[]Event{Enter(), LoadRoot(), Exit(), Enter(), Dereference()},
		[]Event{
			ReplaceRoot(5),
			RetireObject(5),
			TryAdvance(),
			TryAdvance(),
		},
	)
	w, err := FindShortestWitness(p)
	if err != nil {
		t.Fatalf("FindShortestWitness: %v", err)
	}
	if w == nil {
		t.Fatal("expected a witness")
	}
	last := w.Steps[len(w.Steps)-1]
	if last.Thread != 0 || last.Event.Kind != EventDereference {
		t.Fatalf("tie-break selected thread %d, want thread 0\n%s", last.Thread, w)
	}
}

func TestQuiescentAsExitDiagnosticWitness(t *testing.T) {
	p := NewProgram(
		[]Event{
			Enter(),
			LoadRoot(),
			Quiescent(),
			Enter(),
			Dereference(),
		},
		[]Event{
			ReplaceRoot(9),
			RetireObject(9),
			TryAdvance(),
			TryAdvance(),
		},
	)

	// The correct model rejects Quiescent while active, so this script cannot
	// reach a reclamation followed by a dereference.
	w, err := FindShortestWitness(p)
	if err != nil {
		t.Fatalf("FindShortestWitness: %v", err)
	}
	if w != nil {
		t.Fatalf("correct model accepted Quiescent as Exit:\n%s", w)
	}

	// The diagnostic mode models the unsafe pending implementation.
	opts := DefaultOptions()
	opts.TreatQuiescentAsExit = true
	w, err = FindShortestWitnessWithOptions(p, opts)
	if err != nil {
		t.Fatalf("FindShortestWitnessWithOptions: %v", err)
	}
	if w == nil {
		t.Fatal("diagnostic mode should expose the Quiescent-as-Exit use-after-free")
	}
	if w.Violation.Ref != (ObjectRef{ID: 9, Gen: 0}) {
		t.Fatalf("violation = %+v", w.Violation)
	}
}

func TestProgramBounds(t *testing.T) {
	tooManyThreads := make([][]Event, MaxThreads+1)
	p := Program{Threads: tooManyThreads}
	if err := p.Validate(); err == nil {
		t.Fatal("expected too many threads to be rejected")
	}

	script := make([]Event, MaxEventsPerThread+1)
	for i := range script {
		script[i] = Enter()
	}
	p = Program{Threads: [][]Event{script}}
	if err := p.Validate(); err == nil {
		t.Fatal("expected too many events to be rejected")
	}
}
