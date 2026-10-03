package saga

import (
	"testing"
	"time"
)

func TestHappyPath(t *testing.T) {
	dir := t.TempDir()
	w := newWorld()
	w.bankResponds = true
	eng, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	w.eng = eng
	eng.Register(w.definition(5 * time.Second))

	id, err := eng.Start("procurement", map[string]interface{}{"amount": "98000"})
	if err != nil {
		t.Fatal(err)
	}
	status, err := eng.Wait(id)
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusCompleted {
		t.Fatalf("status = %s, want COMPLETED", status)
	}

	events, err := eng.Audit(id)
	if err != nil {
		t.Fatal(err)
	}
	want := []EventType{
		EvTxStarted,
		EvStepStarted, EvAwaitingCallback, EvCallbackReceived, // approve
		EvStepStarted, EvStepCompleted, // purchase
		EvStepStarted, EvAwaitingCallback, EvCallbackReceived, // pay
		EvTxCompleted,
	}
	got := eventTypes(events)
	if len(got) != len(want) {
		t.Fatalf("audit length = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("audit[%d] = %s, want %s (full: %v)", i, got[i], want[i], got)
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.orders) != 1 {
		t.Fatalf("orders = %v, want exactly 1", w.orders)
	}
	if len(w.bankSubmissions) != 1 {
		t.Fatalf("bank submissions = %d, want 1", len(w.bankSubmissions))
	}
}

// Acceptance scenario: payment waits on an external bank that never
// answers; the timeout monitor wakes and drives reverse compensation.
func TestTimeoutTriggersReverseCompensation(t *testing.T) {
	dir := t.TempDir()
	w := newWorld()
	w.bankResponds = false // bank outage
	eng, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	w.eng = eng
	eng.Register(w.definition(150 * time.Millisecond))

	id, err := eng.Start("procurement", map[string]interface{}{"amount": "98000"})
	if err != nil {
		t.Fatal(err)
	}
	status, err := eng.Wait(id)
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusCompensated {
		t.Fatalf("status = %s, want COMPENSATED", status)
	}

	w.mu.Lock()
	if len(w.canceled) != 1 {
		t.Fatalf("canceled orders = %v, want exactly 1", w.canceled)
	}
	if len(w.orders) != 0 {
		t.Fatalf("orders left = %v, want none", w.orders)
	}
	if len(w.approverNotified) != 1 {
		t.Fatalf("approver notifications = %d, want 1", len(w.approverNotified))
	}
	w.mu.Unlock()

	events, err := eng.Audit(id)
	if err != nil {
		t.Fatal(err)
	}
	if i := indexOf(events, EvStepTimedOut, "pay"); i < 0 {
		t.Fatalf("audit missing StepTimedOut(pay): %v", eventTypes(events))
	}
	// Reverse order: cancel purchase BEFORE notifying the approver.
	cancel := indexOf(events, EvCompensateCompleted, "purchase")
	notify := indexOf(events, EvCompensateCompleted, "approve")
	if cancel < 0 || notify < 0 || cancel > notify {
		t.Fatalf("compensation order wrong: cancel@%d notify@%d", cancel, notify)
	}
	// The pay step never completed, so it must not be compensated.
	if i := indexOf(events, EvCompensateStarted, "pay"); i >= 0 {
		t.Fatalf("pay must not be compensated: %v", eventTypes(events))
	}
	if i := indexOf(events, EvTxCompensated, ""); i < 0 {
		t.Fatalf("audit missing TxCompensated: %v", eventTypes(events))
	}
}

// Power loss while parked waiting for the bank: after restart the
// timeout monitor must re-arm from the persisted deadline and still
// fire, and completed steps must not be re-executed.
func TestCrashRecoveryWhileAwaiting(t *testing.T) {
	dir := t.TempDir()
	w := newWorld()
	w.bankResponds = false

	awaitingPay := make(chan string, 1)
	eng, err := Open(dir, func(ev Event) {
		if ev.Type == EvAwaitingCallback && ev.Step == "pay" {
			awaitingPay <- ev.TxID
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	w.eng = eng
	eng.Register(w.definition(400 * time.Millisecond))

	id, err := eng.Start("procurement", nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-awaitingPay:
	case <-time.After(5 * time.Second):
		t.Fatal("transaction never reached the payment wait")
	}

	eng.Close() // power loss

	eng2, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()
	w.eng = eng2
	eng2.Register(w.definition(400 * time.Millisecond))

	status, err := eng2.Wait(id)
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusCompensated {
		t.Fatalf("status = %s, want COMPENSATED", status)
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.purchaseCalls != 1 {
		t.Fatalf("purchase executed %d times, want exactly 1 (no duplicate after crash)", w.purchaseCalls)
	}
	if len(w.bankSubmissions) != 1 {
		t.Fatalf("bank submissions = %d, want 1 (no duplicate after crash)", len(w.bankSubmissions))
	}
	if len(w.canceled) != 1 || len(w.approverNotified) != 1 {
		t.Fatalf("compensation incomplete: canceled=%v notified=%v", w.canceled, w.approverNotified)
	}
}

// Power loss after the external effect of a step but before its
// completion is journaled: recovery retries with the same idempotency
// key and the external system applies it at most once.
func TestCrashMidStepIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	w := newWorld()
	w.bankResponds = true
	w.crashOnPurchase = true

	eng, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	w.eng = eng
	eng.Register(w.definition(5 * time.Second))
	id, err := eng.Start("procurement", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Wait until the runner goroutine dies from the simulated crash.
	deadline := time.Now().Add(5 * time.Second)
	for {
		w.mu.Lock()
		calls := w.purchaseCalls
		w.mu.Unlock()
		if calls > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("purchase step never ran")
		}
		time.Sleep(5 * time.Millisecond)
	}
	eng.wg.Wait() // runner exited via simulated crash
	eng.Close()

	w.crashOnPurchase = false
	eng2, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()
	w.eng = eng2
	eng2.Register(w.definition(5 * time.Second))

	status, err := eng2.Wait(id)
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusCompleted {
		t.Fatalf("status = %s, want COMPLETED", status)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.purchaseCalls != 2 {
		t.Fatalf("purchase action calls = %d, want 2 (retry after crash)", w.purchaseCalls)
	}
	if len(w.orders) != 1 {
		t.Fatalf("orders = %v, want exactly 1 despite the retry (idempotent)", w.orders)
	}
}

// The audit trail must survive a restart and keep a contiguous
// sequence of timestamped events.
func TestAuditTrailSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	w := newWorld()
	w.bankResponds = false
	eng, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	w.eng = eng
	eng.Register(w.definition(100 * time.Millisecond))
	id, err := eng.Start("procurement", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Wait(id); err != nil {
		t.Fatal(err)
	}
	eng.Close()

	eng2, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()
	events, err := eng2.Audit(id)
	if err != nil {
		t.Fatal(err)
	}
	for i, ev := range events {
		if ev.Seq != int64(i+1) {
			t.Fatalf("event %d has seq %d, sequence broken", i, ev.Seq)
		}
		if ev.Time.IsZero() {
			t.Fatalf("event %d missing timestamp", i)
		}
	}
	if got := events[len(events)-1].Type; got != EvTxCompensated {
		t.Fatalf("last event = %s, want TxCompensated", got)
	}
}
