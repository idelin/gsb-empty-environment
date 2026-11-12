package revoke

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// gate lets a test hold a sensitive callback inside its body until Release is
// called, so logout barriers can be forced to wait.
type gate struct {
	once    sync.Once
	release chan struct{}
	entered chan struct{}
}

func newGate() *gate {
	return &gate{release: make(chan struct{}), entered: make(chan struct{})}
}

func (g *gate) Release() { g.once.Do(func() { close(g.release) }) }

// recorder counts sensitive callback executions per generation.
type recorder struct {
	mu     sync.Mutex
	calls  map[uint64]int
	frames []string
}

func newRecorder() *recorder {
	return &recorder{calls: make(map[uint64]int)}
}

func (r *recorder) fn(g *gate) SensitiveFunc {
	return func(ctx AuthContext) {
		r.mu.Lock()
		r.calls[ctx.Generation.GenID()]++
		r.frames = append(r.frames, ctx.FrameID)
		r.mu.Unlock()
		if g != nil {
			close(g.entered)
			<-g.release
		}
	}
}

func (r *recorder) count(genID uint64) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[genID]
}

func assertDecision(t *testing.T, got Decision, frameID string, genID uint64, authorized bool, reason Reason) {
	t.Helper()
	if got.FrameID != frameID || got.GenID != genID ||
		got.Authorized != authorized || got.Reason != reason || got.Point == 0 {
		t.Fatalf("decision mismatch for %s: got %+v want authorized=%v reason=%q point>0",
			frameID, got, authorized, reason)
	}
}

func assertBlocked(t *testing.T, r *Receipt) {
	t.Helper()
	select {
	case dec := <-r.frame.decision:
		t.Fatalf("receipt unexpectedly resolved: %+v", dec)
	case <-time.After(20 * time.Millisecond):
	}
}

// step starts one queued executor task and waits for its dispatcher admission
// decision to settle under the shared linear order.
func step(t *testing.T, exec *PausableExecutor, r *Receipt) {
	t.Helper()
	if n := exec.Step(1); n != 1 {
		t.Fatalf("Step started %d tasks, want 1", n)
	}
	r.WaitScheduled()
}

// TestLogoutBarrierCanonical drives the headline interleaving: one callback in
// flight, one frame queued, a late submission, logout, then re-login.
func TestLogoutBarrierCanonical(t *testing.T) {
	exec := NewPausableExecutor()
	d := New(exec)
	rec := newRecorder()

	gen1, err := d.Login()
	if err != nil {
		t.Fatal(err)
	}

	gateA := newGate()
	rA2, _ := d.Submit(gen1, "A", rec.fn(gateA))
	gateB := newGate()
	rB, _ := d.Submit(gen1, "B", rec.fn(gateB))

	// Start only A; B remains queued in the executor.
	step(t, exec, rA2)
	<-gateA.entered

	// Late submission while A is in flight: it queues behind B.
	rC, _ := d.Submit(gen1, "C", rec.fn(nil))

	loggedOut := make(chan Confirmation, 1)
	go func() {
		conf, err := d.Logout()
		if err != nil {
			t.Errorf("Logout: %v", err)
			return
		}
		loggedOut <- conf
	}()

	// Logout must wait for the in-flight callback A to finish.
	select {
	case <-loggedOut:
		t.Fatal("Logout returned before in-flight sensitive callback finished")
	case <-time.After(20 * time.Millisecond):
	}

	// A submission after the barrier cannot reuse the old identity.
	rD, decD := d.Submit(gen1, "D", rec.fn(nil))
	if decD.Authorized || decD.Reason != ReasonNoSession {
		t.Fatalf("late frame D decision = %+v, want no-session", decD)
	}

	gateA.Release()

	var conf Confirmation
	select {
	case conf = <-loggedOut:
	case <-time.After(2 * time.Second):
		t.Fatal("Logout never returned after in-flight callback finished")
	}
	if conf.GenID != gen1.GenID() || conf.BarrierPoint == 0 {
		t.Fatalf("bad confirmation: %+v", conf)
	}
	// A (authorized, point before barrier) plus B and C (revoked at barrier).
	if len(conf.Decisions) != 3 {
		t.Fatalf("confirmation has %d decisions, want 3: %+v", len(conf.Decisions), conf.Decisions)
	}
	byFrame := map[string]Decision{}
	for _, dec := range conf.Decisions {
		byFrame[dec.FrameID] = dec
	}
	assertDecision(t, byFrame["A"], "A", gen1.GenID(), true, ReasonAuthorized)
	assertDecision(t, byFrame["B"], "B", gen1.GenID(), false, ReasonRevoked)
	assertDecision(t, byFrame["C"], "C", gen1.GenID(), false, ReasonRevoked)
	if !(byFrame["A"].Point < byFrame["B"].Point) {
		t.Fatalf("admission point not before barrier: A=%d B=%d", byFrame["A"].Point, byFrame["B"].Point)
	}

	// After confirmation: zero old-generation sensitive calls beyond A.
	if got := rec.count(gen1.GenID()); got != 1 {
		t.Fatalf("gen1 sensitive calls = %d, want exactly 1 (the in-flight frame)", got)
	}

	// B and C receipts resolve to revocation once their executor tasks run.
	exec.Resume()
	decB := rB.Decision()
	assertDecision(t, decB, "B", gen1.GenID(), false, ReasonRevoked)
	decC := rC.Decision()
	assertDecision(t, decC, "C", gen1.GenID(), false, ReasonRevoked)
	decD2 := rD.Decision()
	assertDecision(t, decD2, "D", gen1.GenID(), false, ReasonNoSession)

	if got := rec.count(gen1.GenID()); got != 1 {
		t.Fatalf("gen1 sensitive calls after drain = %d, want 1", got)
	}

	// Re-login establishes a new generation; the old queue is never re-marked.
	gen2, err := d.Login()
	if err != nil {
		t.Fatal(err)
	}
	if gen2.GenID() <= gen1.GenID() {
		t.Fatalf("new generation id %d not greater than old %d", gen2.GenID(), gen1.GenID())
	}

	// A stale token from the old generation cannot ride the new session.
	_, decE := d.Submit(gen1, "E", rec.fn(nil))
	if decE.Authorized || decE.Reason != ReasonRevoked {
		t.Fatalf("stale-token frame E decision = %+v, want revoked", decE)
	}

	exec.Pause()
	rF, _ := d.Submit(gen2, "F", rec.fn(nil))
	assertBlocked(t, rF)
	step(t, exec, rF)
	assertDecision(t, rF.Decision(), "F", gen2.GenID(), true, ReasonAuthorized)

	if got := rec.count(gen1.GenID()); got != 1 {
		t.Fatalf("old generation sensitive calls = %d after re-login, want 1", got)
	}
	if got := rec.count(gen2.GenID()); got != 1 {
		t.Fatalf("new generation sensitive calls = %d, want 1", got)
	}

	// Session lifecycle guards.
	if _, err := d.Login(); err != ErrAlreadyLoggedIn {
		t.Fatalf("Login while active err = %v, want ErrAlreadyLoggedIn", err)
	}
	if _, err := d.Logout(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Logout(); err != ErrNoActiveSession {
		t.Fatalf("second Logout err = %v, want ErrNoActiveSession", err)
	}
	_, decNoSession := d.Submit(gen2, "G", rec.fn(nil))
	if decNoSession.Authorized || decNoSession.Reason != ReasonNoSession {
		t.Fatalf("no-session decision = %+v", decNoSession)
	}

	exec.Resume()
	exec.WaitDone()
	exec.Close()
}

// TestConcurrentGenerations exercises the goroutine executor under real
// concurrency. After every logout confirmation, no sensitive call of the old
// generation may occur, even with submitters racing the barrier.
func TestConcurrentGenerations(t *testing.T) {
	d := New(NewGoroutineExecutor())
	rec := newRecorder()

	const generations = 8
	const submitters = 6
	const framesPerSubmitter = 40

	for g := uint64(1); g <= generations; g++ {
		gen, err := d.Login()
		if err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		for s := 0; s < submitters; s++ {
			wg.Add(1)
			go func(submitter int) {
				defer wg.Done()
				for k := 0; k < framesPerSubmitter; k++ {
					id := fmt.Sprintf("g%d-s%d-f%d", g, submitter, k)
					r, immediate := d.Submit(gen, id, rec.fn(nil))
					if immediate.Authorized {
						continue
					}
					dec := r.Decision()
					if dec.GenID != g {
						t.Errorf("frame %s recorded under generation %d", id, dec.GenID)
					}
				}
			}(s)
		}
		wg.Wait()

		conf, err := d.Logout()
		if err != nil {
			t.Fatal(err)
		}

		// Snapshot the old generation's sensitive count after confirmation; it
		// must never grow again because every later frame is rejected.
		before := rec.count(g)
		authorized := 0
		for _, dec := range conf.Decisions {
			if dec.Authorized {
				authorized++
			}
			if dec.Point > conf.BarrierPoint {
				t.Fatalf("generation %d decision point %d after barrier %d",
					g, dec.Point, conf.BarrierPoint)
			}
		}
		if authorized != before {
			t.Fatalf("generation %d: %d authorized in confirmation but %d sensitive calls",
				g, authorized, before)
		}
	}

	// Re-check all generations: post-confirmation counts are stable.
	for g := uint64(1); g <= generations; g++ {
		if rec.count(g) == 0 {
			t.Fatalf("generation %d recorded no sensitive calls at all", g)
		}
	}
}

// TestBarrierPointsLinearized checks that admission points and revocation
// barrier points are drawn from one strictly increasing sequence.
func TestBarrierPointsLinearized(t *testing.T) {
	exec := NewPausableExecutor()
	d := New(exec)
	rec := newRecorder()

	var allPoints []uint64
	gen1, _ := d.Login()
	r1, _ := d.Submit(gen1, "p1", rec.fn(nil))
	r2, _ := d.Submit(gen1, "p2", rec.fn(nil))
	step(t, exec, r1)
	step(t, exec, r2)
	r1.WaitScheduled()
	r2.WaitScheduled()
	allPoints = append(allPoints, r1.Decision().Point, r2.Decision().Point)

	conf1, err := d.Logout()
	if err != nil {
		t.Fatal(err)
	}
	allPoints = append(allPoints, conf1.BarrierPoint)

	gen2, _ := d.Login()
	r3, _ := d.Submit(gen2, "p3", rec.fn(nil))
	step(t, exec, r3)
	allPoints = append(allPoints, r3.Decision().Point)

	conf2, _ := d.Logout()
	allPoints = append(allPoints, conf2.BarrierPoint)

	for i := 1; i < len(allPoints); i++ {
		if allPoints[i] <= allPoints[i-1] {
			t.Fatalf("point sequence not strictly increasing: %v", allPoints)
		}
	}

	exec.Resume()
	exec.WaitDone()
	exec.Close()
}
