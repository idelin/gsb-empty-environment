// Package aligner 实现分库分表集群的全局一致性读取（Global Read-View）对齐器。
//
// 各分片产生独立的本地提交时间戳，并通过 CDC 流把变更事件推送给对齐器。
// 对齐器维护每个分片的水位线（watermark），并跟踪所有尚未因果闭合的事务，
// 计算全局安全时间戳 T_Safe：
//
//	T_Safe = min( 各分片水位线, 所有未闭合事务的最早事件时间戳 - 1 )
//
// 只有当一个跨分片事务的所有分片事件全部就绪（因果闭合），且其提交时间戳
// 不超过 T_Safe 时，它才会被并入全局快照。外部查询若请求的时间戳超过
// T_Safe，会被拒绝（ErrNotClosed），从而保证任何快照要么包含一个事务的
// 全部分片事件，要么完全不包含，杜绝"钱凭空消失"。
package aligner

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrNotClosed 表示请求的时间戳尚未因果闭合，查询视图被阻止生成。
var ErrNotClosed = errors.New("aligner: requested timestamp is not causally closed yet")

// OpKind 是 CDC 事件的操作类型。
type OpKind int

const (
	OpDebit  OpKind = iota // 扣款（转出）
	OpCredit               // 入账（转入）
)

func (k OpKind) String() string {
	if k == OpDebit {
		return "DEBIT"
	}
	return "CREDIT"
}

// Event 是分片 CDC 流上送的一条变更事件。
type Event struct {
	ShardID string // 产生事件的分片
	TxnID   string // 全局事务 ID（跨分片事务共享）
	TS      uint64 // 分片本地提交时间戳
	Kind    OpKind
	Account string
	Amount  int64
	Parts   int // 该事务涉及的分片总数（用于判定因果闭合）
}

func (e Event) String() string {
	return fmt.Sprintf("[%s ts=%d txn=%s %s %s %d]", e.ShardID, e.TS, e.TxnID, e.Kind, e.Account, e.Amount)
}

// txnState 跟踪一个尚未闭合的事务。
type txnState struct {
	minTS  uint64 // 已见事件的最早时间戳
	maxTS  uint64 // 已见事件的最晚时间戳，闭合后作为全局提交时间戳
	parts  int
	seen   map[string]struct{}
	events []Event
}

// closedTxn 是已闭合、等待并入快照的事务。
type closedTxn struct {
	commitTS uint64
	events   []Event
}

// Snapshot 是某一全局安全时间点的一致性视图。
type Snapshot struct {
	TS       uint64
	Balances map[string]int64
}

// Total 返回快照中所有账户余额之和（用于资金守恒校验）。
func (s Snapshot) Total() int64 {
	var sum int64
	for _, v := range s.Balances {
		sum += v
	}
	return sum
}

// Aligner 协调各分片的 CDC 流，输出全局一致性快照。
type Aligner struct {
	mu        sync.Mutex
	watermark map[string]uint64    // 每个分片的水位线（心跳驱动，单调递增）
	pending   map[string]*txnState // 未闭合事务：txnID -> 状态
	closed    []closedTxn          // 已闭合待应用事务，按 commitTS 升序
	balances  map[string]int64     // 当前已应用的账户状态
	snaps     []Snapshot           // 历史快照，按 TS 升序
	safeTS    uint64
}

// New 创建一个对齐器，shards 为集群中所有分片的 ID。
func New(shards ...string) *Aligner {
	a := &Aligner{
		watermark: make(map[string]uint64, len(shards)),
		pending:   make(map[string]*txnState),
		balances:  make(map[string]int64),
	}
	for _, s := range shards {
		a.watermark[s] = 0
	}
	a.snaps = append(a.snaps, Snapshot{TS: 0, Balances: map[string]int64{}})
	return a
}

// Ingest 消费一条分片 CDC 事件。若该事件使事务因果闭合，
// 事务进入待应用队列，并尝试推进 T_Safe。
func (a *Aligner) Ingest(ev Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, ok := a.watermark[ev.ShardID]; !ok {
		return fmt.Errorf("aligner: unknown shard %q", ev.ShardID)
	}
	if ev.Parts < 1 {
		return fmt.Errorf("aligner: event %v has invalid Parts=%d", ev, ev.Parts)
	}

	st, ok := a.pending[ev.TxnID]
	if !ok {
		st = &txnState{
			minTS: ev.TS,
			maxTS: ev.TS,
			parts: ev.Parts,
			seen:  make(map[string]struct{}),
		}
		a.pending[ev.TxnID] = st
	}
	if _, dup := st.seen[ev.ShardID]; dup {
		return fmt.Errorf("aligner: duplicate event from shard %q for txn %q", ev.ShardID, ev.TxnID)
	}
	st.seen[ev.ShardID] = struct{}{}
	st.events = append(st.events, ev)
	if ev.TS < st.minTS {
		st.minTS = ev.TS
	}
	if ev.TS > st.maxTS {
		st.maxTS = ev.TS
	}

	// 所有分片部分全部就绪 => 因果闭合。
	if len(st.seen) == st.parts {
		delete(a.pending, ev.TxnID)
		ct := closedTxn{commitTS: st.maxTS, events: st.events}
		// 按 commitTS 升序插入。
		i := sort.Search(len(a.closed), func(i int) bool {
			return a.closed[i].commitTS > ct.commitTS
		})
		a.closed = append(a.closed, closedTxn{})
		copy(a.closed[i+1:], a.closed[i:])
		a.closed[i] = ct
	}

	a.advanceLocked()
	return nil
}

// Heartbeat 上报分片水位线：表示该分片 ts 及之前的所有事件都已送达。
func (a *Aligner) Heartbeat(shardID string, ts uint64) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, ok := a.watermark[shardID]; !ok {
		return fmt.Errorf("aligner: unknown shard %q", shardID)
	}
	if ts > a.watermark[shardID] {
		a.watermark[shardID] = ts
	}
	a.advanceLocked()
	return nil
}

// SafeTS 返回当前全局安全时间戳。
func (a *Aligner) SafeTS() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.safeTS
}

// safeTSLocked 计算 T_Safe：各分片水位线的最小值，再受未闭合事务
// 最早事件时间戳的约束（未闭合事务之前的时刻才是安全的）。
func (a *Aligner) safeTSLocked() uint64 {
	base := ^uint64(0)
	for _, wm := range a.watermark {
		if wm < base {
			base = wm
		}
	}
	for _, st := range a.pending {
		var barrier uint64
		if st.minTS > 0 {
			barrier = st.minTS - 1
		}
		if barrier < base {
			base = barrier
		}
	}
	return base
}

// advanceLocked 重算 T_Safe，并把 commitTS <= T_Safe 的已闭合事务并入快照。
func (a *Aligner) advanceLocked() {
	a.safeTS = a.safeTSLocked()

	n := 0
	for n < len(a.closed) && a.closed[n].commitTS <= a.safeTS {
		for _, ev := range a.closed[n].events {
			if ev.Kind == OpDebit {
				a.balances[ev.Account] -= ev.Amount
			} else {
				a.balances[ev.Account] += ev.Amount
			}
		}
		n++
	}
	a.closed = a.closed[n:]

	// T_Safe 前进时记录一份不可变快照。
	if a.safeTS > a.snaps[len(a.snaps)-1].TS {
		cp := make(map[string]int64, len(a.balances))
		for k, v := range a.balances {
			cp[k] = v
		}
		a.snaps = append(a.snaps, Snapshot{TS: a.safeTS, Balances: cp})
	}
}

// Snapshot 返回当前 T_Safe 时刻的一致性视图。
func (a *Aligner) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snaps[len(a.snaps)-1]
}

// SnapshotAt 返回 ts 时刻的一致性视图。若 ts 超过 T_Safe（尚未因果闭合），
// 返回 ErrNotClosed，阻止外部生成查询视图。
func (a *Aligner) SnapshotAt(ts uint64) (Snapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if ts > a.safeTS {
		return Snapshot{}, ErrNotClosed
	}
	// 找最后一个 TS <= ts 的快照。
	i := sort.Search(len(a.snaps), func(i int) bool {
		return a.snaps[i].TS > ts
	})
	if i == 0 {
		return Snapshot{TS: ts, Balances: map[string]int64{}}, nil
	}
	return a.snaps[i-1], nil
}
