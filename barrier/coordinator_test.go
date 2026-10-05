package barrier

import (
	"reflect"
	"testing"
)

// drainNothing fails the test if any result was produced.
func drainNothing(t *testing.T, c *Coordinator, why string) {
	t.Helper()
	if res := c.DrainResults(); len(res) != 0 {
		t.Fatalf("%s: unexpected results: %+v", why, res)
	}
}

func drainOne(t *testing.T, c *Coordinator) Result {
	t.Helper()
	res := c.DrainResults()
	if len(res) != 1 {
		t.Fatalf("expected exactly 1 result, got %+v", res)
	}
	return res[0]
}

// A high report with a gap in between must not count as progress; filling
// the gap releases the buffered reports in order.
func TestGapBlocksProgressUntilFilled(t *testing.T) {
	clock := &ManualClock{}
	c := NewCoordinator(clock)
	c.Recover("a", 1, 0)
	c.Recover("b", 1, 0)

	id := c.Wait(3, []ShardID{"a", "b"}, 100)

	c.Report("b", 1, 1)
	c.Report("b", 1, 2)
	c.Report("b", 1, 3)
	c.Report("a", 1, 1)
	c.Report("a", 1, 3) // gap: commit 2 missing on shard a
	drainNothing(t, c, "gap on shard a must block the grant")

	st := c.Stats()
	if st.BufferedReports != 1 {
		t.Fatalf("expected 1 buffered out-of-order report, got %+v", st)
	}

	c.Report("a", 1, 2) // gap filled: watermark drains 2 then 3
	res := drainOne(t, c)
	if !res.Granted || res.Req != id {
		t.Fatalf("expected grant for req %d, got %+v", id, res)
	}
	if res.View.Commit != 3 {
		t.Fatalf("view must be pinned at target 3, got %+v", res.View)
	}
	if !reflect.DeepEqual(res.View.Shards, []ShardID{"a", "b"}) {
		t.Fatalf("unexpected view shards: %+v", res.View.Shards)
	}
	if st := c.Stats(); st.PendingRequests != 0 || st.IndexEntries != 0 || st.BufferedReports != 0 {
		t.Fatalf("state leaked after grant: %+v", st)
	}
}

// The view is pinned at the target even when shards have moved past it.
func TestViewPinnedAtTarget(t *testing.T) {
	clock := &ManualClock{}
	c := NewCoordinator(clock)
	c.Recover("a", 1, 0)
	for n := Commit(1); n <= 9; n++ {
		c.Report("a", 1, n)
	}
	id := c.Wait(4, []ShardID{"a"}, 100)
	res := drainOne(t, c)
	if !res.Granted || res.Req != id || res.View.Commit != 4 {
		t.Fatalf("view must pin commit 4, got %+v", res)
	}
}

// Timeout reports every lagging shard with its gap and never emits a
// partial success.
func TestTimeoutReportsAllLaggingShards(t *testing.T) {
	clock := &ManualClock{}
	c := NewCoordinator(clock)
	c.Recover("a", 1, 0)
	c.Recover("c", 1, 0)
	c.Report("c", 1, 1)
	// shard b: never seen -> progress unknown.

	id := c.Wait(4, []ShardID{"a", "b", "c"}, 10)
	clock.Set(10)
	c.JudgeTimeouts()

	res := drainOne(t, c)
	if res.Granted || res.Req != id || res.Reason != ReasonTimeout {
		t.Fatalf("expected timeout for req %d, got %+v", id, res)
	}
	want := []Lagging{
		{Shard: "a", Known: true, Progress: 0, Gap: 4},
		{Shard: "b", Known: false},
		{Shard: "c", Known: true, Progress: 1, Gap: 3},
	}
	if !reflect.DeepEqual(res.Lagging, want) {
		t.Fatalf("lagging mismatch:\n got %+v\nwant %+v", res.Lagging, want)
	}
	if st := c.Stats(); st.PendingRequests != 0 || st.IndexEntries != 0 {
		t.Fatalf("state leaked after timeout: %+v", st)
	}
}

// Progress applied at the same tick as the deadline wins: events are
// applied first, timeouts judged afterwards.
func TestProgressAtDeadlineTickWins(t *testing.T) {
	clock := &ManualClock{}
	c := NewCoordinator(clock)
	c.Recover("a", 1, 0)
	id := c.Wait(2, []ShardID{"a"}, 10)

	clock.Set(10) // arrive at the deadline tick
	c.Report("a", 1, 1)
	c.Report("a", 1, 2) // progress lands at the same tick
	c.JudgeTimeouts()   // judged only after progress is applied

	res := drainOne(t, c)
	if !res.Granted || res.Req != id {
		t.Fatalf("same-tick progress must beat the deadline, got %+v", res)
	}
}

// The mirror case: without the same-tick progress the request times out.
func TestDeadlineWithoutProgressTimesOut(t *testing.T) {
	clock := &ManualClock{}
	c := NewCoordinator(clock)
	c.Recover("a", 1, 0)
	c.Report("a", 1, 1)
	id := c.Wait(2, []ShardID{"a"}, 10)

	clock.Set(10)
	c.JudgeTimeouts()
	res := drainOne(t, c)
	if res.Granted || res.Req != id || res.Reason != ReasonTimeout {
		t.Fatalf("expected timeout, got %+v", res)
	}
	c.Report("a", 1, 2) // too late; must not produce anything
	drainNothing(t, c, "late progress after timeout")
}

// Duplicate events are idempotent: repeated reports and repeated recovery
// proofs change nothing and produce no extra results.
func TestDuplicateEventsAreIdempotent(t *testing.T) {
	clock := &ManualClock{}
	c := NewCoordinator(clock)
	c.Recover("a", 1, 0)
	c.Recover("a", 1, 0) // duplicate proof
	c.Report("a", 1, 1)
	c.Report("a", 1, 1) // duplicate report
	c.Report("a", 1, 1)
	if st := c.Stats(); st.BufferedReports != 0 {
		t.Fatalf("duplicate reports must not buffer: %+v", st)
	}
	id := c.Wait(1, []ShardID{"a"}, 50)
	res := drainOne(t, c)
	if !res.Granted || res.Req != id {
		t.Fatalf("expected single grant, got %+v", res)
	}
	drainNothing(t, c, "duplicates must not emit extra results")
}

// A restart bumps the epoch. Old-epoch reports cannot advance the new
// instance, and the new instance's progress is unknown until its recovery
// proof arrives; reports buffered before the proof flush afterwards.
func TestRestartEpochAndRecoveryProof(t *testing.T) {
	clock := &ManualClock{}
	c := NewCoordinator(clock)
	c.Recover("a", 1, 0)
	c.Report("a", 1, 1)
	c.Report("a", 1, 2)

	c.Report("a", 2, 5) // restart observed: epoch 2, progress now unknown
	id := c.Wait(1, []ShardID{"a"}, 50)
	drainNothing(t, c, "pre-proof progress must be unknown even below old watermark")

	c.Report("a", 1, 9) // stale epoch: ignored
	c.Report("a", 2, 6) // new epoch but no proof yet: buffered, still unknown
	drainNothing(t, c, "no proof yet")

	c.Recover("a", 2, 4) // proof: base 4, buffered 5 and 6 flush
	res := drainOne(t, c)
	if !res.Granted || res.Req != id || res.View.Commit != 1 {
		t.Fatalf("expected grant pinned at 1 after proof, got %+v", res)
	}

	c.Recover("a", 1, 0) // stale proof for the old epoch: ignored
	id2 := c.Wait(6, []ShardID{"a"}, 50)
	res = drainOne(t, c)
	if !res.Granted || res.Req != id2 || res.View.Commit != 6 {
		t.Fatalf("watermark must be 6 after proof+flush, got %+v", res)
	}
	if st := c.Stats(); st.BufferedReports != 0 {
		t.Fatalf("buffer must be drained after proof: %+v", st)
	}
}

// The wait index is shared: two clients on the same shard, and cancelling
// one removes only its own entries.
func TestSharedIndexAndCancel(t *testing.T) {
	clock := &ManualClock{}
	c := NewCoordinator(clock)
	c.Recover("a", 1, 0)

	r1 := c.Wait(1, []ShardID{"a"}, 100)
	r2 := c.Wait(1, []ShardID{"a"}, 100)
	if st := c.Stats(); st.PendingRequests != 2 || st.IndexEntries != 2 {
		t.Fatalf("expected shared index with 2 entries, got %+v", st)
	}

	if !c.Cancel(r1) {
		t.Fatal("cancel of live request must succeed")
	}
	res := drainOne(t, c)
	if res.Granted || res.Req != r1 || res.Reason != ReasonCancelled {
		t.Fatalf("expected cancel result for r1, got %+v", res)
	}
	if st := c.Stats(); st.PendingRequests != 1 || st.IndexEntries != 1 {
		t.Fatalf("cancel must remove only r1, got %+v", st)
	}
	if c.Cancel(r1) {
		t.Fatal("double cancel must be a no-op")
	}

	c.Report("a", 1, 1)
	res = drainOne(t, c)
	if !res.Granted || res.Req != r2 {
		t.Fatalf("only r2 may be granted, got %+v", res)
	}
	if st := c.Stats(); st.PendingRequests != 0 || st.IndexEntries != 0 {
		t.Fatalf("index must be empty after all requests finish, got %+v", st)
	}
}

// A shard going offline fails the requests waiting on it and must not pin
// already-finished requests in the index.
func TestOfflineFailsWaitersAndReleasesState(t *testing.T) {
	clock := &ManualClock{}
	c := NewCoordinator(clock)
	c.Recover("a", 1, 5)

	done := c.Wait(3, []ShardID{"a"}, 100) // granted immediately
	res := drainOne(t, c)
	if !res.Granted || res.Req != done {
		t.Fatalf("expected immediate grant, got %+v", res)
	}

	stuck := c.Wait(9, []ShardID{"a"}, 100)
	c.Offline("a")
	res = drainOne(t, c)
	if res.Granted || res.Req != stuck || res.Reason != ReasonOffline {
		t.Fatalf("expected offline failure, got %+v", res)
	}
	want := []Lagging{{Shard: "a", Known: false}}
	if !reflect.DeepEqual(res.Lagging, want) {
		t.Fatalf("offline shard must report unknown progress, got %+v", res.Lagging)
	}
	if st := c.Stats(); st.PendingRequests != 0 || st.IndexEntries != 0 {
		t.Fatalf("offline shard must not pin finished requests, got %+v", st)
	}

	// A new wait on an offline shard fails immediately.
	late := c.Wait(1, []ShardID{"a"}, 100)
	res = drainOne(t, c)
	if res.Granted || res.Req != late || res.Reason != ReasonOffline {
		t.Fatalf("wait on offline shard must fail at once, got %+v", res)
	}
}
