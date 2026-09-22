package retrysys_test

import (
	"strings"
	"testing"

	"example/retrysim/retrysys"
)

func behaviorsSame(f retrysys.Behavior, n int) map[string]retrysys.Behavior {
	out := map[string]retrysys.Behavior{}
	for i := 0; i < n; i++ {
		out["replica-"+itoa(i)] = f
	}
	return out
}

func itoa(i int) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune { return r },
		strings.Repeat("x", 0))) + smallInt(i)
}

func smallInt(i int) string {
	digits := []byte{'0', '1', '2', '3', '4', '5', '6', '7', '8', '9'}
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{digits[i%10]}, b...)
		i /= 10
	}
	return string(b)
}

func countKind(evs []retrysys.Event, k retrysys.Kind) int {
	n := 0
	for _, e := range evs {
		if e.Kind == k {
			n++
		}
	}
	return n
}

// TestUpper3Lower3DoesNotMultiply: the headline safety property. With 3
// upper x 3 lower attempts and all replicas failing fast, the naive product
// would be 9 backend requests. A single shared budget of 3 means exactly 3
// physical requests are sent and then retries stop as "budget exhausted".
func TestUpper3Lower3DoesNotMultiply(t *testing.T) {
	cfg := retrysys.DefaultConfig()
	cfg.MaxRequests = 3
	beh := retrysys.FailFirst(100, 1) // every physical request fails at t+1

	w := retrysys.NewWorld(cfg, behaviorsSame(beh, cfg.Replicas))
	ok, why, end, err := w.Run()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if ok {
		t.Fatalf("expected failure, got success")
	}
	if got := w.Ledger.PhysicalSends(1); got != 3 {
		t.Fatalf("physical sends = %d, want 3 (not 9)", got)
	}
	if w.Budget().Used() != 3 {
		t.Fatalf("budget used = %d, want 3", w.Budget().Used())
	}
	if err := w.Ledger.Check(cfg.Deadline, cfg.MaxRequests); err != nil {
		t.Fatalf("ledger safety: %v", err)
	}
	if why != retrysys.WhySharedBudget && why != retrysys.WhyAttemptBudget {
		t.Fatalf("terminal why = %q, want budget-exhausted", why)
	}
	t.Logf("upper=3 lower=3 -> actual physical requests=%d, terminal=%q at t=%d",
		w.Ledger.PhysicalSends(1), why, end)
}

// TestSharedBudgetSmallerThanLayers: with budget 1, neither layer may issue
// more than one physical request even though both have 3 attempts.
func TestSharedBudgetSmallerThanLayers(t *testing.T) {
	cfg := retrysys.DefaultConfig()
	cfg.MaxRequests = 1
	w := retrysys.NewWorld(cfg, behaviorsSame(retrysys.FailFirst(100, 1), cfg.Replicas))
	ok, _, _, err := w.Run()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if ok {
		t.Fatal("unexpected success")
	}
	if got := w.Ledger.PhysicalSends(1); got != 1 {
		t.Fatalf("physical sends = %d, want 1", got)
	}
	if err := w.Ledger.Check(cfg.Deadline, cfg.MaxRequests); err != nil {
		t.Fatalf("ledger safety: %v", err)
	}
}

// TestDeadlineStopsAllRequests: dropped requests cause lower-attempt
// timeouts and upper retries. A tight deadline stops the loop: every send is
// strictly before the deadline, the send that would land at/after it is
// refused, and the terminal reason is the deadline.
func TestDeadlineStopsAllRequests(t *testing.T) {
	cfg := retrysys.DefaultConfig()
	cfg.MaxRequests = 10 // generous on purpose: the deadline, not budget, binds
	cfg.Deadline = 12
	cfg.LowerAttemptTimeout = 10
	cfg.UpperAttemptTimeout = 11
	cfg.UpperBackoff = 1

	w := retrysys.NewWorld(cfg, behaviorsSame(retrysys.DropAll(), cfg.Replicas))
	ok, why, end, err := w.Run()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if ok {
		t.Fatal("unexpected success after deadline")
	}
	if why != retrysys.WhyDeadline {
		t.Fatalf("terminal why = %q, want deadline-reached", why)
	}
	sends := 0
	for _, e := range w.Ledger.Events() {
		if e.Kind == retrysys.KindSend {
			sends++
			if e.Tick >= cfg.Deadline {
				t.Fatalf("send %s at t=%d on/after deadline t=%d", e.ID, e.Tick, cfg.Deadline)
			}
		}
	}
	// t=0 send times out at t=10; upper retry is scheduled t=11; its lower
	// loop sends at t=11 (before deadline 12). The next attempt would be
	// t=12 == deadline and must be refused. Hence exactly 2 physical sends.
	if sends != 2 {
		t.Fatalf("physical sends = %d, want exactly 2 (next would be at deadline)", sends)
	}
	if end < cfg.Deadline {
		t.Fatalf("terminal at t=%d before deadline t=%d", end, cfg.Deadline)
	}
	if err := w.Ledger.Check(cfg.Deadline, cfg.MaxRequests); err != nil {
		t.Fatalf("ledger safety: %v", err)
	}
	t.Logf("deadline t=%d: sends=%d all strictly before deadline; terminal=%q at t=%d",
		cfg.Deadline, sends, why, end)
}

// TestCancellationPropagates: a slow dropped request is cancelled when the
// lower-attempt timer fires; the ledger must show the cancel event and the
// replica must honor it (no reply resurrects the attempt).
func TestCancellationPropagates(t *testing.T) {
	cfg := retrysys.DefaultConfig()
	cfg.MaxRequests = 5
	cfg.LowerAttemptTimeout = 10

	w := retrysys.NewWorld(cfg, behaviorsSame(retrysys.DropAll(), cfg.Replicas))
	if _, _, _, err := w.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	evs := w.Ledger.Events()
	cancels := countKind(evs, retrysys.KindCancel)
	if cancels == 0 {
		t.Fatal("expected cancellation propagation events, got none")
	}
	if err := w.Ledger.Check(cfg.Deadline, cfg.MaxRequests); err != nil {
		t.Fatalf("ledger safety: %v", err)
	}
}

// TestLateFailureAfterSuccessDoesNotFlipTerminal: the first physical request
// succeeds. A scripted "late failure" from a request whose cancellation was
// not honored (cancel lost in the network) arrives after the proxy is
// terminal. The terminal result must remain success and the late reply must
// be recorded as ignored.
func TestLateFailureAfterSuccessDoesNotFlipTerminal(t *testing.T) {
	cfg := retrysys.DefaultConfig()
	cfg.MaxRequests = 5

	// Ordinal 1: fast success (attempt settles). Ordinal 2: a failure that
	// arrives late. In this script the late failure is produced by an
	// outstanding sibling-style retry path; the simulator guarantees it is
	// only received after termination and therefore ignored.
	beh := retrysys.Scripted([]retrysys.Effect{
		{Delay: 1, OK: true, HonorCancel: true},
		{Delay: 20, OK: false, HonorCancel: false, Late: true},
	}, retrysys.Effect{Delay: 1, OK: true, HonorCancel: true})
	w := retrysys.NewWorld(cfg, behaviorsSame(beh, cfg.Replicas))

	// Drive a run where the second physical request is forced: first upper
	// attempt succeeds immediately, so we instead use a failure-then-success
	// script for the main liveness/late interaction below.
	_ = w

	// Precise construction: lower attempt 1 fails slowly (cancelled by the
	// upper attempt timer which does NOT suppress its reply), lower attempt
	// 2 of the next upper attempt succeeds; the late failure then arrives.
	cfg2 := cfg
	cfg2.LowerAttemptTimeout = 5
	cfg2.UpperAttemptTimeout = 8
	cfg2.UpperBackoff = 1
	beh2 := retrysys.Scripted([]retrysys.Effect{
		{Delay: 20, OK: false, HonorCancel: false, Late: true}, // ignored cancel -> late fail
		{Delay: 1, OK: false, HonorCancel: true},               // fail fast
		{Delay: 1, OK: true, HonorCancel: true},                // success
	}, retrysys.Effect{Delay: 1, OK: true, HonorCancel: true})
	w2 := retrysys.NewWorld(cfg2, behaviorsSame(beh2, cfg2.Replicas))
	ok, why, end, err := w2.Run()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !ok || why != retrysys.WhyOK {
		t.Fatalf("terminal = (%v,%q), want success terminal", ok, why)
	}
	lateIgnored := 0
	for _, e := range w2.Ledger.Events() {
		if e.Why == retrysys.WhyLate {
			lateIgnored++
		}
	}
	if lateIgnored == 0 {
		t.Fatal("expected the late failure to be recorded as ignored")
	}
	// Terminal success stays the first and only proxy terminal.
	term, found := w2.Ledger.FirstTerminal(1, "proxy")
	if !found || term.Why != retrysys.WhyOK || term.Tick > end {
		t.Fatalf("terminal event wrong: %+v found=%v end=%d", term, found, end)
	}
	if err := w2.Ledger.Check(cfg2.Deadline, cfg2.MaxRequests); err != nil {
		t.Fatalf("ledger safety: %v", err)
	}
	t.Logf("late failure arrived after t=%d success; ignored=%d; terminal stays ok",
		term.Tick, lateIgnored)
}

// TestLivenessSucceedsWithinBudget: under failures the layers keep retrying
// and, once a replica succeeds, terminate ok and stop sending.
func TestLivenessSucceedsWithinBudget(t *testing.T) {
	cfg := retrysys.DefaultConfig()
	cfg.MaxRequests = 5
	beh := retrysys.Scripted([]retrysys.Effect{
		{Delay: 1, OK: false, HonorCancel: true},
		{Delay: 1, OK: false, HonorCancel: true},
		{Delay: 1, OK: true, HonorCancel: true},
	}, retrysys.Effect{Delay: 1, OK: true, HonorCancel: true})
	w := retrysys.NewWorld(cfg, behaviorsSame(beh, cfg.Replicas))
	ok, why, end, err := w.Run()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !ok || why != retrysys.WhyOK {
		t.Fatalf("terminal = (%v,%q), want ok", ok, why)
	}
	if got := w.Ledger.PhysicalSends(1); got != 3 {
		t.Fatalf("physical sends = %d, want exactly 3 (no success-after-send)", got)
	}
	if end >= cfg.Deadline {
		t.Fatalf("liveness: settled at t=%d but should be well before deadline", end)
	}
	if err := w.Ledger.Check(cfg.Deadline, cfg.MaxRequests); err != nil {
		t.Fatalf("ledger safety: %v", err)
	}
}

// TestLivenessExhaustionAndDropTerminate: if every request is dropped and
// the budget is finite, the call still terminates (failure) — the system is
// livelock-free.
func TestLivenessExhaustionAndDropTerminate(t *testing.T) {
	cfg := retrysys.DefaultConfig()
	cfg.MaxRequests = 4
	w := retrysys.NewWorld(cfg, behaviorsSame(retrysys.DropAll(), cfg.Replicas))
	ok, why, _, err := w.Run()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if ok {
		t.Fatal("unexpected ok with all drops")
	}
	if why != retrysys.WhySharedBudget && why != retrysys.WhyAttemptBudget && why != retrysys.WhyDeadline {
		t.Fatalf("terminal why = %q", why)
	}
	if got := w.Ledger.PhysicalSends(1); got > cfg.MaxRequests {
		t.Fatalf("sent %d > budget %d", got, cfg.MaxRequests)
	}
}

// TestDeterministic: identical scripts must produce identical ledgers.
func TestDeterministic(t *testing.T) {
	run := func() []retrysys.Event {
		cfg := retrysys.DefaultConfig()
		cfg.MaxRequests = 4
		beh := retrysys.Scripted([]retrysys.Effect{
			{Delay: 1, OK: false, HonorCancel: true},
			{Delay: 2, OK: false, HonorCancel: true},
		}, retrysys.Effect{Drop: true})
		w := retrysys.NewWorld(cfg, behaviorsSame(beh, cfg.Replicas))
		if _, _, _, err := w.Run(); err != nil {
			t.Fatalf("run: %v", err)
		}
		return w.Ledger.Events()
	}
	a := run()
	b := run()
	if len(a) != len(b) {
		t.Fatalf("non-deterministic event count: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].String() != b[i].String() {
			t.Fatalf("event %d differs:\n  %s\n  %s", i, a[i], b[i])
		}
	}
}
