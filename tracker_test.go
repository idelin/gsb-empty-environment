package correlator

import (
	"testing"
	"time"
)

// clock 是可手动推进的时钟。
type clock struct{ t time.Time }

func newClock() *clock { return &clock{t: time.Unix(1_700_000_000, 0)} }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestTracker(c *clock, timeout time.Duration, maxPending int) *Tracker {
	return NewTracker(timeout, maxPending, c.now)
}

func lastEvent(t *testing.T, tr *Tracker) Event {
	t.Helper()
	evs := tr.Events()
	if len(evs) == 0 {
		t.Fatal("no events")
	}
	return evs[len(evs)-1]
}

func TestMatchLatencyAndOutOfOrder(t *testing.T) {
	c := newClock()
	tr := newTestTracker(c, 10*time.Second, 8)

	tr.OnRequest(1, 1)
	tr.OnRequest(1, 2)
	tr.OnRequest(1, 3)

	// 乱序响应：seq 3 先回。
	c.advance(2 * time.Second)
	tr.OnResponse(1, 3)
	if e := lastEvent(t, tr); e.Kind != EventMatched || e.Latency != 2*time.Second {
		t.Fatalf("seq3: %+v", e)
	}
	c.advance(3 * time.Second)
	tr.OnResponse(1, 1)
	if e := lastEvent(t, tr); e.Kind != EventMatched || e.Latency != 5*time.Second {
		t.Fatalf("seq1: %+v", e)
	}
	if tr.Pending() != 1 {
		t.Fatalf("pending=%d, want 1", tr.Pending())
	}
	if tr.Stats().Matched != 2 {
		t.Fatalf("matched=%d, want 2", tr.Stats().Matched)
	}
}

func TestDuplicateReplayDoesNotRefreshStart(t *testing.T) {
	c := newClock()
	tr := newTestTracker(c, 30*time.Second, 8)

	tr.OnRequest(1, 1)
	c.advance(5 * time.Second)
	tr.OnRequest(1, 1) // 重放
	if e := lastEvent(t, tr); e.Kind != EventDuplicate {
		t.Fatalf("got %v, want DUPLICATE", e)
	}
	if tr.Pending() != 1 {
		t.Fatalf("pending=%d, want 1 (重放不得新建挂起项)", tr.Pending())
	}
	c.advance(4 * time.Second)
	tr.OnResponse(1, 1)
	// 延迟必须从原始开始时间 9s 计，而非重放时刻 4s。
	if e := lastEvent(t, tr); e.Kind != EventMatched || e.Latency != 9*time.Second {
		t.Fatalf("latency=%v, want 9s from original start", e.Latency)
	}
}

func TestTimeoutThenLateResponse(t *testing.T) {
	c := newClock()
	tr := newTestTracker(c, 10*time.Second, 8)

	tr.OnRequest(1, 1)
	c.advance(11 * time.Second)
	tr.Sweep()
	if e := lastEvent(t, tr); e.Kind != EventTimeout {
		t.Fatalf("got %v, want TIMEOUT", e)
	}
	// 超时后的响应记为迟到，而非新的成功。
	c.advance(time.Second)
	tr.OnResponse(1, 1)
	e := lastEvent(t, tr)
	if e.Kind != EventLate || e.Latency != 12*time.Second {
		t.Fatalf("got %+v, want LATE latency=12s", e)
	}
	s := tr.Stats()
	if s.Matched != 0 || s.Late != 1 || s.Timeouts != 1 {
		t.Fatalf("stats=%+v", s)
	}
}

func TestOrphanResponse(t *testing.T) {
	c := newClock()
	tr := newTestTracker(c, 10*time.Second, 8)
	tr.OnResponse(1, 99)
	if e := lastEvent(t, tr); e.Kind != EventOrphan {
		t.Fatalf("got %v, want ORPHAN", e)
	}
}

func TestOldEpochResponseCannotMatchNewRequest(t *testing.T) {
	c := newClock()
	tr := newTestTracker(c, 30*time.Second, 8)

	tr.OnRequest(2, 1) // 新 epoch 复用了 seq 1
	c.advance(time.Second)
	tr.OnResponse(1, 1) // 旧 epoch 迟到响应
	if e := lastEvent(t, tr); e.Kind != EventOrphan {
		t.Fatalf("old-epoch response: got %v, want ORPHAN", e)
	}
	if tr.Pending() != 1 {
		t.Fatalf("新请求被旧响应错误配对，pending=%d", tr.Pending())
	}
	tr.OnResponse(2, 1)
	if e := lastEvent(t, tr); e.Kind != EventMatched {
		t.Fatalf("got %v, want MATCHED", e)
	}
}

func TestSeqReuseAfterCompletion(t *testing.T) {
	c := newClock()
	tr := newTestTracker(c, 30*time.Second, 8)

	tr.OnRequest(1, 1)
	c.advance(time.Second)
	tr.OnResponse(1, 1)
	// 同 epoch 内序号复用：是全新请求而非重复。
	tr.OnRequest(1, 1)
	if n := tr.Stats().Duplicates; n != 0 {
		t.Fatalf("duplicates=%d, want 0", n)
	}
	c.advance(2 * time.Second)
	tr.OnResponse(1, 1)
	if e := lastEvent(t, tr); e.Kind != EventMatched || e.Latency != 2*time.Second {
		t.Fatalf("reused seq: %+v, want latency=2s", e)
	}
}

func TestBoundedStorageEviction(t *testing.T) {
	c := newClock()
	tr := newTestTracker(c, time.Minute, 2)

	tr.OnRequest(1, 1)
	tr.OnRequest(1, 2)
	tr.OnRequest(1, 3) // 超界，驱逐最旧的 seq 1
	if e := lastEvent(t, tr); e.Kind != EventEvicted || e.Seq != 1 {
		t.Fatalf("got %+v, want EVICTED seq=1", e)
	}
	if tr.Pending() != 2 {
		t.Fatalf("pending=%d, want 2", tr.Pending())
	}
	// 被驱逐者的响应成为孤立消息。
	tr.OnResponse(1, 1)
	if e := lastEvent(t, tr); e.Kind != EventOrphan {
		t.Fatalf("got %v, want ORPHAN", e)
	}
}

func TestCheckpointRestore(t *testing.T) {
	c := newClock()
	tr := newTestTracker(c, 30*time.Second, 4)

	tr.OnRequest(1, 1)
	tr.OnRequest(1, 2)
	c.advance(3 * time.Second)
	tr.OnResponse(1, 1) // 先完成一个，统计随 checkpoint 保留

	data, err := tr.Checkpoint()
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	c.advance(5 * time.Second)
	restored, err := Restore(data, c.now)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored.Pending() != 1 {
		t.Fatalf("pending=%d, want 1", restored.Pending())
	}
	// 延迟跨越 checkpoint 连续：3s(checkpoint 前) + 5s + 2s。
	c.advance(2 * time.Second)
	restored.OnResponse(1, 2)
	if e := lastEvent(t, restored); e.Kind != EventMatched || e.Latency != 10*time.Second {
		t.Fatalf("after restore: %+v, want MATCHED latency=10s", e)
	}
	if s := restored.Stats(); s.Matched != 2 || s.Requests != 2 {
		t.Fatalf("stats after restore=%+v", s)
	}
}

func TestCheckpointPreservesEvictionOrder(t *testing.T) {
	c := newClock()
	tr := newTestTracker(c, time.Minute, 2)
	tr.OnRequest(1, 1)
	tr.OnRequest(1, 2)

	data, err := tr.Checkpoint()
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	restored, err := Restore(data, c.now)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	restored.OnRequest(1, 3) // 应驱逐最旧的 seq 1
	if e := lastEvent(t, restored); e.Kind != EventEvicted || e.Seq != 1 {
		t.Fatalf("got %+v, want EVICTED seq=1", e)
	}
}

func TestRestoreRejectsCorruptCheckpoint(t *testing.T) {
	if _, err := Restore([]byte("not a checkpoint"), nil); err == nil {
		t.Fatal("want error for corrupt checkpoint")
	}
}
