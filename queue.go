// Package diskqueue 实现磁盘优先（WAL 先行）的持久化优先级队列。
//
// 特性：
//   - 每个任务分配单调递增的稳定序号 seq，重启后不重置；
//   - 相同优先级按入队顺序（FIFO）出队；
//   - 支持优先级更新、删除、原子批量弹出（PopBatch）；
//   - 所有变更先写 WAL（带 CRC 帧），重启后回放恢复；
//   - 弹出事务先落盘 commit 记录再从内存移除，失败不丢任务；
//   - 回放幂等：旧更新记录不会让已删除任务复活。
//
// 持久化边界见 README.md。
package diskqueue

import (
	"container/heap"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

var (
	// ErrClosed 队列已关闭。
	ErrClosed = errors.New("diskqueue: queue closed")
	// ErrNotFound 指定 seq 的任务不存在（未入队、已删除或已弹出）。
	ErrNotFound = errors.New("diskqueue: task not found")
)

// Item 是弹出时返回的任务。
type Item struct {
	Seq      uint64 // 稳定序号，入队时分配，单调递增
	Priority int64  // 优先级，小的先出队，允许负数
	Payload  []byte
}

// Option 是 Open 的配置项。
type Option func(*Queue)

// WithSyncOnWrite 控制每条记录是否 fsync（默认 true）。
// 为 false 时进程崩溃仍不丢数据（OS 页缓存仍在），但断电可能丢失最近已确认的操作。
// 注意：PopBatch 的 commit 记录始终 fsync，与该选项无关。
func WithSyncOnWrite(sync bool) Option {
	return func(q *Queue) { q.syncOnWrite = sync }
}

// WithFaultHook 设置故障注入钩子，用于测试崩溃/断电路径。
func WithFaultHook(h FaultHook) Option {
	return func(q *Queue) { q.hook = h }
}

// Queue 是持久化优先级队列。所有文件都位于 Open 指定的目录下。
type Queue struct {
	dir         string
	mu          sync.Mutex
	wal         *os.File
	h           entryHeap
	bySeq       map[uint64]*entry
	nextSeq     uint64 // 下一个分配的序号
	baseSeq     uint64 // 快照点之后的序号下界，用于回放时跳过已合并记录
	syncOnWrite bool
	hook        FaultHook
	closed      bool
}

// Open 打开（或创建）目录 dir 下的队列，回放 WAL 恢复状态。
func Open(dir string, opts ...Option) (*Queue, error) {
	q := &Queue{
		dir:         dir,
		bySeq:       make(map[uint64]*entry),
		syncOnWrite: true,
	}
	for _, o := range opts {
		o(q)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	snapPath := filepath.Join(dir, "snapshot")
	if _, err := os.Stat(snapPath); err == nil {
		if err := q.loadSnapshot(snapPath); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	walPath := filepath.Join(dir, "wal.log")
	created := false
	if _, err := os.Stat(walPath); os.IsNotExist(err) {
		created = true
	}
	f, err := os.OpenFile(walPath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	q.wal = f

	maxSeq := q.nextSeq
	good, err := replayWAL(f, func(typ byte, payload []byte) error {
		return q.applyRecord(typ, payload, &maxSeq)
	})
	if err != nil {
		f.Close()
		return nil, err
	}
	// 截断撕裂尾（崩溃/断电留下的半帧），后续追加从完好位置继续。
	if fi, err := f.Stat(); err == nil && fi.Size() > good {
		if err := f.Truncate(good); err != nil {
			f.Close()
			return nil, err
		}
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return nil, err
	}
	if created {
		if err := syncDir(dir); err != nil {
			f.Close()
			return nil, err
		}
	}
	q.nextSeq = maxSeq
	return q, nil
}

// applyRecord 幂等地回放一条 WAL 记录。
// 对不存在（已删除/已弹出/已合并进快照）的 seq，Update/Delete/PopCommit 一律视为无操作，
// 因此旧的 Update 记录无法让已删除的任务复活。
func (q *Queue) applyRecord(typ byte, p []byte, maxSeq *uint64) error {
	switch typ {
	case recEnqueue:
		seq, prio, payload, err := decodeEnqueue(p)
		if err != nil {
			return err
		}
		if seq < q.baseSeq {
			return nil // 已合并进快照
		}
		if _, ok := q.bySeq[seq]; ok {
			return nil
		}
		e := &entry{seq: seq, prio: prio, payload: payload}
		heap.Push(&q.h, e)
		q.bySeq[seq] = e
		if seq+1 > *maxSeq {
			*maxSeq = seq + 1
		}
	case recUpdate:
		seq, prio, err := decodeUpdate(p)
		if err != nil {
			return err
		}
		if e, ok := q.bySeq[seq]; ok {
			e.prio = prio
			heap.Fix(&q.h, e.index)
		}
	case recDelete:
		seq, err := decodeSeq(p)
		if err != nil {
			return err
		}
		if e, ok := q.bySeq[seq]; ok {
			q.removeEntry(e)
		}
	case recPopCommit:
		seqs, err := decodePopCommit(p)
		if err != nil {
			return err
		}
		for _, s := range seqs {
			if e, ok := q.bySeq[s]; ok {
				q.removeEntry(e)
			}
		}
	default:
		return fmt.Errorf("diskqueue: unknown record type %d", typ)
	}
	return nil
}

// append 写一条 WAL 记录，按配置 fsync。注入点按序触发。
func (q *Queue) append(typ byte, payload []byte) error {
	if err := q.inject(FaultBeforeAppend, typ); err != nil {
		return err
	}
	if err := appendRecord(q.wal, typ, payload); err != nil {
		return err
	}
	if err := q.inject(FaultAfterAppendBeforeSync, typ); err != nil {
		return err
	}
	if q.syncOnWrite || typ == recPopCommit {
		if err := q.wal.Sync(); err != nil {
			return err
		}
	}
	return q.inject(FaultAfterSync, typ)
}

// Enqueue 入队一个任务，返回分配的稳定序号。优先级允许负数。
func (q *Queue) Enqueue(priority int64, payload []byte) (uint64, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return 0, ErrClosed
	}
	seq := q.nextSeq
	if err := q.append(recEnqueue, encodeEnqueue(seq, priority, payload)); err != nil {
		return 0, err
	}
	e := &entry{seq: seq, prio: priority, payload: append([]byte(nil), payload...)}
	heap.Push(&q.h, e)
	q.bySeq[seq] = e
	q.nextSeq++
	return seq, nil
}

// Update 修改任务优先级。任务不存在时返回 ErrNotFound，且不产生任何记录，
// 因此不可能把已删除的任务“更新回来”。
func (q *Queue) Update(seq uint64, priority int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrClosed
	}
	e, ok := q.bySeq[seq]
	if !ok {
		return ErrNotFound
	}
	if err := q.append(recUpdate, encodeUpdate(seq, priority)); err != nil {
		return err
	}
	e.prio = priority
	heap.Fix(&q.h, e.index)
	return nil
}

// Delete 删除任务。任务不存在时返回 ErrNotFound。
func (q *Queue) Delete(seq uint64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrClosed
	}
	e, ok := q.bySeq[seq]
	if !ok {
		return ErrNotFound
	}
	if err := q.append(recDelete, encodeSeq(seq)); err != nil {
		return err
	}
	q.removeEntry(e)
	return nil
}

// PopBatch 原子弹出最多 n 个优先级最高的任务（同优先级按入队序）。
//
// 实现：先在堆副本上选出任务，把 commit 记录（含全部 seq）写入 WAL 并 fsync，
// 然后才从内存堆移除。commit 落盘前失败 → 任务原样保留，不丢；
// commit 落盘后崩溃 → 重启回放 commit，任务被移除，不会重复交付。
// 空队列或 n<=0 返回 (nil, nil)。
func (q *Queue) PopBatch(n int) ([]Item, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil, ErrClosed
	}
	if n <= 0 || len(q.h) == 0 {
		return nil, nil
	}
	if n > len(q.h) {
		n = len(q.h)
	}
	// 在副本（值拷贝，避免污染真实堆的 index）上选出 n 个最小元素。
	clone := make(entryHeap, len(q.h))
	for i, e := range q.h {
		c := *e
		clone[i] = &c
	}
	heap.Init(&clone)
	picked := make([]*entry, 0, n)
	for i := 0; i < n; i++ {
		picked = append(picked, heap.Pop(&clone).(*entry))
	}
	seqs := make([]uint64, len(picked))
	for i, e := range picked {
		seqs[i] = e.seq
	}
	if err := q.append(recPopCommit, encodePopCommit(seqs)); err != nil {
		return nil, fmt.Errorf("diskqueue: pop commit failed, tasks kept: %w", err)
	}
	items := make([]Item, len(picked))
	for i, c := range picked {
		e := q.bySeq[c.seq]
		items[i] = Item{Seq: e.seq, Priority: e.prio, Payload: append([]byte(nil), e.payload...)}
		q.removeEntry(e)
	}
	return items, nil
}

// Len 返回当前队列中的任务数。
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.h)
}

// Compact 把当前状态写入快照（tmp+rename 原子替换）并清空 WAL，缩短重启回放时间。
func (q *Queue) Compact() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrClosed
	}
	if err := q.writeSnapshot(); err != nil {
		return err
	}
	walPath := filepath.Join(q.dir, "wal.log")
	if err := q.wal.Close(); err != nil {
		return err
	}
	if err := os.Remove(walPath); err != nil {
		return err
	}
	f, err := os.OpenFile(walPath, os.O_RDWR|os.O_CREATE|os.O_APPEND|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	q.wal = f
	q.baseSeq = q.nextSeq
	return syncDir(q.dir)
}

// Close 关闭队列。进程崩溃不经过 Close 也能从 WAL 恢复，Close 只做句柄清理。
func (q *Queue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil
	}
	q.closed = true
	return q.wal.Close()
}

func (q *Queue) removeEntry(e *entry) {
	heap.Remove(&q.h, e.index)
	delete(q.bySeq, e.seq)
}
