package repartition

import (
	"container/heap"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// Open 打开或创建数据目录 dir 下的持久化屏障。
// 首次创建时 cfg 生效；重新打开时 cfg 必须与持久化配置一致。
func Open(dir string, cfg Config, opts ...Option) (*Barrier, error) {
	cfg = cfg.normalized()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	b := &Barrier{
		oldMax:     map[int]int64{},
		newMax:     map[int]int64{},
		cutReached: map[int]bool{},
		committed:  map[string]bool{},
		delivered:  map[string]bool{},
		known:      map[string]bool{},
	}
	for _, o := range opts {
		o(b)
	}

	var persisted *Config
	l, err := openWAL(dir, func(fr frame) error {
		return b.replay(fr, &persisted)
	})
	if err != nil {
		return nil, err
	}
	b.wal = l
	b.cond = sync.NewCond(&b.mu)

	if persisted != nil {
		if !configEqual(*persisted, cfg) {
			_ = l.close()
			return nil, ErrConfigMismatch
		}
	} else if err := l.append(evConfig, cfg, b.fault); err != nil {
		_ = l.close()
		return nil, err
	}
	b.cfg = cfg
	b.pendingIDs = map[string]bool{}
	// 原子性派生：若所有旧分区的切换通知都已持久化，即使 switched 帧
	// 尚未落盘就崩溃，恢复后也必须视为已经翻转。
	if !b.switched && b.allCutsReached() {
		b.switched = true
	}
	b.rebuildPending()
	return b, nil
}

// replay 把一帧 WAL 事件折叠进内存状态，不触发任何发布。
func (b *Barrier) replay(fr frame, persisted **Config) error {
	switch fr.Kind {
	case evConfig:
		var cfg Config
		if err := json.Unmarshal(fr.Body, &cfg); err != nil {
			return err
		}
		*persisted = &cfg
		b.cfg = cfg
	case evRecord:
		var env recordEnvelope
		if err := json.Unmarshal(fr.Body, &env); err != nil {
			return err
		}
		r := env.Record
		r.seq = env.Seq
		if env.Seq >= b.nextSeq {
			b.nextSeq = env.Seq + 1
		}
		b.applyRecord(r)
	case evCutover:
		var ev cutoverEvent
		if err := json.Unmarshal(fr.Body, &ev); err != nil {
			return err
		}
		b.cutReached[ev.Partition] = true
	case evSwitched:
		b.switched = true
	case evCommitted:
		var ev commitEvent
		if err := json.Unmarshal(fr.Body, &ev); err != nil {
			return err
		}
		b.committed[ev.ID] = true
	case evDelivered:
		var ev commitEvent
		if err := json.Unmarshal(fr.Body, &ev); err != nil {
			return err
		}
		b.delivered[ev.ID] = true
	default:
		return fmt.Errorf("repartition: 未知 WAL 事件类型 %q", fr.Kind)
	}
	return nil
}

// applyRecord 仅恢复内存索引与队列，不做校验（落盘时已校验）。
func (b *Barrier) applyRecord(r Record) {
	id := r.id()
	if b.known[id] {
		return
	}
	b.known[id] = true
	if r.Topology {
		if off, ok := b.newMax[r.Partition]; !ok || r.Offset > off {
			b.newMax[r.Partition] = r.Offset
		}
		heap.Push(&b.newHeap, r)
		return
	}
	if off, ok := b.oldMax[r.Partition]; !ok || r.Offset > off {
		b.oldMax[r.Partition] = r.Offset
	}
	b.oldQueue = insertSorted(b.oldQueue, r)
}

// rebuildPending 在回放结束后重建待发布队列：已提交的记录跳过，
// 未提交的记录按当时的可见性规则重新排队，保证重启后不丢不重。
func (b *Barrier) rebuildPending() {
	b.pending = nil
	all := make([]Record, 0, len(b.oldQueue)+len(b.newHeap))
	all = append(all, b.oldQueue...)
	for _, r := range b.newHeap {
		all = append(all, r)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].seq < all[j].seq })
	for _, r := range all {
		if r.Topology && !b.switched {
			continue // 回放时尚未翻转：新拓扑保持不可见
		}
		id := r.id()
		if b.committed[id] || b.pendingIDs[id] {
			continue
		}
		b.pending = append(b.pending, r)
		b.pendingIDs[id] = true
	}
}

// MarkDelivered 在调用下游 Publish 之前持久化投递意图。
// 重启后看到该标记且没有回执时，说明上次可能在 Publish 前后崩溃：
// 组件会重新调用 Publish（至少一次），由幂等 Sink 去重，再补回执。
func (b *Barrier) MarkDelivered(r Record) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkOpen(); err != nil {
		return err
	}
	id := r.id()
	if b.delivered[id] || b.committed[id] {
		return nil
	}
	if len(b.pending) == 0 || b.pending[0].id() != id {
		return fmt.Errorf("repartition: 记录 %s 不是当前待发布记录", id)
	}
	if err := b.persist(evDelivered, commitEvent{ID: id}); err != nil {
		return err
	}
	b.delivered[id] = true
	return nil
}

// IsDelivered 返回记录的投递意图是否已落盘（用于恢复路径决策）。
func (b *Barrier) IsDelivered(r Record) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.delivered[r.id()]
}

// enqueueVisible 在拓扑翻转的瞬间把新拓扑堆整体移入 pending：
// 旧记录此前已逐条入队，新记录在此之前对消费者完全不可见。
func (b *Barrier) enqueueVisible() {
	if !b.switched {
		return
	}
	for b.newHeap.Len() > 0 {
		r := heap.Pop(&b.newHeap).(Record)
		id := r.id()
		if b.committed[id] || b.pendingIDs[id] {
			continue
		}
		b.pending = append(b.pending, r)
		b.pendingIDs[id] = true
	}
}

func (b *Barrier) allCutsReached() bool {
	for _, p := range b.cfg.OldPartitions {
		if !b.cutReached[p] {
			return false
		}
	}
	return true
}

func (b *Barrier) persist(kind string, body any) error {
	err := b.wal.append(kind, body, b.fault)
	if err != nil {
		b.closed = true
		return err
	}
	return nil
}

func (b *Barrier) checkOpen() error {
	if b.closed || b.wal == nil || b.wal.f == nil {
		return ErrClosed
	}
	return nil
}

func (b *Barrier) checkOpenLocked() error { return b.checkOpen() }

func contains(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func insertSorted(q []Record, r Record) []Record {
	i := sort.Search(len(q), func(i int) bool { return lessRecord(r, q[i]) })
	q = append(q, Record{})
	copy(q[i+1:], q[i:])
	q[i] = r
	return q
}

func lessRecord(a, c Record) bool {
	if a.Partition != c.Partition {
		return a.Partition < c.Partition
	}
	return a.Offset < c.Offset
}
