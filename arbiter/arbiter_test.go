package arbiter

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

// commitKind extracts the sentinel classifying a commit failure.
func commitKind(err error) error {
	if ce, ok := err.(*CommitError); ok {
		return ce.Kind
	}
	return nil
}

// ---------------------------------------------------------------------------
// Sequential semantics
// ---------------------------------------------------------------------------

func TestSequentialProposeCommit(t *testing.T) {
	a := New()
	e1 := a.Propose(1)
	if e1 != 1 {
		t.Fatalf("first epoch = %d, want 1", e1)
	}
	if err := a.Commit(e1, 1, "hello"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	log := a.Log()
	if len(log) != 1 || log[0].Value != "hello" || log[0].Node != 1 || log[0].Epoch != 1 {
		t.Fatalf("bad log: %+v", log)
	}
}

func TestEpochsStrictlyIncrease(t *testing.T) {
	a := New()
	prev := a.Epoch()
	for i := 0; i < 100; i++ {
		e := a.Propose(i)
		if e <= prev {
			t.Fatalf("epoch not increasing: %d after %d", e, prev)
		}
		prev = e
	}
}

func TestStaleTokenRejected(t *testing.T) {
	a := New()
	e1 := a.Propose(1)
	e2 := a.Propose(2) // supersedes e1
	if err := a.Commit(e1, 1, "old"); commitKind(err) != ErrStale {
		t.Fatalf("stale commit: want ErrStale, got %v", err)
	}
	if err := a.Commit(e2, 2, "new"); err != nil {
		t.Fatalf("fresh commit: %v", err)
	}
	if got := len(a.Log()); got != 1 {
		t.Fatalf("log length = %d, want 1", got)
	}
	st := a.Stats()
	if st.StaleWins != 1 || st.Commits != 1 || st.Proposals != 2 {
		t.Fatalf("bad stats: %+v", st)
	}
}

func TestWrongNodeRejected(t *testing.T) {
	a := New()
	e := a.Propose(1)
	if err := a.Commit(e, 2, "impostor"); commitKind(err) != ErrNotHolder {
		t.Fatalf("want ErrNotHolder, got %v", err)
	}
	if got := len(a.Log()); got != 0 {
		t.Fatalf("log length = %d, want 0", got)
	}
}

func TestCommitZeroEpochRejected(t *testing.T) {
	a := New()
	// No proposal yet: epoch 0 is not a valid token for anyone.
	if err := a.Commit(0, 7, "x"); err == nil {
		t.Fatal("commit without proposal should fail")
	}
}

// ---------------------------------------------------------------------------
// Concurrency: competition, linearizability, race-freedom
// ---------------------------------------------------------------------------

// runCompetition launches nodes goroutines that each attempt proposals x
// propose+commit cycles, and returns the arbiter plus per-node success
// counts. It is the shared driver for the concurrency tests.
func runCompetition(nodes, proposals int) (*Arbiter, []int) {
	a := New()
	wins := make([]int, nodes)
	var wg sync.WaitGroup
	for n := 0; n < nodes; n++ {
		wg.Add(1)
		go func(node int) {
			defer wg.Done()
			for i := 0; i < proposals; i++ {
				e := a.Propose(node)
				if err := a.Commit(e, node, node); err == nil {
					wins[node]++
				}
			}
		}(n)
	}
	wg.Wait()
	return a, wins
}

func TestConcurrentCompetition(t *testing.T) {
	const nodes, proposals = 16, 500
	a, wins := runCompetition(nodes, proposals)

	st := a.Stats()
	if st.Proposals != uint64(nodes*proposals) {
		t.Fatalf("proposals = %d, want %d", st.Proposals, nodes*proposals)
	}
	total := 0
	for _, w := range wins {
		total += w
	}
	if st.Commits != uint64(total) {
		t.Fatalf("commits = %d, want %d (sum of observed wins)", st.Commits, total)
	}
	if int(st.Commits) != len(a.Log()) {
		t.Fatalf("commits = %d but log holds %d entries", st.Commits, len(a.Log()))
	}
	if st.Commits+st.StaleWins+st.WrongNode != st.Proposals {
		t.Fatalf("stats do not add up: %+v", st)
	}
	if st.Commits == 0 {
		t.Fatal("no commit ever succeeded; arbiter is starved")
	}
}

// TestLinearizableLog verifies the committed history is a strict total
// order: epochs in the log are strictly increasing, every committed epoch
// was actually granted, and each entry was committed by the node the epoch
// belonged to.
func TestLinearizableLog(t *testing.T) {
	const nodes, proposals = 8, 300
	a, _ := runCompetition(nodes, proposals)
	log := a.Log()
	prev := uint64(0)
	for i, e := range log {
		if e.Epoch <= prev {
			t.Fatalf("log[%d]: epoch %d not greater than previous %d", i, e.Epoch, prev)
		}
		if e.Epoch > a.Epoch() {
			t.Fatalf("log[%d]: epoch %d was never granted (max %d)", i, e.Epoch, a.Epoch())
		}
		if e.Node < 0 || e.Node >= nodes {
			t.Fatalf("log[%d]: bogus node %d", i, e.Node)
		}
		if v, ok := e.Value.(int); !ok || v != e.Node {
			t.Fatalf("log[%d]: value %v does not match node %d", i, e.Value, e.Node)
		}
		prev = e.Epoch
	}
}

// TestFencingUnderContention hammers the arbiter with pairs of nodes that
// interleave propose/commit to force stale-token conflicts, and asserts no
// stale value ever lands in the log.
func TestFencingUnderContention(t *testing.T) {
	a := New()
	const pairs = 64
	var wg sync.WaitGroup
	for p := 0; p < pairs; p++ {
		wg.Add(2)
		go func(p int) {
			defer wg.Done()
			e := a.Propose(2 * p)
			// Yield to widen the race window before committing.
			runtime.Gosched()
			_ = a.Commit(e, 2*p, p)
		}(p)
		go func(p int) {
			defer wg.Done()
			e := a.Propose(2*p + 1)
			runtime.Gosched()
			_ = a.Commit(e, 2*p+1, p)
		}(p)
	}
	wg.Wait()
	log := a.Log()
	prev := uint64(0)
	for i, e := range log {
		if e.Epoch <= prev {
			t.Fatalf("log[%d]: stale value committed (epoch %d after %d)", i, e.Epoch, prev)
		}
		prev = e.Epoch
	}
}

// TestNoDeadlock drives maximal contention with a watchdog: if any
// goroutine blocks forever the test times out and fails.
func TestNoDeadlock(t *testing.T) {
	done := make(chan struct{})
	go func() {
		runCompetition(32, 2000)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("deadlock: competition did not finish in 30s")
	}
}

// TestNoGoroutineLeak asserts the arbiter neither spawns nor strands
// goroutines: after a competition and a settle window, the goroutine count
// returns to baseline.
func TestNoGoroutineLeak(t *testing.T) {
	base := runtime.NumGoroutine()
	runCompetition(16, 500)
	// The arbiter spawns nothing, so only scheduler lag can delay the count.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= base {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: baseline %d, now %d", base, runtime.NumGoroutine())
}

// TestLogReturnsCopy ensures callers cannot mutate internal state.
func TestLogReturnsCopy(t *testing.T) {
	a := New()
	e := a.Propose(1)
	if err := a.Commit(e, 1, "v"); err != nil {
		t.Fatal(err)
	}
	log := a.Log()
	log[0].Value = "corrupted"
	if a.Log()[0].Value != "v" {
		t.Fatal("internal log mutated through returned slice")
	}
}
