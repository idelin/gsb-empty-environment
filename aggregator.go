package main

import "fmt"

// Aggregator 聚合输入事件，发布与确认分离：
// 先生成结果并随检查点落盘（待发），再发布，最后确认并从待发中移除。
type Aggregator struct {
	store      *Store
	cp         Checkpoint
	dirtySum   int64 // 已摄入但未落盘
	dirtyCount int64
	consumed   int64 // 已摄入事件总数（含脏数据）
}

// NewAggregator 从检查点恢复；无检查点时初始化新实例。
// InstanceID 持久化在检查点中，重启后不变，保证结果 ID 稳定。
func NewAggregator(store *Store) *Aggregator {
	cp, ok, err := store.Load()
	if err != nil {
		panic(err)
	}
	if !ok {
		cp = Checkpoint{InstanceID: "agg-1", NextResultSeq: 1}
	}
	return &Aggregator{store: store, cp: cp, consumed: cp.Offset}
}

func (a *Aggregator) Ingest(v int64) {
	a.dirtySum += v
	a.dirtyCount++
	a.consumed++
}

func (a *Aggregator) Consumed() int64 { return a.consumed }

// Persist 把脏数据折叠进检查点并原子落盘（计算状态推进）。
func (a *Aggregator) Persist() error {
	a.cp.Offset = a.consumed
	a.cp.Sum += a.dirtySum
	a.cp.Count += a.dirtyCount
	a.dirtySum, a.dirtyCount = 0, 0
	return a.store.Save(a.cp)
}

// Produce 生成结果：ID 来自检查点里持久化的序号。
// 关键约束：生成后必须先 Persist 再发布，
// 这样恢复后重发用的是同一个 ID，不会生成新 ID 造成重复计数。
func (a *Aggregator) Produce() Result {
	r := Result{
		ID:         fmt.Sprintf("%s-r%06d", a.cp.InstanceID, a.cp.NextResultSeq),
		Seq:        a.cp.NextResultSeq,
		FromOffset: a.cp.LastEmittedOffset,
		ToOffset:   a.cp.Offset,
		Delta:      a.cp.Sum - a.cp.LastEmittedSum,
	}
	a.cp.NextResultSeq++
	a.cp.LastEmittedOffset = r.ToOffset
	a.cp.LastEmittedSum = a.cp.Sum
	a.cp.Pending = append(a.cp.Pending, r)
	return r
}

// HasUnemittedDelta 报告是否存在已持久化但尚未生成结果的增量。
func (a *Aggregator) HasUnemittedDelta() bool {
	return a.cp.Sum != a.cp.LastEmittedSum
}

func (a *Aggregator) Pending() []Result { return a.cp.Pending }

// Ack 从待发列表移除（仅内存），调用方随后 Persist 提交（效果确认推进）。
func (a *Aggregator) Ack(id string) {
	kept := a.cp.Pending[:0]
	for _, p := range a.cp.Pending {
		if p.ID != id {
			kept = append(kept, p)
		}
	}
	a.cp.Pending = kept
}

func (a *Aggregator) State() Checkpoint { return a.cp }
