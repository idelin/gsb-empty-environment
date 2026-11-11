package repartition

import (
	"container/heap"
	"context"
	"fmt"
)

// Ingest 接收一条采集端记录。可安全重复调用：同分区同偏移的重复记录直接忽略。
//
// 旧拓扑记录在切换偏移之后会被 ErrFenced 拒绝；新拓扑记录在屏障翻转前
// 仅持久化缓存，对 Next/Run 不可见，保证不会提前发布。
func (b *Barrier) Ingest(r Record) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkOpen(); err != nil {
		return err
	}
	if r.Offset < 0 {
		return fmt.Errorf("repartition: 偏移不能为负")
	}
	id := r.id()
	if b.known[id] {
		return nil // 重复通知，幂等忽略
	}
	r.seq = b.nextSeq
	env := recordEnvelope{Record: r, Seq: r.seq}

	if r.Topology {
		if !contains(b.cfg.NewPartitions, r.Partition) {
			return fmt.Errorf("repartition: 未知新拓扑分区 %d", r.Partition)
		}
		if off, ok := b.newMax[r.Partition]; ok && r.Offset <= off {
			return fmt.Errorf("%w: 新分区 %d", ErrOffsetOrder, r.Partition)
		}
		if err := b.persist(evRecord, env); err != nil {
			return err
		}
		b.known[id] = true
		b.nextSeq++
		b.newMax[r.Partition] = r.Offset
		heap.Push(&b.newHeap, r)
		if b.switched {
			if !b.committed[id] && !b.pendingIDs[id] {
				b.pending = append(b.pending, r)
				b.pendingIDs[id] = true
			}
		}
		b.cond.Broadcast()
		return nil
	}

	if !contains(b.cfg.OldPartitions, r.Partition) {
		return fmt.Errorf("repartition: 未知旧拓扑分区 %d", r.Partition)
	}
	if b.switched {
		return ErrFenced
	}
	cut := b.cfg.CutOffsets[r.Partition]
	if r.Offset > cut {
		return ErrFenced
	}
	if off, ok := b.oldMax[r.Partition]; ok && r.Offset <= off {
		return fmt.Errorf("%w: 旧分区 %d", ErrOffsetOrder, r.Partition)
	}
	if err := b.persist(evRecord, env); err != nil {
		return err
	}
	b.known[id] = true
	b.nextSeq++
	b.oldMax[r.Partition] = r.Offset
	b.oldQueue = insertSorted(b.oldQueue, r)
	if !b.committed[id] && !b.pendingIDs[id] {
		b.pending = append(b.pending, r)
		b.pendingIDs[id] = true
	}
	b.cond.Broadcast()
	return nil
}

// NotifyCutover 通知旧分区 partition 已到达切换偏移。重复通知幂等。
// 当最后一个旧分区到达时，屏障原子翻转并立即持久化 switched 事件。
func (b *Barrier) NotifyCutover(partition int, offset int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkOpen(); err != nil {
		return err
	}
	cut, ok := b.cfg.CutOffsets[partition]
	if !ok {
		return fmt.Errorf("repartition: 分区 %d 不在旧拓扑中", partition)
	}
	if offset != cut {
		return fmt.Errorf("repartition: 分区 %d 切换偏移 %d 与配置 %d 不一致", partition, offset, cut)
	}
	if b.cutReached[partition] {
		return nil // 重复通知
	}
	if err := b.persist(evCutover, cutoverEvent{Partition: partition, Offset: offset}); err != nil {
		return err
	}
	b.cutReached[partition] = true

	if !b.switched && b.allCutsReached() {
		if err := b.persist(evSwitched, struct{}{}); err != nil {
			return err
		}
		b.switched = true
		b.enqueueVisible()
	}
	b.cond.Broadcast()
	return nil
}

// Next 返回下一条可见且未回执的记录，阻塞至记录可用、ctx 取消或屏障关闭。
// 发布成功后必须调用 Ack；崩溃未 Ack 的记录会在重启后重新投递。
func (b *Barrier) Next(ctx context.Context) (Record, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			b.mu.Lock()
			b.cond.Broadcast()
			b.mu.Unlock()
		case <-stop:
		}
	}()

	for {
		if err := b.checkOpenLocked(); err != nil {
			return Record{}, err
		}
		if len(b.pending) > 0 {
			return b.pending[0], nil
		}
		if ctx.Err() != nil {
			return Record{}, ctx.Err()
		}
		b.cond.Wait()
	}
}

// TryNext 非阻塞地返回下一条可见未回执记录；暂不可用时返回 ErrDone。
func (b *Barrier) TryNext() (Record, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkOpenLocked(); err != nil {
		return Record{}, err
	}
	if len(b.pending) > 0 {
		return b.pending[0], nil
	}
	return Record{}, ErrDone
}

// Ack 持久化下游发布回执。重复回执幂等；回执按 Next 的投递顺序确认。
func (b *Barrier) Ack(r Record) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkOpen(); err != nil {
		return err
	}
	id := r.id()
	if b.committed[id] {
		return nil
	}
	if len(b.pending) == 0 || b.pending[0].id() != id {
		return fmt.Errorf("repartition: 回执 %s 不是当前待确认记录", id)
	}
	if err := b.persist(evCommitted, commitEvent{ID: id}); err != nil {
		return err
	}
	b.committed[id] = true
	b.pending = b.pending[1:]
	delete(b.pendingIDs, id)
	delete(b.delivered, id)
	b.cond.Broadcast()
	return nil
}

// Run 驱动“持久化投递意图 -> 下游发布 -> 持久化回执”循环，直到 ctx 取消。
// 投递意图在 Publish 之前 fsync，因此 Publish 与回执之间崩溃后，重启会
// 跳过 Publish 直接补回执：每条记录对 Sink 恰好下发一次。Sink 自身仍应
// 以记录 ID 做幂等防御。WAL 故障等屏障错误会直接返回。
func (b *Barrier) Run(ctx context.Context, sink Sink) error {
	for {
		r, err := b.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		// 投递意图先落盘；崩溃恢复后该标记可能已存在，仍要重新 Publish，
		// 由 Sink 以记录 ID 幂等去重，保证至少一次且不产生重复副作用。
		if err := b.MarkDelivered(r); err != nil {
			return err
		}
		if err := sink.Publish(ctx, r); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue // 发布失败：保留在 pending，下轮重试
		}
		if err := b.Ack(r); err != nil {
			return err
		}
	}
}

// Close 刷新并关闭 WAL。关闭后所有操作返回 ErrClosed。
func (b *Barrier) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	err := b.wal.close()
	b.cond.Broadcast()
	return err
}

// Status 是屏障的只读快照。
type Status struct {
	Switched       bool
	CutReached     map[int]bool
	Pending        int
	CommittedCount int
	KnownCount     int
}

// Snapshot 返回当前屏障状态，用于监控与测试断言。
func (b *Barrier) Snapshot() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	cuts := make(map[int]bool, len(b.cutReached))
	for k, v := range b.cutReached {
		cuts[k] = v
	}
	return Status{
		Switched:       b.switched,
		CutReached:     cuts,
		Pending:        len(b.pending),
		CommittedCount: len(b.committed),
		KnownCount:     len(b.known),
	}
}

// Idle 报告当前是否没有可立即发布的可见记录（稍后可能还会出现）。
func (b *Barrier) Idle() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending) == 0
}

// FullyDrained 报告所有已知记录是否都已拿到下游回执。
func (b *Barrier) FullyDrained() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending) == 0 && len(b.committed) == len(b.known)
}
