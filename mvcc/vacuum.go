package mvcc

import "errors"

// Cutoffs 携带一次 VACUUM FREEZE 扫描使用的三个全局阈值。
type Cutoffs struct {
	// OldestXmin 是全局最老活跃快照下界，删除事务早于它才允许物理清除。
	OldestXmin XID
	// FreezeLimit 是 xmin 冻结阈值，已提交且早于它的 xmin 原地冻结。
	FreezeLimit XID
	// MultiXactCutoff 是 MultiXact 重打包/清除阈值。
	MultiXactCutoff MultiXactId
}

// VacuumStats 汇总单页清理动作计数。
type VacuumStats struct {
	Examined      int // 扫描的元组数
	Removed       int // 物理清除的死元组数
	FrozenXmin    int // xmin 被原地冻结的元组数
	ClearedXmax   int // 普通 xmax 被清除的元组数
	RepackedMulti int // 重打包后仍保留活跃锁成员的 MultiXact 数
	DroppedMulti  int // 被整体清除的过时 MultiXact 数
}

// VacuumResult 是单页清理结果，包含统计与清理后应有的可见性映射位。
type VacuumResult struct {
	Stats      VacuumStats
	AllVisible bool
	AllFrozen  bool
}

var errNilPage = errors.New("mvcc: nil page")

// xmax 相关的全部标志位，清除 xmax 时一次性摘掉。
const xmaxAllFlags = HeapXmaxCommitted | HeapXmaxInvalid | HeapXmaxIsMulti |
	HeapXmaxIsLocker | HeapXmaxExclLock | HeapXmaxSharedLock | HeapXmaxKeyShareLock

// clearXmax 清空元组的 xmax 字段及其全部关联标志。
func clearXmax(t *Tuple) {
	t.Xmax = InvalidXid
	t.ClearMask(xmaxAllFlags)
}

// freezeXminIfNeeded 对已提交且早于 FreezeLimit 的 xmin 原地冻结。
// FrozenXid 恒可见且幂等，重复扫描不会重复计数。
func freezeXminIfNeeded(t *Tuple, freezeLimit XID, clog TxnStatusProvider, stats *VacuumStats) {
	if t.Xmin == FrozenXid {
		return
	}
	committed := t.Xmin == BootstrapXid ||
		(t.Xmin >= FirstNormalXid && resolveXminStatus(t, clog) == StatusCommitted)
	if committed && XidPrecedes(t.Xmin, freezeLimit) {
		t.Xmin = FrozenXid
		t.SetMask(HeapXminFrozen)
		stats.FrozenXmin++
	}
}

// processPlainXmax 清理存活元组上已结束且过时的普通 xmax。
// 运行中的事务（活跃删除者/锁持有者）一律保留，杜绝误删。
func processPlainXmax(t *Tuple, freezeLimit XID, clog TxnStatusProvider, stats *VacuumStats) {
	if !t.XmaxIsValid() || t.IsXmaxMulti() {
		return
	}
	if resolveXmaxStatus(t, clog) == StatusInProgress {
		return
	}
	if !XidPrecedes(t.Xmax, freezeLimit) {
		return
	}
	clearXmax(t)
	stats.ClearedXmax++
}

// processMultiXmax 对过时的 MultiXact 重打包：只保留仍活跃（运行中）
// 的锁成员。已结束的锁成员一律丢弃；进入存活分支的修改成员必然已中止，
// 同样丢弃。运行中的修改成员受活跃快照保护，整组原样保留。
func processMultiXmax(t *Tuple, cutoff MultiXactId, clog TxnStatusProvider, mx MultiXactManager, stats *VacuumStats) error {
	if !t.XmaxIsValid() || !t.IsXmaxMulti() {
		return nil
	}

	oldID := MultiXactId(t.Xmax)
	if !MultiXactIdPrecedes(oldID, cutoff) {
		return nil
	}

	members, err := mx.Members(oldID)
	if err != nil {
		return err
	}

	// 运行中的修改/删除事务仍可能提交：受活跃快照保护，不能改写。
	for _, m := range members {
		if m.Status.IsUpdate() && clog.StatusOf(m.Xid) == StatusInProgress {
			return nil
		}
	}

	activeLockers := make([]MultiXactMember, 0, len(members))
	for _, m := range members {
		// 仅保留仍活跃的锁成员；已结束锁与中止的修改成员全部淘汰。
		if m.Status.IsLock() && clog.StatusOf(m.Xid) == StatusInProgress {
			activeLockers = append(activeLockers, m)
		}
	}

	if len(activeLockers) == 0 {
		clearXmax(t)
		mx.Discard(oldID)
		stats.DroppedMulti++
		return nil
	}

	newID, err := mx.Create(activeLockers)
	if err != nil {
		return err
	}
	t.Xmax = XID(newID)
	mx.Discard(oldID)
	stats.RepackedMulti++
	return nil
}

// hasPendingDeletion 报告元组是否仍带着未决删除：运行中的删除事务、
// 已提交但尚未被所有快照消化的删除者，或含此类成员的 MultiXact。
// 纯行锁（含已结束的锁）不构成未决删除。
func hasPendingDeletion(t *Tuple, clog TxnStatusProvider, mx MultiXactReader) bool {
	if !t.XmaxIsValid() {
		return false
	}

	if t.IsXmaxMulti() {
		if mx == nil {
			return true
		}
		members, err := mx.Members(MultiXactId(t.Xmax))
		if err != nil {
			return true
		}
		for _, m := range members {
			status := clog.StatusOf(m.Xid)
			if status == StatusInProgress {
				return true
			}
			if m.Status.IsUpdate() && status == StatusCommitted {
				return true
			}
		}
		return false
	}

	switch resolveXmaxStatus(t, clog) {
	case StatusInProgress:
		return true
	case StatusCommitted:
		return !t.HasMask(HeapXmaxIsLocker)
	default:
		return false
	}
}

// tupleAllVisible 报告清理后的元组是否对任意新老快照都可见：
// 已提交且 xmin 早于 OldestXmin，并且不存在未决删除。
func tupleAllVisible(t *Tuple, oldestXmin XID, clog TxnStatusProvider, mx MultiXactReader) bool {
	xminVisible := resolveXminStatus(t, clog) == StatusCommitted &&
		XidPrecedes(t.Xmin, oldestXmin)
	return xminVisible && !hasPendingDeletion(t, clog, mx)
}

// tupleAllFrozen 报告元组是否彻底冻结：xmin 已是 FrozenXid，
// 且不存在任何残留 MultiXact 或活跃 xmax 引用。
func tupleAllFrozen(t *Tuple) bool {
	return t.Xmin == FrozenXid && !t.IsXmaxMulti() && t.Xmax == InvalidXid
}

// VacuumPage 扫描并就地清理一个堆页，返回统计与清理后应落盘的
// Visibility Map 状态。page.Tuples 会被原地收缩，死元组指针被剔除。
func VacuumPage(page *Page, cutoffs Cutoffs, clog TxnStatusProvider, mx *MultiXactStore) (VacuumResult, error) {
	if page == nil {
		return VacuumResult{}, errNilPage
	}

	result := VacuumResult{AllVisible: true, AllFrozen: true}
	kept := make([]*Tuple, 0, len(page.Tuples))

	for _, t := range page.Tuples {
		result.Stats.Examined++

		verdict, err := ClassifyTuple(t, cutoffs.OldestXmin, clog, mx)
		if err != nil {
			return VacuumResult{}, err
		}

		if verdict == VerdictDead {
			// 确认对全部快照已死：xmin 中止，或提交删除早于 OldestXmin。
			result.Stats.Removed++
			continue
		}
		kept = append(kept, t)

		if verdict != VerdictLive {
			// RECENTLY_DEAD / INSERT_IN_PROGRESS 受活跃快照保护，原样保留。
			continue
		}

		freezeXminIfNeeded(t, cutoffs.FreezeLimit, clog, &result.Stats)
		processPlainXmax(t, cutoffs.FreezeLimit, clog, &result.Stats)
		if err := processMultiXmax(t, cutoffs.MultiXactCutoff, clog, mx, &result.Stats); err != nil {
			return VacuumResult{}, err
		}
	}

	page.Tuples = kept

	for _, t := range page.Tuples {
		if !tupleAllVisible(t, cutoffs.OldestXmin, clog, mx) {
			result.AllVisible = false
		}
		if !tupleAllFrozen(t) {
			result.AllFrozen = false
		}
	}

	// ALL_FROZEN 以 ALL_VISIBLE 为前提，空页两者皆为 true。
	if !result.AllVisible {
		result.AllFrozen = false
	}
	return result, nil
}
