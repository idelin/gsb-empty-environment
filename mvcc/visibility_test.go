package mvcc

import (
	"errors"
	"testing"
)

func tupleWith(xmin, xmax XID, mask uint16) *Tuple {
	return &Tuple{Xmin: xmin, Xmax: xmax, Infomask: mask}
}

func multiTuple(xmin XID, id MultiXactId) *Tuple {
	return tupleWith(xmin, XID(id), HeapXmaxIsMulti)
}

func classify(t *testing.T, tuple *Tuple, oldest XID, clog TxnStatusProvider, mx *MultiXactStore) TupleVerdict {
	t.Helper()
	verdict, err := ClassifyTuple(tuple, oldest, clog, mx)
	if err != nil {
		t.Fatalf("ClassifyTuple failed: %v", err)
	}
	return verdict
}

func TestClassifyXminAbortedIsDead(t *testing.T) {
	clog := NewClog()
	clog.Set(10, StatusAborted)
	tuple := tupleWith(10, InvalidXid, 0)

	if verdict := classify(t, tuple, 20, clog, nil); verdict != VerdictDead {
		t.Fatalf("aborted xmin verdict = %s, want DEAD", verdict)
	}
	if !tuple.HasMask(HeapXminInvalid) {
		t.Fatal("aborted xmin must set HEAP_XMIN_INVALID hint")
	}
}

func TestClassifyFrozenAndBootstrapXmin(t *testing.T) {
	clog := NewClog()
	for _, xmin := range []XID{FrozenXid, BootstrapXid} {
		tuple := tupleWith(xmin, InvalidXid, 0)
		if verdict := classify(t, tuple, 100, clog, nil); verdict != VerdictLive {
			t.Fatalf("xmin %d verdict = %s, want LIVE", xmin, verdict)
		}
	}

	frozen := tupleWith(FrozenXid, InvalidXid, 0)
	classify(t, frozen, 100, clog, nil)
	if !frozen.HasMask(HeapXminFrozen) {
		t.Fatal("FrozenXid tuple must carry the frozen hint bit combination")
	}
}

func TestClassifyPlainXmax(t *testing.T) {
	clog := NewClog()
	clog.Set(5, StatusCommitted)  // 存活元组的插入事务
	clog.Set(10, StatusCommitted) // 已提交删除者（相对 old oldest 已死）
	clog.Set(90, StatusCommitted) // 已提交删除者（相对新 oldest 仍近期死）
	clog.Set(11, StatusAborted)   // 中止的删除者

	oldDelete := tupleWith(5, 10, 0)
	if v := classify(t, oldDelete, 100, clog, nil); v != VerdictDead {
		t.Fatalf("old committed delete = %s, want DEAD", v)
	}
	if !oldDelete.HasMask(HeapXmaxCommitted) {
		t.Fatal("committed xmax must set HEAP_XMAX_COMMITTED")
	}

	// 删除事务 90 不早于 OldestXmin=80，部分活跃快照仍可能看到元组。
	recentDelete := tupleWith(5, 90, 0)
	if v := classify(t, recentDelete, 80, clog, nil); v != VerdictRecentDead {
		t.Fatalf("recent committed delete = %s, want RECENTLY_DEAD", v)
	}

	abortedDelete := tupleWith(5, 11, 0)
	if v := classify(t, abortedDelete, 100, clog, nil); v != VerdictLive {
		t.Fatalf("aborted delete = %s, want LIVE", v)
	}
	if !abortedDelete.HasMask(HeapXmaxInvalid) {
		t.Fatal("aborted xmax must set HEAP_XMAX_INVALID")
	}

	running := tupleWith(5, 120, 0)
	if v := classify(t, running, 100, clog, nil); v != VerdictLive {
		t.Fatalf("in-progress delete = %s, want LIVE", v)
	}

	// 纯行锁持有者即使已提交也不能杀死元组。
	locker := tupleWith(5, 10, HeapXmaxIsLocker|HeapXmaxSharedLock)
	if v := classify(t, locker, 100, clog, nil); v != VerdictLive {
		t.Fatalf("committed locker = %s, want LIVE", v)
	}
}

func TestClassifyInsertInProgressProtected(t *testing.T) {
	clog := NewClog() // 所有事务默认运行中
	tuple := tupleWith(200, InvalidXid, 0)

	if v := classify(t, tuple, 100, clog, nil); v != VerdictInsertInProgress {
		t.Fatalf("running xmin = %s, want INSERT_IN_PROGRESS", v)
	}
}

func TestClassifyMultiLockOnlyNeverDead(t *testing.T) {
	clog := NewClog()
	clog.Set(5, StatusCommitted)
	clog.Set(10, StatusCommitted)
	clog.Set(11, StatusCommitted)
	store := NewMultiXactStore()
	id, err := store.Create([]MultiXactMember{
		{Xid: 10, Status: MultiXactStatusForKeyShare},
		{Xid: 11, Status: MultiXactStatusForShare},
	})
	if err != nil {
		t.Fatal(err)
	}

	tuple := multiTuple(5, id)
	if v := classify(t, tuple, 100, clog, store); v != VerdictLive {
		t.Fatalf("lock-only MultiXact = %s, want LIVE", v)
	}
}

func TestClassifyMultiAbortedUpdaterKeepsTuple(t *testing.T) {
	clog := NewClog()
	clog.Set(5, StatusCommitted)
	clog.Set(10, StatusCommitted) // 锁持有者
	clog.Set(11, StatusAborted)   // 中止的 UPDATE
	store := NewMultiXactStore()
	id, err := store.Create([]MultiXactMember{
		{Xid: 10, Status: MultiXactStatusForShare},
		{Xid: 11, Status: MultiXactStatusForUpdate},
	})
	if err != nil {
		t.Fatal(err)
	}

	tuple := multiTuple(5, id)
	if v := classify(t, tuple, 100, clog, store); v != VerdictLive {
		t.Fatalf("aborted updater in MultiXact = %s, want LIVE", v)
	}
}

func TestClassifyMultiCommittedUpdater(t *testing.T) {
	clog := NewClog()
	clog.Set(5, StatusCommitted)
	clog.Set(10, StatusCommitted) // 锁持有者
	clog.Set(11, StatusCommitted) // 已提交 DELETE
	store := NewMultiXactStore()
	id, err := store.Create([]MultiXactMember{
		{Xid: 10, Status: MultiXactStatusForKeyShare},
		{Xid: 11, Status: MultiXactStatusForDelete},
	})
	if err != nil {
		t.Fatal(err)
	}

	oldTuple := multiTuple(5, id)
	if v := classify(t, oldTuple, 100, clog, store); v != VerdictDead {
		t.Fatalf("old committed deleter in MultiXact = %s, want DEAD", v)
	}

	recentTuple := multiTuple(5, id)
	if v := classify(t, recentTuple, 11, clog, store); v != VerdictRecentDead {
		t.Fatalf("recent committed deleter in MultiXact = %s, want RECENTLY_DEAD", v)
	}
}

func TestClassifyMultiRunningMembersProtected(t *testing.T) {
	clog := NewClog()
	clog.Set(5, StatusCommitted)
	clog.Set(11, StatusCommitted) // 提交的更新者
	store := NewMultiXactStore()

	// 运行中的锁成员：整组保守视为存活，禁止误删。
	withLocker, err := store.Create([]MultiXactMember{
		{Xid: 10, Status: MultiXactStatusForShare}, // 运行中
		{Xid: 11, Status: MultiXactStatusForUpdate},
	})
	if err != nil {
		t.Fatal(err)
	}
	tuple := multiTuple(5, withLocker)
	if v := classify(t, tuple, 100, clog, store); v != VerdictLive {
		t.Fatalf("running locker must protect tuple, got %s", v)
	}

	// 运行中的更新者同样保护元组。
	runningUpdater, err := store.Create([]MultiXactMember{
		{Xid: 12, Status: MultiXactStatusForUpdate}, // 运行中
	})
	if err != nil {
		t.Fatal(err)
	}
	tuple2 := multiTuple(5, runningUpdater)
	if v := classify(t, tuple2, 100, clog, store); v != VerdictLive {
		t.Fatalf("running updater must protect tuple, got %s", v)
	}
}

func TestClassifyMultiMissingStoreAndId(t *testing.T) {
	clog := NewClog()
	clog.Set(5, StatusCommitted)
	tuple := multiTuple(5, FirstMultiXactId)

	if _, err := ClassifyTuple(tuple, 100, clog, nil); err == nil {
		t.Fatal("multi tuple without store must return an error")
	}
	if _, err := ClassifyTuple(tuple, 100, clog, NewMultiXactStore()); !errors.Is(err, ErrInvalidMultiXact) {
		t.Fatalf("missing MultiXact must return ErrInvalidMultiXact, got %v", err)
	}
	if _, err := ClassifyTuple(nil, 100, NewClog(), nil); err == nil {
		t.Fatal("nil tuple must return an error")
	}
}
