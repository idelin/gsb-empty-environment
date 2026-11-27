package mvcc

import "errors"

// TupleVerdict 是 VACUUM 视角下元组的生命周期分类，对应
// PostgreSQL 的 HeapTupleSatisfiesVacuum 返回值。
type TupleVerdict int

const (
	// VerdictDead 表示元组对所有现存快照均已死，可物理清除。
	VerdictDead TupleVerdict = iota
	// VerdictLive 表示元组存活，可能附带需要清理的行锁 xmax。
	VerdictLive
	// VerdictRecentDead 表示元组已被提交事务删除，但删除事务
	// 对某些活跃快照仍可能可见，暂时不能清除。
	VerdictRecentDead
	// VerdictInsertInProgress 表示插入事务仍在运行，不能触碰。
	VerdictInsertInProgress
)

// String 返回分类名称，便于日志与测试断言。
func (v TupleVerdict) String() string {
	switch v {
	case VerdictDead:
		return "DEAD"
	case VerdictLive:
		return "LIVE"
	case VerdictRecentDead:
		return "RECENTLY_DEAD"
	case VerdictInsertInProgress:
		return "INSERT_IN_PROGRESS"
	default:
		return "UNKNOWN"
	}
}

var errNilTuple = errors.New("mvcc: nil tuple")

// MultiXactReader 是 ClassifyTuple 所需的 MultiXact 读取能力，
// 由 *MultiXactStore 实现；声明为接口可让 nil 入参得到干净的错误返回。
type MultiXactReader interface {
	Members(id MultiXactId) ([]MultiXactMember, error)
}

// MultiXactManager 组合读写能力，供 VACUUM 重打包 MultiXact 使用。
type MultiXactManager interface {
	MultiXactReader
	Create(members []MultiXactMember) (MultiXactId, error)
	Discard(id MultiXactId)
}

// resolveXminStatus 解析插入事务状态，命中提示位时直接返回并避免
// 查询 CLOG；特殊事务号按 PostgreSQL 语义处理。
func resolveXminStatus(t *Tuple, clog TxnStatusProvider) TxnStatus {
	switch {
	case t.Xmin == FrozenXid:
		return StatusCommitted
	case t.Xmin == BootstrapXid:
		return StatusCommitted
	case t.Xmin == InvalidXid:
		return StatusAborted
	case t.HasMask(HeapXminCommitted):
		return StatusCommitted
	case t.HasMask(HeapXminInvalid):
		return StatusAborted
	default:
		return clog.StatusOf(t.Xmin)
	}
}

// resolveXmaxStatus 解析普通 xmax 事务状态；提示位优先。
func resolveXmaxStatus(t *Tuple, clog TxnStatusProvider) TxnStatus {
	switch {
	case t.Xmax == InvalidXid:
		return StatusAborted
	case t.HasMask(HeapXmaxCommitted):
		return StatusCommitted
	case t.HasMask(HeapXmaxInvalid):
		return StatusAborted
	default:
		return clog.StatusOf(t.Xmax)
	}
}

// ClassifyTuple 实现 VACUUM 视角的可见性判定。
//
// oldestXmin 是全局活跃快照的最早 xmin（OldestXmin）。判定过程中
// 会把解析到的 CLOG 状态固化为元组提示位（hint bits）。
func ClassifyTuple(t *Tuple, oldestXmin XID, clog TxnStatusProvider, mx MultiXactReader) (TupleVerdict, error) {
	if t == nil {
		return VerdictDead, errNilTuple
	}

	switch xminStatus := resolveXminStatus(t, clog); xminStatus {
	case StatusAborted:
		// 插入事务中止：元组从未对任何快照存在过，直接可清除。
		t.SetMask(HeapXminInvalid)
		return VerdictDead, nil
	case StatusInProgress:
		// 插入事务仍在运行，任何快照都不能安全地判定其死活。
		return VerdictInsertInProgress, nil
	}

	// xmin 已提交，固化提示位（FrozenXid 直接携带冻结提示位）。
	if t.Xmin == FrozenXid {
		t.SetMask(HeapXminFrozen)
	} else {
		t.SetMask(HeapXminCommitted)
		t.ClearMask(HeapXminInvalid)
	}

	// 没有有效 xmax 即没有删除者，元组存活。
	if !t.XmaxIsValid() {
		return VerdictLive, nil
	}

	if t.IsXmaxMulti() {
		return classifyMultiXmax(t, oldestXmin, clog, mx)
	}

	switch xmaxStatus := resolveXmaxStatus(t, clog); xmaxStatus {
	case StatusInProgress:
		// 删除事务仍在运行，元组此刻仍存活。
		return VerdictLive, nil
	case StatusAborted:
		// 删除已中止：若 xmax 仅为行锁则本就是锁，否则删除从未发生。
		t.SetMask(HeapXmaxInvalid)
		return VerdictLive, nil
	case StatusCommitted:
		t.SetMask(HeapXmaxCommitted)
		t.ClearMask(HeapXmaxInvalid)
		// 纯行锁持有者提交后元组依旧存活，xmax 留待 freeze 阶段清除。
		if t.HasMask(HeapXmaxIsLocker) {
			return VerdictLive, nil
		}
		// 真正的删除/更新事务：删除早于 OldestXmin 才对所有快照均死。
		if XidPrecedes(t.Xmax, oldestXmin) {
			return VerdictDead, nil
		}
		return VerdictRecentDead, nil
	default:
		return VerdictLive, nil
	}
}

// classifyMultiXmax 处理 xmax 指向 MultiXact 的情形：
// 锁成员从不杀死元组，至多一个修改成员决定死活。
func classifyMultiXmax(t *Tuple, oldestXmin XID, clog TxnStatusProvider, mx MultiXactReader) (TupleVerdict, error) {
	if mx == nil {
		return VerdictLive, errors.New("mvcc: tuple references MultiXact but no MultiXactStore provided")
	}

	members, err := mx.Members(MultiXactId(t.Xmax))
	if err != nil {
		return VerdictLive, err
	}

	// 任何成员事务仍在运行时，保守视为存活——既不能删也不能当死元组。
	for _, m := range members {
		if clog.StatusOf(m.Xid) == StatusInProgress {
			return VerdictLive, nil
		}
	}

	for _, m := range members {
		if !m.Status.IsUpdate() {
			continue
		}
		switch clog.StatusOf(m.Xid) {
		case StatusAborted:
			// 修改成员已中止，锁成员无法杀死元组：存活。
			return VerdictLive, nil
		case StatusCommitted:
			if XidPrecedes(m.Xid, oldestXmin) {
				return VerdictDead, nil
			}
			return VerdictRecentDead, nil
		}
	}

	// 只有锁成员（或不存在任何修改成员），元组必然存活。
	return VerdictLive, nil
}
