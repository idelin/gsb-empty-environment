package desim

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// ledger is the user State for undo tests: a balance plus an op log.
type ledger struct {
	balance int
	ops     []string
}

type ledgerSnap struct {
	balance int
	ops     []string
}

func (l *ledger) Snapshot() interface{} {
	return ledgerSnap{l.balance, append([]string(nil), l.ops...)}
}

func (l *ledger) Restore(s interface{}) {
	snap := s.(ledgerSnap)
	l.balance = snap.balance
	l.ops = append([]string(nil), snap.ops...)
}

// chainIDs collects event ids assigned while handlers execute.
type chainIDs struct{ a, b, c, d EventID }

// buildChain creates A(t=1,+100) -> B(t=2,+50) -> C(t=3,-20) plus an
// independent root D(t=2,phase1,+7). Successor ids are only known once
// the parent executes, so they are filled into the returned struct.
func buildChain(led *ledger, eng *Engine) *chainIDs {
	ids := &chainIDs{}
	ids.a, _ = eng.Schedule("A:+100", 1, 0, func(ctx *Context) {
		led.balance += 100
		led.ops = append(led.ops, "A")
		ids.b, _ = ctx.Schedule("B:+50", 1, 0, func(ctx *Context) {
			led.balance += 50
			led.ops = append(led.ops, "B")
			ids.c, _ = ctx.Schedule("C:-20", 1, 0, func(ctx *Context) {
				led.balance -= 20
				led.ops = append(led.ops, "C")
			})
		})
	})
	ids.d, _ = eng.Schedule("D:+7", 2, 1, func(ctx *Context) {
		led.balance += 7
		led.ops = append(led.ops, "D")
	})
	return ids
}

func runWithCheckpoints(eng *Engine, t *testing.T) {
	for eng.Step() {
		if err := eng.Checkpoint(); err != nil {
			t.Fatal(err)
		}
	}
}

func checkResim(t *testing.T, eng *Engine, led *ledger) {
	fresh, err := eng.Resimulate()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fresh, led.Snapshot()) {
		t.Fatalf("from-scratch resimulation %v != live state %v", fresh, led.Snapshot())
	}
}

func TestUndoCausalSuccessors(t *testing.T) {
	led := &ledger{}
	eng := New(led)
	ids := buildChain(led, eng)
	runWithCheckpoints(eng, t)
	a, b, c, d := ids.a, ids.b, ids.c, ids.d

	if led.balance != 137 {
		t.Fatalf("balance %d, want 137", led.balance)
	}
	if want := []string{"A", "B", "D", "C"}; !reflect.DeepEqual(led.ops, want) {
		t.Fatalf("ops %v, want %v", led.ops, want)
	}

	// Causal chain of A is the whole family; of B just B and C.
	if got := eng.CausalChain(a); len(got) != 3 {
		t.Fatalf("causal chain of A has %d events, want 3", len(got))
	}
	chain := eng.CausalChain(b)
	if len(chain) != 2 || chain[0].ID != b || chain[1].ID != c {
		t.Fatalf("causal chain of B wrong: %v", chain)
	}

	// Undo B: B and C must be undone; replay starts at the checkpoint
	// right after A executed (history length 1).
	rep, err := eng.Undo(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Undone) != 2 || rep.Undone[0].ID != b || rep.Undone[1].ID != c {
		t.Fatalf("undone set wrong: %v", rep.Undone)
	}
	if rep.CheckpointLen != 1 {
		t.Fatalf("replay started at history length %d, want 1", rep.CheckpointLen)
	}
	if led.balance != 107 {
		t.Fatalf("balance after undo %d, want 107", led.balance)
	}
	if want := []string{"A", "D"}; !reflect.DeepEqual(led.ops, want) {
		t.Fatalf("ops after undo %v, want %v", led.ops, want)
	}
	joined := strings.Join(rep.ReplayLog, "\n")
	for _, want := range []string{"SKIP", "REPLAY", "B:+50", "C:-20", "D:+7"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("replay log missing %q:\n%s", want, joined)
		}
	}
	if !eng.Event(b).Undone() || !eng.Event(c).Undone() {
		t.Fatal("B and C should be marked undone")
	}
	checkResim(t, eng, led)

	// Undo A as well: only D survives.
	rep2, err := eng.Undo(a)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.CheckpointLen != 0 {
		t.Fatalf("undo of root should replay from genesis, got %d", rep2.CheckpointLen)
	}
	if led.balance != 7 {
		t.Fatalf("balance after undoing A %d, want 7", led.balance)
	}
	if want := []string{"D"}; !reflect.DeepEqual(led.ops, want) {
		t.Fatalf("ops %v, want %v", led.ops, want)
	}
	checkResim(t, eng, led)

	// Engine remains usable: schedule more work at the current time.
	if _, err := eng.Schedule("E:+1", eng.Now(), eng.Phase(), func(ctx *Context) {
		led.balance++
		led.ops = append(led.ops, "E")
	}); err != nil {
		t.Fatal(err)
	}
	eng.Run()
	if led.balance != 8 {
		t.Fatalf("balance %d, want 8", led.balance)
	}
	checkResim(t, eng, led)
	_ = d
}

func TestUndoExternalCompensation(t *testing.T) {
	led := &ledger{}
	eng := New(led)
	var ext EventID
	root, _ := eng.Schedule("root:+10", 1, 0, func(ctx *Context) {
		led.balance += 10
		led.ops = append(led.ops, "root")
		ctx.Schedule("wire:+1000", 1, 0, func(ctx *Context) {
			led.balance += 1000
			led.ops = append(led.ops, "wire")
		}, External())
		ctx.Schedule("fee:-3", 2, 0, func(ctx *Context) {
			led.balance -= 3
			led.ops = append(led.ops, "fee")
		})
	})
	runWithCheckpoints(eng, t)
	if led.balance != 1007 {
		t.Fatalf("balance %d, want 1007", led.balance)
	}
	ext = eng.CausalChain(root)[1].ID

	// Undo the root: the external wire transfer cannot be undone; it
	// yields a compensation requirement and its effect stays.
	rep, err := eng.Undo(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Compensations) != 1 || rep.Compensations[0].Event.ID != ext {
		t.Fatalf("compensations wrong: %v", rep.Compensations)
	}
	if len(rep.Undone) != 2 { // root and fee
		t.Fatalf("undone should be root+fee: %v", rep.Undone)
	}
	if led.balance != 1000 {
		t.Fatalf("external effect must remain: balance %d, want 1000", led.balance)
	}
	if want := []string{"wire"}; !reflect.DeepEqual(led.ops, want) {
		t.Fatalf("ops %v, want %v", led.ops, want)
	}
	if len(eng.Compensations()) != 1 {
		t.Fatalf("engine compensation list: %v", eng.Compensations())
	}
	if !strings.Contains(strings.Join(rep.ReplayLog, "\n"), "COMPENSATE") {
		t.Fatalf("replay log should record the compensation:\n%v", rep.ReplayLog)
	}
	checkResim(t, eng, led)

	// Undoing an external event directly also only compensates.
	led2 := &ledger{}
	eng2 := New(led2)
	direct, _ := eng2.Schedule("wire:+5", 1, 0, func(ctx *Context) {
		led2.balance += 5
	}, External())
	runWithCheckpoints(eng2, t)
	rep2, err := eng2.Undo(direct)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep2.Undone) != 0 || len(rep2.Compensations) != 1 {
		t.Fatalf("external undo should only compensate: %v", rep2)
	}
	if led2.balance != 5 {
		t.Fatalf("external effect undone! balance %d, want 5", led2.balance)
	}
	checkResim(t, eng2, led2)
}

func TestUndoPendingDescendants(t *testing.T) {
	led := &ledger{}
	eng := New(led)
	ids := buildChain(led, eng)
	eng.Step() // only A executed; B and D still pending
	a, b := ids.a, ids.b

	rep, err := eng.Undo(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Undone) != 1 || rep.Undone[0].ID != a {
		t.Fatalf("undone: %v", rep.Undone)
	}
	if len(rep.CancelledPending) != 1 || rep.CancelledPending[0].ID != b {
		t.Fatalf("pending successor B should be cancelled: %v", rep.CancelledPending)
	}
	if led.balance != 0 {
		t.Fatalf("balance %d, want 0 after undoing A", led.balance)
	}
	eng.Run() // only D remains
	if led.balance != 7 {
		t.Fatalf("balance %d, want 7 (only D)", led.balance)
	}
	if eng.Event(b).Cancelled() != true {
		t.Fatal("B should be cancelled")
	}
	checkResim(t, eng, led)
}

func TestUndoErrors(t *testing.T) {
	led := &ledger{}
	eng := New(led)
	ids := buildChain(led, eng)
	a := ids.a

	if _, err := eng.Undo(9999); err == nil {
		t.Fatal("undo of unknown event should fail")
	}
	if _, err := eng.Undo(a); err == nil {
		t.Fatal("undo of a pending event should fail (use Cancel)")
	}
	eng.Run()
	if _, err := eng.Undo(a); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Undo(a); err == nil {
		t.Fatal("double undo should fail")
	}
}

func TestUndoNearestCheckpoint(t *testing.T) {
	led := &ledger{}
	eng := New(led)
	var ids []EventID
	for i := 0; i < 6; i++ {
		i := i
		id, _ := eng.Schedule("e", int64(i+1), 0, func(ctx *Context) {
			led.balance += 1 << uint(i)
			led.ops = append(led.ops, string(rune('0'+i)))
		})
		ids = append(ids, id)
	}
	// Checkpoints only after the 1st and 4th events.
	for eng.Step() {
		n := len(eng.History())
		if n == 1 || n == 4 {
			if err := eng.Checkpoint(); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Undo the 5th event (exec index 4): nearest usable checkpoint is
	// the one at history length 4.
	rep, err := eng.Undo(ids[4])
	if err != nil {
		t.Fatal(err)
	}
	if rep.CheckpointLen != 4 {
		t.Fatalf("used checkpoint at history length %d, want 4", rep.CheckpointLen)
	}
	want := 1 + 2 + 4 + 8 + 32 // events 0..3 and 5 remain
	if led.balance != want {
		t.Fatalf("balance %d, want %d", led.balance, want)
	}
	checkResim(t, eng, led)
}

// TestUndoRandomizedFromScratch runs randomized scenarios with
// checkpoints, external events and cancellations, then undoes random
// executed events and cross-checks the live state against a full
// from-scratch resimulation after every undo.
func TestUndoRandomizedFromScratch(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 30; trial++ {
		led := &ledger{}
		eng := New(led)
		var allIDs []EventID

		var mkHandler func(depth int) Handler
		mkHandler = func(depth int) Handler {
			amount := rng.Intn(200) - 100
			external := rng.Intn(6) == 0
			kids := rng.Intn(3)
			return func(ctx *Context) {
				led.balance += amount
				led.ops = append(led.ops, "x")
				if depth >= 3 {
					return
				}
				for i := 0; i < kids; i++ {
					delay := int64(rng.Intn(3))
					phase := rng.Intn(3)
					if delay == 0 {
						phase = ctx.Event().Key.Phase + rng.Intn(2)
					}
					var opts []Option
					if external && i == 0 {
						opts = append(opts, External())
					}
					id, err := ctx.Schedule("k", delay, phase, mkHandler(depth+1), opts...)
					if err == nil && id != 0 {
						allIDs = append(allIDs, id)
					}
				}
			}
		}
		for i := 0; i < 4+rng.Intn(6); i++ {
			id, err := eng.Schedule("r", int64(rng.Intn(10)), rng.Intn(3), mkHandler(0))
			if err != nil {
				t.Fatal(err)
			}
			allIDs = append(allIDs, id)
		}
		// Random cancels of pending events before the run.
		for _, id := range allIDs {
			if rng.Intn(8) == 0 {
				eng.Cancel(id)
			}
		}
		for eng.Step() {
			if rng.Intn(3) == 0 {
				if err := eng.Checkpoint(); err != nil {
					t.Fatal(err)
				}
			}
		}

		// Undo a few random executed events, verifying consistency.
		for u := 0; u < 3; u++ {
			var candidates []EventID
			for _, id := range allIDs {
				ev := eng.Event(id)
				if ev != nil && ev.Executed() && !ev.Undone() {
					candidates = append(candidates, id)
				}
			}
			if len(candidates) == 0 {
				break
			}
			victim := candidates[rng.Intn(len(candidates))]
			rep, err := eng.Undo(victim)
			if err != nil {
				t.Fatalf("trial %d: undo %d: %v", trial, victim, err)
			}
			// Every executed causal successor must be undone or compensated.
			for _, ev := range rep.CausalChain {
				if ev.Executed() && !ev.Undone() && !ev.External {
					t.Fatalf("trial %d: successor #%d neither undone nor external", trial, ev.ID)
				}
			}
			checkResim(t, eng, led)
		}
	}
}
