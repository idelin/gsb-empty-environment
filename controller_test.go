package timetravel

import (
	"fmt"
	"reflect"
	"testing"
)

// Acceptance scenario:
//  1. perform 3 consecutive user state modifications
//  2. Undo 2 steps
//  3. verify every Signal and every associated Computed is restored
//     exactly to the values of the first modification
//  4. Redo 1 step and verify the state matches the second modification
//     with zero drift
func TestAcceptance_TimeTravel(t *testing.T) {
	ctrl := NewController()

	count := NewSignal(ctrl, 0)
	name := NewSignal(ctrl, "init")

	doubled := NewComputed(ctrl, func() int { return count.Get() * 2 })
	label := NewComputed(ctrl, func() string {
		return fmt.Sprintf("%s:%d", name.Get(), count.Get())
	})

	type state struct {
		count   int
		name    string
		doubled int
		label   string
	}
	capture := func() state {
		return state{count.Get(), name.Get(), doubled.Get(), label.Get()}
	}

	// Step 1: three consecutive user modifications.
	count.Set(1)      // version 1
	name.Set("alice") // version 2
	count.Set(2)      // version 3

	step1 := state{1, "init", 2, "init:1"}
	step2 := state{1, "alice", 2, "alice:1"}
	step3 := state{2, "alice", 4, "alice:2"}

	if got := capture(); got != step3 {
		t.Fatalf("after 3 modifications: got %+v, want %+v", got, step3)
	}
	if v := ctrl.Version(); v != 3 {
		t.Fatalf("version = %d, want 3", v)
	}

	// Step 2: undo 2 steps -> back to the first modification.
	if !ctrl.Undo() || !ctrl.Undo() {
		t.Fatal("Undo returned false, want true")
	}

	// Step 3: all signals and computeds must equal step-1 values exactly.
	if got := capture(); got != step1 {
		t.Fatalf("after Undo x2: got %+v, want %+v", got, step1)
	}
	if v := ctrl.Version(); v != 1 {
		t.Fatalf("version after Undo x2 = %d, want 1", v)
	}

	// Step 4: redo 1 step -> exactly the second modification, no drift.
	if !ctrl.Redo() {
		t.Fatal("Redo returned false, want true")
	}
	if got := capture(); got != step2 {
		t.Fatalf("after Redo x1: got %+v, want %+v", got, step2)
	}
	if v := ctrl.Version(); v != 2 {
		t.Fatalf("version after Redo = %d, want 2", v)
	}

	// Redo once more to confirm the full forward chain is intact.
	if !ctrl.Redo() {
		t.Fatal("second Redo returned false, want true")
	}
	if got := capture(); got != step3 {
		t.Fatalf("after Redo x2: got %+v, want %+v", got, step3)
	}
}

// Downstream consumers must be re-notified on time travel so the UI
// re-renders in sync with the restored version.
func TestRerenderSyncOnTimeTravel(t *testing.T) {
	ctrl := NewController()
	count := NewSignal(ctrl, 0)
	doubled := NewComputed(ctrl, func() int { return count.Get() * 2 })

	var versions []int
	ctrl.OnChange(func(v int) { versions = append(versions, v) })

	var signalSeen []int
	count.Subscribe(func(v int) { signalSeen = append(signalSeen, v) })

	var computedSeen []int
	doubled.Subscribe(func(v int) { computedSeen = append(computedSeen, v) })

	count.Set(1)
	count.Set(2)
	count.Set(3)
	ctrl.Undo()
	ctrl.Undo()
	ctrl.Redo()

	wantVersions := []int{1, 2, 3, 2, 1, 2}
	if !reflect.DeepEqual(versions, wantVersions) {
		t.Fatalf("OnChange versions = %v, want %v", versions, wantVersions)
	}

	wantSignal := []int{1, 2, 3, 2, 1, 2}
	if !reflect.DeepEqual(signalSeen, wantSignal) {
		t.Fatalf("signal notifications = %v, want %v", signalSeen, wantSignal)
	}

	wantComputed := []int{2, 4, 6, 4, 2, 4}
	if !reflect.DeepEqual(computedSeen, wantComputed) {
		t.Fatalf("computed notifications = %v, want %v", computedSeen, wantComputed)
	}
}

func TestGoToArbitraryVersion(t *testing.T) {
	ctrl := NewController()
	a := NewSignal(ctrl, "a0")
	b := NewSignal(ctrl, 0)

	a.Set("a1") // v1
	b.Set(1)    // v2
	a.Set("a2") // v3
	b.Set(2)    // v4

	if !ctrl.GoTo(0) {
		t.Fatal("GoTo(0) failed")
	}
	if a.Get() != "a0" || b.Get() != 0 {
		t.Fatalf("at v0: got (%q, %d), want (a0, 0)", a.Get(), b.Get())
	}

	if !ctrl.GoTo(3) {
		t.Fatal("GoTo(3) failed")
	}
	if a.Get() != "a2" || b.Get() != 1 {
		t.Fatalf("at v3: got (%q, %d), want (a2, 1)", a.Get(), b.Get())
	}

	if ctrl.GoTo(99) {
		t.Fatal("GoTo(99) succeeded, want failure")
	}
	if ctrl.GoTo(-1) {
		t.Fatal("GoTo(-1) succeeded, want failure")
	}
	if ctrl.GoTo(3) {
		t.Fatal("GoTo(current) succeeded, want no-op failure")
	}
}

func TestRedoBranchTruncatedOnNewChange(t *testing.T) {
	ctrl := NewController()
	count := NewSignal(ctrl, 0)

	count.Set(1)
	count.Set(2)
	count.Set(3)
	ctrl.Undo()
	ctrl.Undo() // at v1

	count.Set(10) // new branch, v2 becomes 10

	if ctrl.CanRedo() {
		t.Fatal("CanRedo = true after new commit on truncated branch")
	}
	if ctrl.Redo() {
		t.Fatal("Redo succeeded after truncation")
	}
	if got := count.Get(); got != 10 {
		t.Fatalf("count = %d, want 10", got)
	}

	ctrl.Undo()
	if got := count.Get(); got != 1 {
		t.Fatalf("after Undo count = %d, want 1", got)
	}
	ctrl.Redo()
	if got := count.Get(); got != 10 {
		t.Fatalf("after Redo count = %d, want 10", got)
	}
}

func TestBatchCommitsSingleVersion(t *testing.T) {
	ctrl := NewController()
	x := NewSignal(ctrl, 0)
	y := NewSignal(ctrl, 0)
	sum := NewComputed(ctrl, func() int { return x.Get() + y.Get() })

	var versions []int
	ctrl.OnChange(func(v int) { versions = append(versions, v) })

	ctrl.Batch(func() {
		x.Set(1)
		y.Set(2)
	})

	if v := ctrl.Version(); v != 1 {
		t.Fatalf("version = %d, want 1 (batch = one commit)", v)
	}
	if !reflect.DeepEqual(versions, []int{1}) {
		t.Fatalf("OnChange versions = %v, want [1]", versions)
	}
	if got := sum.Get(); got != 3 {
		t.Fatalf("sum = %d, want 3", got)
	}

	ctrl.Undo()
	if x.Get() != 0 || y.Get() != 0 || sum.Get() != 0 {
		t.Fatalf("after Undo: got (%d, %d, %d), want (0, 0, 0)",
			x.Get(), y.Get(), sum.Get())
	}
}

func TestBoundaries(t *testing.T) {
	ctrl := NewController()
	count := NewSignal(ctrl, 0)

	if ctrl.CanUndo() || ctrl.CanRedo() {
		t.Fatal("fresh controller should not allow undo/redo")
	}
	if ctrl.Undo() || ctrl.Redo() {
		t.Fatal("undo/redo on empty history should fail")
	}

	count.Set(1)
	if !ctrl.CanUndo() || ctrl.CanRedo() {
		t.Fatal("after one commit: CanUndo=true, CanRedo=false expected")
	}

	ctrl.Undo()
	if ctrl.CanUndo() || !ctrl.CanRedo() {
		t.Fatal("at baseline: CanUndo=false, CanRedo=true expected")
	}
	if ctrl.Undo() {
		t.Fatal("Undo past baseline succeeded")
	}
	if got := count.Get(); got != 0 {
		t.Fatalf("count = %d after failed Undo, want 0", got)
	}
}

// Snapshots must be immutable: later mutations must not rewrite history.
func TestSnapshotImmutability(t *testing.T) {
	type Point struct{ X, Y int }

	ctrl := NewController()
	p := NewSignal(ctrl, Point{0, 0})

	p.Set(Point{1, 1}) // v1
	p.Set(Point{2, 2}) // v2

	ctrl.GoTo(1)
	if got := p.Get(); got != (Point{1, 1}) {
		t.Fatalf("v1 = %+v, want {1 1}", got)
	}
	ctrl.GoTo(0)
	if got := p.Get(); got != (Point{0, 0}) {
		t.Fatalf("v0 = %+v, want {0 0}", got)
	}
	ctrl.GoTo(2)
	if got := p.Get(); got != (Point{2, 2}) {
		t.Fatalf("v2 = %+v, want {2 2}", got)
	}
}

func TestUnsubscribe(t *testing.T) {
	ctrl := NewController()
	count := NewSignal(ctrl, 0)

	calls := 0
	unsub := count.Subscribe(func(int) { calls++ })
	count.Set(1)
	unsub()
	unsub() // idempotent
	count.Set(2)

	if calls != 1 {
		t.Fatalf("subscriber called %d times, want 1", calls)
	}

	globalCalls := 0
	unsubAll := ctrl.OnChange(func(int) { globalCalls++ })
	count.Set(3)
	unsubAll()
	count.Set(4)
	if globalCalls != 1 {
		t.Fatalf("OnChange called %d times, want 1", globalCalls)
	}
}

// A signal registered after history exists must still restore correctly
// when travelling to versions that predate it (backfilled baseline).
func TestLateRegisteredSignal(t *testing.T) {
	ctrl := NewController()
	a := NewSignal(ctrl, "a")
	a.Set("a1") // v1

	b := NewSignal(ctrl, 100) // registered after v1
	b.Set(200)                // v2

	ctrl.GoTo(1)
	if a.Get() != "a1" || b.Get() != 100 {
		t.Fatalf("at v1: got (%q, %d), want (a1, 100)", a.Get(), b.Get())
	}
	ctrl.GoTo(0)
	if a.Get() != "a" || b.Get() != 100 {
		t.Fatalf("at v0: got (%q, %d), want (a, 100)", a.Get(), b.Get())
	}
}
