package mvcc

import (
	"errors"
	"testing"
)

func defaultCutoffs() Cutoffs {
	return Cutoffs{OldestXmin: 100, FreezeLimit: 50, MultiXactCutoff: 1000}
}

func TestVacuumWraparoundFreeze(t *testing.T) {
	clog := NewClog()
	clog.Set(MaxXid, StatusCommitted)
	clog.Set(MaxXid-1, StatusCommitted)
	clog.Set(3, StatusCommitted)
	clog.Set(40, StatusCommitted)

	// OldestXmin/FreezeLimit 回绕到低值：接近 2^32-1 的老事务在环形
	// 时间线上早于阈值，必须冻结；阈值之内的新事务保持原样。
	cutoffs := Cutoffs{OldestXmin: 10, FreezeLimit: 5, MultiXactCutoff: 1000}
	page := &Page{BlockNo: 0, Tuples: []*Tuple{
		tupleWith(MaxXid, InvalidXid, 0),   // 回绕前的老事务 -> 冻结
		tupleWith(MaxXid-1, InvalidXid, 0), // 同样老 -> 冻结
		tupleWith(3, InvalidXid, 0),        // 阈值之外的新事务 -> 保留
		tupleWith(40, InvalidXid, 0),       // 新事务 -> 保留
	}}

	result, err := VacuumPage(page, cutoffs, clog, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.FrozenXmin != 3 {
		t.Fatalf("frozen count = %d, want 3", result.Stats.FrozenXmin)
	}
	if page.Tuples[0].Xmin != FrozenXid || page.Tuples[1].Xmin != FrozenXid {
		t.Fatal("old wraparound xmin must be frozen in place")
	}
	if page.Tuples[2].Xmin != FrozenXid {
		t.Fatal("xmin=3 precedes FreezeLimit=5 and must also be frozen")
	}
	if page.Tuples[3].Xmin != 40 {
		t.Fatal("recent xmin must never be frozen")
	}
	if result.AllVisible || result.AllFrozen {
		t.Fatal("page with recent xmin must be neither all-visible nor all-frozen")
	}
}

func TestVacuumFreezeIsIdempotentAndStrict(t *testing.T) {
	clog := NewClog()
	clog.Set(49, StatusCommitted)
	clog.Set(50, StatusCommitted) // 恰等于 FreezeLimit：严格小于，不冻结

	page := &Page{Tuples: []*Tuple{
		tupleWith(49, InvalidXid, 0),
		tupleWith(50, InvalidXid, 0),
		tupleWith(FrozenXid, InvalidXid, 0), // 已是 FrozenXid，不重复计数
	}}
	result, err := VacuumPage(page, defaultCutoffs(), clog, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.FrozenXmin != 1 {
		t.Fatalf("frozen count = %d, want 1 (strict cutoff, idempotent)", result.Stats.FrozenXmin)
	}

	// 再扫一遍，冻结计数必须保持为 0。
	again, err := VacuumPage(page, defaultCutoffs(), clog, nil)
	if err != nil {
		t.Fatal(err)
	}
	if again.Stats.FrozenXmin != 0 {
		t.Fatalf("second pass frozen count = %d, want 0", again.Stats.FrozenXmin)
	}
}

func TestVacuumRemovesDeadAndProtectsRecentAndRunning(t *testing.T) {
	clog := NewClog()
	clog.Set(10, StatusAborted)    // 中止插入 -> 死
	clog.Set(20, StatusCommitted)  // 老删除者 -> 死
	clog.Set(105, StatusCommitted) // 新删除者：晚于 OldestXmin -> 近期死，受保护
	clog.Set(5, StatusCommitted)   // 存活元组的插入事务

	page := &Page{Tuples: []*Tuple{
		tupleWith(10, InvalidXid, 0),
		tupleWith(5, 20, 0),
		tupleWith(5, 105, 0),
		tupleWith(200, InvalidXid, 0), // 插入事务运行中
		tupleWith(5, InvalidXid, 0),   // 干净的存活老元组
	}}

	result, err := VacuumPage(page, defaultCutoffs(), clog, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.Removed != 2 {
		t.Fatalf("removed = %d, want 2 (recent-dead must be protected)", result.Stats.Removed)
	}
	if len(page.Tuples) != 3 {
		t.Fatalf("survivors = %d, want 3", len(page.Tuples))
	}
	if page.Tuples[0].Xmax != 105 {
		t.Fatal("RECENTLY_DEAD tuple must be kept untouched")
	}
	if result.AllVisible || result.AllFrozen {
		t.Fatal("recent-dead and in-progress tuples must block both VM bits")
	}
}

func TestVacuumMultiXactRepackKeepsActiveLocker(t *testing.T) {
	clog := NewClog()
	clog.Set(5, StatusCommitted)  // xmin
	clog.Set(10, StatusCommitted) // 已结束锁成员 -> 丢弃
	clog.Set(11, StatusAborted)   // 中止更新成员 -> 丢弃
	// xid 12 保持运行中：活跃锁成员 -> 重打包后保留

	store := NewMultiXactStore()
	oldID, err := store.Create([]MultiXactMember{
		{Xid: 10, Status: MultiXactStatusForShare},
		{Xid: 11, Status: MultiXactStatusForUpdate},
		{Xid: 12, Status: MultiXactStatusForKeyShare},
	})
	if err != nil {
		t.Fatal(err)
	}

	tuple := multiTuple(5, oldID)
	page := &Page{Tuples: []*Tuple{tuple}}
	result, err := VacuumPage(page, defaultCutoffs(), clog, store)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.RepackedMulti != 1 || result.Stats.DroppedMulti != 0 {
		t.Fatalf("repack=%d drop=%d, want 1/0", result.Stats.RepackedMulti, result.Stats.DroppedMulti)
	}

	newID := MultiXactId(tuple.Xmax)
	if newID == oldID || !tuple.IsXmaxMulti() {
		t.Fatal("tuple must reference the repacked MultiXact")
	}
	members, err := store.Members(newID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].Xid != 12 || !members[0].Status.IsLock() {
		t.Fatalf("repacked members = %+v, want only active locker xid 12", members)
	}
	if _, err := store.Members(oldID); !errors.Is(err, ErrInvalidMultiXact) {
		t.Fatal("old MultiXact must be discarded after repack")
	}
	if result.AllVisible || result.AllFrozen {
		t.Fatal("retained active locker blocks both VM bits")
	}
}

func TestVacuumMultiXactDroppedWhenAllFinished(t *testing.T) {
	clog := NewClog()
	clog.Set(5, StatusCommitted)
	clog.Set(10, StatusCommitted) // 已结束锁成员
	clog.Set(11, StatusAborted)   // 中止更新成员

	store := NewMultiXactStore()
	id, err := store.Create([]MultiXactMember{
		{Xid: 10, Status: MultiXactStatusForKeyShare},
		{Xid: 11, Status: MultiXactStatusForUpdate},
	})
	if err != nil {
		t.Fatal(err)
	}

	tuple := multiTuple(5, id)
	page := &Page{Tuples: []*Tuple{tuple}}
	result, err := VacuumPage(page, defaultCutoffs(), clog, store)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.DroppedMulti != 1 {
		t.Fatalf("dropped = %d, want 1", result.Stats.DroppedMulti)
	}
	if tuple.Xmax != InvalidXid || tuple.IsXmaxMulti() {
		t.Fatal("finished MultiXact must be fully cleared from tuple")
	}
	if _, err := store.Members(id); !errors.Is(err, ErrInvalidMultiXact) {
		t.Fatal("store entry must be discarded")
	}
	if !result.AllVisible || !result.AllFrozen {
		t.Fatalf("clean tuple should be all-visible/all-frozen, got %+v", result)
	}
}

func TestVacuumMultiXactRecentKeptAndRunningUpdaterProtected(t *testing.T) {
	clog := NewClog()
	clog.Set(5, StatusCommitted)
	clog.Set(10, StatusCommitted)

	store := NewMultiXactStore()

	// 编号晚于 cutoff 的 MultiXact 必须原样保留。
	recentID, err := store.Create([]MultiXactMember{
		{Xid: 10, Status: MultiXactStatusForShare},
	})
	if err != nil {
		t.Fatal(err)
	}
	recentTuple := multiTuple(5, recentID)

	// 过时 MultiXact 中存在运行中的更新者：整组受保护，不得重写。
	oldID, err := store.Create([]MultiXactMember{
		{Xid: 12, Status: MultiXactStatusForUpdate}, // 运行中
	})
	if err != nil {
		t.Fatal(err)
	}
	protectedTuple := multiTuple(5, oldID)

	cutoffs := Cutoffs{OldestXmin: 100, FreezeLimit: 50, MultiXactCutoff: recentID}
	page := &Page{Tuples: []*Tuple{recentTuple, protectedTuple}}
	result, err := VacuumPage(page, cutoffs, clog, store)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.RepackedMulti != 0 || result.Stats.DroppedMulti != 0 {
		t.Fatalf("recent/protected multis must be untouched, got %+v", result.Stats)
	}
	if MultiXactId(recentTuple.Xmax) != recentID || MultiXactId(protectedTuple.Xmax) != oldID {
		t.Fatal("MultiXact references must not be rewritten")
	}
}

func TestVacuumPlainXmaxLifecycle(t *testing.T) {
	clog := NewClog()
	clog.Set(5, StatusCommitted)
	clog.Set(20, StatusCommitted) // 老的锁持有者：清除
	clog.Set(60, StatusCommitted) // 新的锁持有者：保留
	// 80 保持运行中：活跃 xmax，绝不清除

	oldLocker := tupleWith(5, 20, HeapXmaxIsLocker|HeapXmaxSharedLock)
	newLocker := tupleWith(5, 60, HeapXmaxIsLocker|HeapXmaxKeyShareLock)
	running := tupleWith(5, 80, 0)

	page := &Page{Tuples: []*Tuple{oldLocker, newLocker, running}}
	result, err := VacuumPage(page, defaultCutoffs(), clog, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.ClearedXmax != 1 {
		t.Fatalf("cleared xmax = %d, want 1", result.Stats.ClearedXmax)
	}
	if oldLocker.Xmax != InvalidXid || oldLocker.HasMask(xmaxAllFlags) {
		t.Fatal("old locker xmax and its flags must be cleared")
	}
	if newLocker.Xmax != 60 || running.Xmax != 80 {
		t.Fatal("recent and in-progress xmax must be retained")
	}
	if result.AllVisible || result.AllFrozen {
		t.Fatal("retained xmax prevents all-visible and all-frozen")
	}
}

func TestVacuumVisibilityBitsPrecision(t *testing.T) {
	clog := NewClog()
	clog.Set(5, StatusCommitted)
	clog.Set(10, StatusCommitted)

	// 全部老元组、无任何残留 xmax：ALL_VISIBLE + ALL_FROZEN。
	cleanPage := &Page{BlockNo: 1, Tuples: []*Tuple{
		tupleWith(5, InvalidXid, 0),
		tupleWith(10, InvalidXid, 0),
	}}
	cleanResult, err := VacuumPage(cleanPage, defaultCutoffs(), clog, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !cleanResult.AllVisible || !cleanResult.AllFrozen {
		t.Fatalf("clean old page = %+v, want both bits set", cleanResult)
	}
	for _, tuple := range cleanPage.Tuples {
		if tuple.Xmin != FrozenXid {
			t.Fatal("all old xmin must be frozen")
		}
	}

	// 已冻结但仍带 MultiXact 锁：只能 ALL_VISIBLE，不能 ALL_FROZEN。
	store := NewMultiXactStore()
	id, err := store.Create([]MultiXactMember{
		{Xid: 10, Status: MultiXactStatusForShare},
	})
	if err != nil {
		t.Fatal(err)
	}
	lockedPage := &Page{BlockNo: 2, Tuples: []*Tuple{multiTuple(5, id)}}
	keepMulti := Cutoffs{OldestXmin: 100, FreezeLimit: 50, MultiXactCutoff: InvalidMultiXactId}
	lockedResult, err := VacuumPage(lockedPage, keepMulti, clog, store)
	if err != nil {
		t.Fatal(err)
	}
	if !lockedResult.AllVisible {
		t.Fatal("lock-only MultiXact with old xmin must be all-visible")
	}
	if lockedResult.AllFrozen {
		t.Fatal("residual MultiXact must block ALL_FROZEN")
	}

	// 空页：两个 VM 位都置位。
	emptyPage := &Page{BlockNo: 3}
	emptyResult, err := VacuumPage(emptyPage, defaultCutoffs(), clog, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !emptyResult.AllVisible || !emptyResult.AllFrozen {
		t.Fatal("empty page should be both all-visible and all-frozen")
	}
}

func TestVacuumRecentXminVisibleButNotFrozen(t *testing.T) {
	clog := NewClog()
	clog.Set(95, StatusCommitted) // 已提交但晚于 FreezeLimit，且早于 OldestXmin

	page := &Page{Tuples: []*Tuple{tupleWith(95, InvalidXid, 0)}}
	result, err := VacuumPage(page, defaultCutoffs(), clog, nil)
	if err != nil {
		t.Fatal(err)
	}
	if page.Tuples[0].Xmin != 95 {
		t.Fatal("xmin inside freeze horizon must be retained unchanged")
	}
	if !result.AllVisible {
		t.Fatal("committed xmin older than OldestXmin must be all-visible")
	}
	if result.AllFrozen {
		t.Fatal("unfrozen tuple must block ALL_FROZEN")
	}
}

func TestVisibilityMapInvariant(t *testing.T) {
	vm := NewVisibilityMap()
	if err := vm.Set(7, false, true); !errors.Is(err, ErrVisibilityConflict) {
		t.Fatalf("frozen without visible must fail, got %v", err)
	}

	result := VacuumResult{AllVisible: true, AllFrozen: true}
	if err := vm.ApplyVacuum(7, result); err != nil {
		t.Fatal(err)
	}
	if !vm.IsAllVisible(7) || !vm.IsAllFrozen(7) {
		t.Fatal("VM bits were not persisted")
	}

	vm.Clear(7)
	if vm.IsAllVisible(7) || vm.IsAllFrozen(7) {
		t.Fatal("Clear must reset both VM bits")
	}
}

func TestVacuumNilInputs(t *testing.T) {
	if _, err := VacuumPage(nil, defaultCutoffs(), NewClog(), nil); err == nil {
		t.Fatal("nil page must return an error")
	}
}
