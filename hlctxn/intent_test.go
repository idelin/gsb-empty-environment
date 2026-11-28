package hlctxn

import (
	"errors"
	"testing"
)

func newLazyCluster(t *testing.T, n int) *Cluster {
	t.Helper()
	base := NewSimPhysicalClock(1000)
	phys := make([]PhysicalClock, n)
	for i := range phys {
		phys[i] = SkewedPhysicalClock{Base: base}
	}
	c := NewCluster(phys, base, testMaxOffset)
	c.LazyResolution = true
	return c
}

func TestIntentResolution_CommittedAndAborted(t *testing.T) {
	c := newLazyCluster(t, 3)
	commitKey := keyOnNode(c, 1, "ic")
	abortKey := keyOnNode(c, 2, "ia")

	committer := c.Begin(1, 0)
	if err := committer.Put(commitKey, "committed-value"); err != nil {
		t.Fatal(err)
	}
	commitTS, err := committer.Commit()
	if err != nil {
		t.Fatal(err)
	}

	aborter := c.Begin(2, 0)
	if err := aborter.Put(abortKey, "aborted-value"); err != nil {
		t.Fatal(err)
	}
	if err := aborter.Abort(); err != nil {
		t.Fatal(err)
	}

	// 懒解析模式：提交/中止后意图仍在，等待后来者原地解析。
	left := c.PendingIntents()
	if len(left) != 2 {
		t.Fatalf("expected 2 lingering intents, got %d", len(left))
	}

	reader := c.Begin(0, 0)
	// 撞上已提交意图：按真实提交戳解析并读到值。
	value, found, err := reader.Get(commitKey)
	if err != nil || !found || value != "committed-value" {
		t.Fatalf("committed intent resolve: %q %v %v", value, found, err)
	}
	view, _ := c.LookupTxn(committer.ID())
	if view.Status != Committed || view.CommitTS.Compare(commitTS) != 0 {
		t.Fatalf("committed record wrong: %+v", view)
	}
	// 撞上已中止意图：忽略清理，键视为不存在。
	_, found, err = reader.Get(abortKey)
	if err != nil || found {
		t.Fatalf("aborted intent should be dropped: found=%v err=%v", found, err)
	}
	if remaining := c.PendingIntents(); len(remaining) != 0 {
		t.Fatalf("all intents should be resolved, got %d", len(remaining))
	}
	if c.StatsSnapshot().IntentResolved != 2 {
		t.Fatalf("resolved count = %d, want 2", c.StatsSnapshot().IntentResolved)
	}

	// 再次读不再有任何副作用，版本以统一 commitTS 对外可见。
	value, found, _ = reader.Get(commitKey)
	if !found || value != "committed-value" {
		t.Fatal("post-resolution read unstable")
	}
}

func TestStateMachine_OneWayTransitionsAndCommitBound(t *testing.T) {
	c := newLazyCluster(t, 2)
	key := keyOnNode(c, 0, "sm")

	txn := c.Begin(0, 0)
	if err := txn.Put(key, "x"); err != nil {
		t.Fatal(err)
	}

	// 高优读者推戳：被推高的意图戳必须成为提交戳下界。
	pusher := c.Begin(1, 10)
	if _, _, err := pusher.Get(key); err != nil {
		t.Fatalf("push get: %v", err)
	}
	rec, _ := c.LookupTxn(txn.ID())
	if rec.Status != Pending {
		t.Fatalf("pushed txn must stay PENDING, got %s", rec.Status)
	}
	if c.StatsSnapshot().TimestampPushes == 0 {
		t.Fatal("expected at least one timestamp push")
	}

	commitTS, err := txn.Commit()
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if commitTS.Less(rec.WriteTS) {
		t.Fatalf("commitTS %+v below max pushed intent ts %+v", commitTS, rec.WriteTS)
	}
	rec, _ = c.LookupTxn(txn.ID())
	if rec.Status != Committed {
		t.Fatalf("status = %s, want COMMITTED", rec.Status)
	}

	// 单向迁移：COMMITTED 后再写/再中止必须被拒绝。
	if err := txn.Put(key, "y"); !errors.Is(err, ErrCommitted) {
		t.Fatalf("post-commit put err = %v, want ErrCommitted", err)
	}
	if err := txn.Abort(); err != nil {
		t.Fatalf("abort on committed txn should be no-op, got %v", err)
	}
	rec, _ = c.LookupTxn(txn.ID())
	if rec.Status != Committed {
		t.Fatal("committed state must never transition back")
	}

	// ABORTED 同样终态。
	victim := c.Begin(0, 0)
	_ = victim.Put(keyOnNode(c, 1, "victim"), "z")
	if err := victim.Abort(); err != nil {
		t.Fatal(err)
	}
	if _, err := victim.Commit(); !errors.Is(err, ErrAborted) {
		t.Fatalf("commit aborted txn err = %v, want ErrAborted", err)
	}
}

func TestPriorityPush_HighPriorityReaderPushes(t *testing.T) {
	c := newLazyCluster(t, 2)
	key := keyOnNode(c, 0, "push")

	low := c.Begin(0, 1)
	if err := low.Put(key, "low"); err != nil {
		t.Fatal(err)
	}
	lowBefore := low.ReadTS()

	high := c.Begin(1, 100)
	_, _, err := high.Get(key)
	if err != nil {
		t.Fatalf("high reader should push, got %v", err)
	}
	rec, _ := c.LookupTxn(low.ID())
	if rec.Status != Pending || !rec.WriteTS.Greater(lowBefore) {
		t.Fatalf("push did not advance low txn: status=%s writeTS=%+v before=%+v",
			rec.Status, rec.WriteTS, lowBefore)
	}
	// 低优事务以被推高后的戳提交，读者（同 maxTS 窗口）最终可见。
	commitTS, err := low.Commit()
	if err != nil || !commitTS.Greater(lowBefore) {
		t.Fatalf("pushed commit wrong: ts=%+v err=%v", commitTS, err)
	}
	value, found, _, err := high.GetWithRestart(key)
	if err != nil || !found || value != "low" {
		t.Fatalf("reader after pushed commit: %q %v %v", value, found, err)
	}
}

func TestPriorityConflict_LowPriorityWriterAborted(t *testing.T) {
	c := newLazyCluster(t, 2)
	key := keyOnNode(c, 0, "hot")

	holder := c.Begin(0, 1)
	if err := holder.Put(key, "holder"); err != nil {
		t.Fatal(err)
	}

	// 低优写者撞高优 PENDING 意图：收到 ConflictError，事务记录保持 PENDING。
	lowWriter := c.Begin(1, 0)
	err := lowWriter.Put(key, "intruder")
	var cf *ConflictError
	if !errors.As(err, &cf) {
		t.Fatalf("expected ConflictError, got %v", err)
	}
	if cf.OtherTxn != holder.ID() {
		t.Fatalf("conflict points at txn %d, want %d", cf.OtherTxn, holder.ID())
	}

	// 高优写者直接中止低优持有者并接管键。
	highWriter := c.Begin(1, 100)
	if err := highWriter.Put(key, "winner"); err != nil {
		t.Fatalf("high writer should abort holder: %v", err)
	}
	rec, _ := c.LookupTxn(holder.ID())
	if rec.Status != Aborted {
		t.Fatalf("holder status = %s, want ABORTED", rec.Status)
	}
	if _, err := highWriter.Commit(); err != nil {
		t.Fatal(err)
	}

	// 被中止事务继续写必须失败。
	if err := holder.Put(keyOnNode(c, 1, "other"), "x"); !errors.Is(err, ErrAborted) {
		t.Fatalf("aborted txn put err = %v, want ErrAborted", err)
	}
}

// TestPendingIntent_OverlapDeterministic 手工构造多个同时 PENDING 的意图，
// 确定性验证推戳、中止、未来意图忽略与冲突退避，不依赖 goroutine 调度。
func TestPendingIntent_OverlapDeterministic(t *testing.T) {
	c := newLazyCluster(t, 3)
	key := keyOnNode(c, 0, "overlap")

	// 低优写者撞上高优持有者的 PENDING 意图，必须收到 ConflictError 并退避。
	holderA := c.Begin(0, 1)
	if err := holderA.Put(key, "a"); err != nil {
		t.Fatal(err)
	}
	holderB := c.Begin(1, 0)
	err := holderB.Put(key, "b")
	var cf *ConflictError
	if !errors.As(err, &cf) {
		t.Fatalf("lower-priority holderB should get ConflictError, got %v", err)
	}

	// 高优读者推戳 holderA，不中止它。
	highReader := c.Begin(2, 100)
	writeTSBefore, _ := c.LookupTxn(holderA.ID())
	if _, _, err := highReader.Get(key); err != nil {
		t.Fatalf("high reader push: %v", err)
	}
	after, _ := c.LookupTxn(holderA.ID())
	if after.Status != Pending || !after.WriteTS.Greater(writeTSBefore.WriteTS) {
		t.Fatalf("push failed: before=%+v after=%+v", writeTSBefore, after)
	}

	// 高优写者中止 holderA 并接管键。
	highWriter := c.Begin(2, 200)
	if err := highWriter.Put(key, "winner"); err != nil {
		t.Fatalf("high writer abort+takeover: %v", err)
	}
	rec, _ := c.LookupTxn(holderA.ID())
	if rec.Status != Aborted {
		t.Fatalf("holderA should be ABORTED, got %s", rec.Status)
	}

	// 被推高的提交戳下界：高优写者提交戳 >= 其 readTS。
	commitTS, err := highWriter.Commit()
	if err != nil || commitTS.Less(highWriter.ReadTS()) {
		t.Fatalf("winner commit wrong: ts=%+v err=%v", commitTS, err)
	}

	// holderB 此前冲突失败，可正常中止且不影响胜者。
	if err := holderB.Abort(); err != nil {
		t.Fatal(err)
	}

	// 新读者解析残留意图后读到胜者的值。
	observer := c.Begin(0, 0)
	value, found, _, err := observer.GetWithRestart(key)
	if err != nil || !found || value != "winner" {
		t.Fatalf("observer: value=%q found=%v err=%v", value, found, err)
	}

	stats := c.StatsSnapshot()
	if stats.TimestampPushes == 0 || stats.TxnAborts == 0 {
		t.Fatalf("expected both pushes and aborts, got %+v", stats)
	}
}

func TestEqualPriority_NoDeadlockTotalOrder(t *testing.T) {
	c := newLazyCluster(t, 3)

	// 两个键、两个同优事务互相交叉访问：ID 全序保证总有确定的胜者，
	// 在有界重试内全部完成，无循环等待。
	keyA := keyOnNode(c, 0, "eqA")
	keyB := keyOnNode(c, 1, "eqB")

	body := func(txn *Transaction, ownKey, otherKey, val string) error {
		if err := txn.Put(ownKey, val); err != nil {
			return err
		}
		if err := txn.Put(otherKey, val+"-2"); err != nil {
			return err
		}
		return nil
	}

	errs := make(chan error, 2)
	go func() {
		_, err := c.RunWithRetry(0, 5, 0, func(txn *Transaction) error {
			return body(txn, keyA, keyB, "a")
		})
		errs <- err
	}()
	go func() {
		_, err := c.RunWithRetry(1, 5, 0, func(txn *Transaction) error {
			return body(txn, keyB, keyA, "b")
		})
		errs <- err
	}()
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("equal-priority txn %d failed: %v", i, err)
		}
	}

	// 最终两个键都有已提交版本；懒解析模式下残留意图可被显式 Drain 全部解析。
	if n := c.DrainIntents(); n > 2 {
		t.Fatalf("drained %d intents, at most 2 keys expected", n)
	}
	if len(c.PendingIntents()) != 0 {
		t.Fatalf("lingering intents after drain: %d", len(c.PendingIntents()))
	}
	if c.StatsSnapshot().TxnCommits != 2 {
		t.Fatalf("commits = %d, want 2", c.StatsSnapshot().TxnCommits)
	}
}
