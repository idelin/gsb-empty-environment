package revoke

import (
	"math/rand"
	"strings"
	"testing"
	"time"
)

// enumFrame tracks one frame across an enumerated episode.
type enumFrame struct {
	id        string
	genID     uint64
	gate      *gate
	receipt   *Receipt
	beforeBar bool // submitted before the gen1 logout barrier was launched
}

// TestInterleaveEnumeration enumerates many legal schedules of logout,
// re-authentication and late frames against a pausable executor and checks the
// same invariants on every schedule.
func TestInterleaveEnumeration(t *testing.T) {
	const episodes = 400
	for ep := 0; ep < episodes; ep++ {
		rng := rand.New(rand.NewSource(int64(ep*7919 + 13)))
		t.Run("", func(t *testing.T) { runEpisode(t, rng) })
	}
}

func runEpisode(t *testing.T, rng *rand.Rand) {
	t.Helper()

	exec := NewPausableExecutor()
	d := New(exec)
	rec := newRecorder()
	var trace strings.Builder

	gen1, err := d.Login()
	if err != nil {
		t.Fatal(err)
	}

	gateA, gateB := newGate(), newGate()
	frames := map[string]*enumFrame{
		"A": {id: "A", genID: gen1.GenID(), gate: gateA, beforeBar: true},
		"B": {id: "B", genID: gen1.GenID(), gate: gateB, beforeBar: true},
	}
	frames["A"].receipt, _ = d.Submit(gen1, "A", rec.fn(gateA))
	frames["B"].receipt, _ = d.Submit(gen1, "B", rec.fn(gateB))
	queue := []*Receipt{frames["A"].receipt, frames["B"].receipt}

	var lateSubmitted bool
	steps := 0
	entered := map[string]bool{}
	// heldGate names the gate the currently running callback is blocked on;
	// "" means no callback is active.
	var heldGate string

	// Phase 1: everything up to launching the logout.
	for {
		var actions []string
		switch heldGate {
		case "A", "B":
			// A held gate must be released before any other interleaving.
			actions = []string{"release"}
		default:
			if exec.Queued() > 0 {
				actions = append(actions, "step")
			}
			if !lateSubmitted {
				actions = append(actions, "late")
			}
			if steps > 0 && rng.Intn(4) == 0 {
				actions = []string{"logout"}
			}
		}
		if steps > 0 && heldGate == "" && len(actions) == 0 {
			actions = append(actions, "logout")
		}
		choice := actions[rng.Intn(len(actions))]
		trace.WriteString(choice + " ")

		switch choice {
		case "step":
			if exec.Step(1) != 1 {
				t.Fatalf("step found no queued task; trace=%s", trace.String())
			}
			r := queue[0]
			queue = queue[1:]
			steps++
			entered[r.frame.id] = true
			switch r.frame.id {
			case "A":
				r.WaitScheduled()
				heldGate = "A"
			case "B":
				r.WaitScheduled()
				heldGate = "B"
			default:
				// Ungated frame: wait for the callback to fully finish so the
				// next action observes a settled executor.
				exec.WaitDone()
				heldGate = "" // ungated late frame completes immediately
			}
		case "release":
			switch heldGate {
			case "A":
				gateA.Release()
			case "B":
				gateB.Release()
			}
			exec.WaitDone()
			heldGate = ""
		case "late":
			lateSubmitted = true
			frames["L"] = &enumFrame{id: "L", genID: gen1.GenID(), beforeBar: true}
			frames["L"].receipt, _ = d.Submit(gen1, "L", rec.fn(nil))
			queue = append(queue, frames["L"].receipt)
		case "logout":
			goto logoutLaunched
		}
	}

logoutLaunched:
	logoutCh := make(chan Confirmation, 1)
	go func() {
		conf, err := d.Logout()
		if err != nil {
			t.Errorf("Logout: %v", err)
			return
		}
		logoutCh <- conf
	}()

	// Phase 2: release any held gates so the barrier can settle.
	if gateActive(gateA) {
		gateA.Release()
	}
	if gateActive(gateB) {
		gateB.Release()
	}

	var conf Confirmation
	select {
	case conf = <-logoutCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("logout confirmation never arrived; trace=%s active=%d queued=%d",
			trace.String(), exec.Active(), exec.Queued())
	}

	// A frame arriving strictly after confirmation must never be re-admitted.
	frames["M"] = &enumFrame{id: "M", genID: gen1.GenID(), beforeBar: false}
	frames["M"].receipt, _ = d.Submit(gen1, "M", rec.fn(nil))

	// Phase 3: re-authentication and a fresh generation.
	gen2, err := d.Login()
	if err != nil {
		t.Fatal(err)
	}
	if gen2.GenID() != gen1.GenID()+1 {
		t.Fatalf("re-login did not build a fresh generation: %d -> %d", gen1.GenID(), gen2.GenID())
	}

	frames["C"] = &enumFrame{id: "C", genID: gen2.GenID(), beforeBar: true}
	frames["C"].receipt, _ = d.Submit(gen2, "C", rec.fn(nil))

	// Old tokens cannot ride the new session.
	frames["N"] = &enumFrame{id: "N", genID: gen1.GenID(), beforeBar: false}
	frames["N"].receipt, _ = d.Submit(gen1, "N", rec.fn(nil))

	exec.Resume()
	exec.WaitDone()

	conf2, err := d.Logout()
	if err != nil {
		t.Fatal(err)
	}

	seenPoints := map[uint64]bool{}
	gen1Sensitive := 0
	for id, ef := range frames {
		dec := ef.receipt.Decision()
		// Every not-started frame of a logged-out generation shares the single
		// barrier point, so point uniqueness only applies to other decisions.
		if seenPoints[dec.Point] && dec.Reason != ReasonRevoked {
			t.Fatalf("duplicate linearization point %d for frame %s", dec.Point, id)
		}
		seenPoints[dec.Point] = true
		if dec.GenID != ef.genID || dec.FrameID != id {
			t.Fatalf("frame %s decision identity mismatch: %+v", id, dec)
		}

		switch ef.genID {
		case gen1.GenID():
			switch {
			case entered[id]:
				if !dec.Authorized || dec.Reason != ReasonAuthorized || dec.Point >= conf.BarrierPoint {
					t.Fatalf("frame %s should be authorized before barrier: %+v barrier=%d",
						id, dec, conf.BarrierPoint)
				}
				gen1Sensitive++
			case ef.beforeBar:
				if dec.Authorized || dec.Reason != ReasonRevoked || dec.Point != conf.BarrierPoint {
					t.Fatalf("frame %s should be revoked at barrier %d: %+v",
						id, conf.BarrierPoint, dec)
				}
			default:
				if dec.Authorized ||
					(dec.Reason != ReasonRevoked && dec.Reason != ReasonNoSession) ||
					dec.Point <= conf.BarrierPoint {
					t.Fatalf("late frame %s should be rejected after barrier: %+v", id, dec)
				}
			}
		case gen2.GenID():
			if !dec.Authorized || dec.Reason != ReasonAuthorized {
				t.Fatalf("new-generation frame %s not authorized: %+v", id, dec)
			}
		}
	}

	if got := rec.count(gen1.GenID()); got != gen1Sensitive {
		t.Fatalf("gen1 sensitive calls = %d, expected %d (only frames started before barrier)",
			got, gen1Sensitive)
	}
	if got := rec.count(gen2.GenID()); got != 1 {
		t.Fatalf("gen2 sensitive calls = %d, want 1", got)
	}

	confirmed := map[string]Decision{}
	for _, dec := range conf.Decisions {
		if _, dup := confirmed[dec.FrameID]; dup {
			t.Fatalf("confirmation lists frame %s twice", dec.FrameID)
		}
		confirmed[dec.FrameID] = dec
	}
	for id, ef := range frames {
		if ef.genID != gen1.GenID() || !ef.beforeBar {
			continue
		}
		dec, ok := confirmed[id]
		if !ok {
			t.Fatalf("confirmation missing pre-barrier frame %s: %+v", id, conf.Decisions)
		}
		if dec.Authorized != entered[id] {
			t.Fatalf("confirmation frame %s authorized=%v want %v", id, dec.Authorized, entered[id])
		}
	}

	if len(conf2.Decisions) != 1 || !conf2.Decisions[0].Authorized ||
		conf2.Decisions[0].FrameID != "C" ||
		conf2.Decisions[0].Point >= conf2.BarrierPoint {
		t.Fatalf("gen2 confirmation wrong: %+v", conf2.Decisions)
	}

	exec.Close()
}

func gateActive(g *gate) bool {
	select {
	case <-g.entered:
		select {
		case <-g.release:
			return false
		default:
			return true
		}
	default:
		return false
	}
}
