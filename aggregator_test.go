package aggregator

import (
	"errors"
	"path/filepath"
	"testing"
)

// ---- 测试用具 ----

type sinkFunc func(res Result) error

func (f sinkFunc) Publish(res Result) error { return f(res) }

// flakyStore 包装 Store，可注入“接下来 N 次 Save 失败”，
// 用于模拟检查点写入中途崩溃。
type flakyStore struct {
	inner     Store
	failSaves int
}

func (s *flakyStore) Save(cp Checkpoint) error {
	if s.failSaves > 0 {
		s.failSaves--
		return errors.New("injected save failure")
	}
	return s.inner.Save(cp)
}

func (s *flakyStore) Load() (Checkpoint, error) { return s.inner.Load() }

func newStore(t *testing.T) *FileStore {
	t.Helper()
	return NewFileStore(filepath.Join(t.TempDir(), "checkpoint.json"))
}

// reopen 模拟崩溃重启：丢弃内存态，从同一存储恢复。
func reopen(t *testing.T, store Store, sink Sink, emitEvery int64) *Aggregator {
	t.Helper()
	a, err := Open(store, sink, emitEvery)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	return a
}

// publishAndAckAll 发布全部待发结果并逐条确认。
func publishAndAckAll(t *testing.T, a *Aggregator) {
	t.Helper()
	if err := a.PublishPending(); err != nil {
		t.Fatalf("publish: %v", err)
	}
	for _, id := range a.Progress().Effect.PendingIDs {
		if err := a.Ack(id); err != nil {
			t.Fatalf("ack %s: %v", id, err)
		}
	}
}

// ---- 故障点 1：结算落盘后、发布前崩溃 ----

func TestCrashAfterCheckpointBeforePublish(t *testing.T) {
	store := newStore(t)
	recv := NewReceiver()

	a, err := Open(store, recv, 2)
	if err != nil {
		t.Fatal(err)
	}
	// 两条事件触发一次结算：结果已进入 Pending 并落盘，
	// 但尚未发布（模拟此刻断电）。
	if err := a.Add("k", 3); err != nil {
		t.Fatal(err)
	}
	if err := a.Add("k", 4); err != nil {
		t.Fatal(err)
	}
	if got := recv.UniqueCount(); got != 0 {
		t.Fatalf("receiver should be empty before publish, got %d", got)
	}

	// 崩溃重启：计算状态恢复，重发 Pending 中的原结果。
	a2 := reopen(t, store, recv, 2)
	p := a2.Progress()
	if !p.Compute.RestoredFromCheckpoint {
		t.Fatal("compute state should be restored from checkpoint")
	}
	if p.Compute.Sums["k"] != 7 {
		t.Fatalf("restored sum = %d, want 7", p.Compute.Sums["k"])
	}
	if p.Effect.Pending != 1 {
		t.Fatalf("pending = %d, want 1", p.Effect.Pending)
	}
	publishAndAckAll(t, a2)

	if got := recv.Total("k"); got != 7 {
		t.Fatalf("receiver total = %d, want 7", got)
	}
	if got := recv.UniqueCount(); got != 1 {
		t.Fatalf("unique results = %d, want 1", got)
	}
}

// ---- 故障点 2：发布后、确认前崩溃（重发同 ID，不重复计数）----

func TestCrashAfterPublishBeforeAck(t *testing.T) {
	store := newStore(t)
	recv := NewReceiver()

	a, err := Open(store, recv, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Add("k", 10); err != nil {
		t.Fatal(err)
	}
	if err := a.PublishPending(); err != nil {
		t.Fatal(err)
	}
	// 结果已送达接收方，但 Ack 尚未发生即崩溃。
	crashID := a.Progress().Effect.PendingIDs[0]

	// 重启恢复：Pending 仍在，必须重发同一 ID。
	a2 := reopen(t, store, recv, 1)
	ids := a2.Progress().Effect.PendingIDs
	if len(ids) != 1 || ids[0] != crashID {
		t.Fatalf("recovered pending IDs = %v, want [%s]", ids, crashID)
	}
	if err := a2.PublishPending(); err != nil {
		t.Fatal(err)
	}

	// 幂等接收：同 ID 第二次投递被去重，计数不变。
	if got := recv.Total("k"); got != 10 {
		t.Fatalf("receiver total = %d, want 10 (no double count)", got)
	}
	if got := recv.UniqueCount(); got != 1 {
		t.Fatalf("unique results = %d, want 1", got)
	}
	if got := recv.DuplicateCalls(); got != 1 {
		t.Fatalf("duplicate deliveries = %d, want 1", got)
	}
	publishAndAckAll(t, a2)
	if p := a2.Progress(); p.Effect.Confirmed != 1 || p.Effect.Pending != 0 {
		t.Fatalf("effect progress = %+v, want confirmed=1 pending=0", p.Effect)
	}
}

// ---- 故障点 3：确认后、确认落盘前崩溃 ----

func TestCrashAfterAckBeforeCheckpoint(t *testing.T) {
	base := newStore(t)
	store := &flakyStore{inner: base}
	recv := NewReceiver()

	a, err := Open(store, recv, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Add("k", 5); err != nil {
		t.Fatal(err)
	}
	if err := a.PublishPending(); err != nil {
		t.Fatal(err)
	}
	id := a.Progress().Effect.PendingIDs[0]

	// Ack 时检查点写入失败：确认未持久化，相当于崩溃。
	store.failSaves = 1
	if err := a.Ack(id); err == nil {
		t.Fatal("ack should report checkpoint save failure")
	}

	// 重启：旧检查点里该结果仍 Pending，重发同 ID。
	a2 := reopen(t, store, recv, 1)
	if err := a2.PublishPending(); err != nil {
		t.Fatal(err)
	}
	if got := recv.Total("k"); got != 5 {
		t.Fatalf("receiver total = %d, want 5 (no double count)", got)
	}
	if got := recv.DuplicateCalls(); got != 1 {
		t.Fatalf("duplicate deliveries = %d, want 1", got)
	}
	// 重新确认，最终一致。
	publishAndAckAll(t, a2)
	if p := a2.Progress(); p.Effect.Confirmed != 1 {
		t.Fatalf("confirmed = %d, want 1", p.Effect.Confirmed)
	}
}

// ---- 故障点 4：输入流中途崩溃，计算状态恢复 ----

func TestComputeStateRecoveryMidStream(t *testing.T) {
	store := newStore(t)
	recv := NewReceiver()

	a, err := Open(store, recv, 3)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ { // 7 条事件：第 3、6 条各结算一次，第 7 条只在内存/检查点
		if err := a.Add("k", 1); err != nil {
			t.Fatal(err)
		}
	}
	before := a.Snapshot()

	// 崩溃重启：累计值与输入进度必须精确还原。
	a2 := reopen(t, store, recv, 3)
	p := a2.Progress()
	if p.Compute.InputSeq != before.InputSeq {
		t.Fatalf("input seq = %d, want %d", p.Compute.InputSeq, before.InputSeq)
	}
	if p.Compute.Sums["k"] != 7 {
		t.Fatalf("restored sum = %d, want 7", p.Compute.Sums["k"])
	}
	// 继续输入不丢不重：再 2 条触发第三次结算，总量 9。
	if err := a2.Add("k", 1); err != nil {
		t.Fatal(err)
	}
	if err := a2.Add("k", 1); err != nil {
		t.Fatal(err)
	}
	publishAndAckAll(t, a2)
	if got := recv.Total("k"); got != 9 {
		t.Fatalf("receiver total = %d, want 9", got)
	}
	if got := recv.UniqueCount(); got != 3 {
		t.Fatalf("unique results = %d, want 3", got)
	}
}

// ---- 故障点 5：检查点保存失败，旧检查点完好 ----

func TestCheckpointSaveFailureKeepsOldState(t *testing.T) {
	base := newStore(t)
	store := &flakyStore{inner: base}
	recv := NewReceiver()

	a, err := Open(store, recv, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Add("k", 2); err != nil {
		t.Fatal(err)
	}
	publishAndAckAll(t, a)

	// 下一次写入失败：操作报错，内存态回滚，盘上仍是旧检查点。
	store.failSaves = 1
	if err := a.Add("k", 100); err == nil {
		t.Fatal("add should report save failure")
	}
	cp, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cp.Sums["k"] != 2 {
		t.Fatalf("on-disk sum = %d, want 2 (old checkpoint intact)", cp.Sums["k"])
	}

	// 重启后从完好检查点继续，重放该事件即可恢复。
	a2 := reopen(t, store, recv, 1)
	if err := a2.Add("k", 100); err != nil {
		t.Fatal(err)
	}
	publishAndAckAll(t, a2)
	if got := recv.Total("k"); got != 102 {
		t.Fatalf("receiver total = %d, want 102", got)
	}
}

// ---- 端到端：多轮崩溃恢复，全程不重复计数 ----

func TestEndToEndNoDoubleCountAcrossCrashes(t *testing.T) {
	store := newStore(t)
	recv := NewReceiver()

	const events = 50
	var inputTotal int64

	a, err := Open(store, recv, 4)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < events; i++ {
		if err := a.Add("k", 2); err != nil {
			t.Fatal(err)
		}
		inputTotal += 2
		// 每 7 条事件模拟一次“发布后未确认即崩溃”。
		if i%7 == 3 {
			if err := a.PublishPending(); err != nil {
				t.Fatal(err)
			}
			a = reopen(t, store, recv, 4)
			if err := a.PublishPending(); err != nil { // 恢复重发同 ID
				t.Fatal(err)
			}
			publishAndAckAll(t, a)
		}
	}
	if err := a.Flush(); err != nil {
		t.Fatal(err)
	}
	publishAndAckAll(t, a)

	if got := recv.Total("k"); got != inputTotal {
		t.Fatalf("receiver total = %d, want %d (no double count)", got, inputTotal)
	}
	p := a.Progress()
	if got := recv.UniqueCount(); got != int(p.Effect.Emitted) {
		t.Fatalf("unique results = %d, want %d", got, p.Effect.Emitted)
	}
	if p.Effect.Pending != 0 || p.Effect.Confirmed != p.Effect.Emitted {
		t.Fatalf("effect progress not settled: %+v", p.Effect)
	}
	if recv.DuplicateCalls() == 0 {
		t.Fatal("expected duplicate deliveries from recovery republish")
	}
}

// ---- 结果 ID 稳定性：同一逻辑结果跨恢复绝不换 ID ----

func TestResultIDStableAcrossRecovery(t *testing.T) {
	store := newStore(t)
	var published []string
	sink := sinkFunc(func(res Result) error {
		published = append(published, res.ID)
		return nil
	})

	a, err := Open(store, sink, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Add("k", 1); err != nil {
		t.Fatal(err)
	}
	if err := a.PublishPending(); err != nil {
		t.Fatal(err)
	}
	// 崩溃恢复后重发，ID 必须与首次发布完全一致。
	a2 := reopen(t, store, sink, 1)
	if err := a2.PublishPending(); err != nil {
		t.Fatal(err)
	}
	if len(published) != 2 || published[0] != published[1] {
		t.Fatalf("republished IDs = %v, want identical pair", published)
	}
}
