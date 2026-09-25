// Package pagealloc 管理固定大小页的申请与释放，支持连续区间申请、
// 事务回滚，以及基于预写日志的崩溃恢复。
package pagealloc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var (
	// ErrNoSpace 表示没有足够大的连续空闲区间。
	ErrNoSpace = errors.New("pagealloc: no contiguous free range")
	// ErrDoubleFree 表示释放了一页当前已空闲的页。
	ErrDoubleFree = errors.New("pagealloc: double free")
	// ErrOutOfRange 表示页号超出 [0, PageCount)。
	ErrOutOfRange = errors.New("pagealloc: page id out of range")
	// ErrTxnClosed 表示事务已提交或已回滚。
	ErrTxnClosed = errors.New("pagealloc: transaction already closed")
)

const (
	stateFile   = "pages.state"
	journalFile = "pages.journal"
)

// Hooks 是故障注入点，仅用于测试与故障演练。
// 任一钩子返回非 nil 错误时，Commit 立即中止并把该错误返回给调用方，
// 磁盘上已完成的写入保持原样，等价于在该时刻掉电/崩溃。
type Hooks struct {
	// AfterJournalOpsSync 在操作记录已写入并 fsync、commit 记录尚未写入时触发。
	AfterJournalOpsSync func() error
	// AfterJournalCommitSync 在 commit 记录已写入并 fsync、状态尚未落盘时触发。
	AfterJournalCommitSync func() error
	// AfterStatePersist 在状态文件已原子替换并 fsync、日志尚未清理时触发。
	AfterStatePersist func() error
}

// Options 打开或创建分配器。
type Options struct {
	// Dir 存放状态与日志文件的目录，由调用方指定，不存在时自动创建。
	Dir string
	// PageCount 总页数，仅首次创建时生效；再次打开时若为非零则校验一致性。
	PageCount uint64
	// PageSize 页大小（字节），仅作元数据保存，缺省 4096。
	PageSize uint32
	// Hooks 故障注入点，可为 nil。
	Hooks *Hooks
}

// Allocator 是页分配器。同一时刻只允许一个活跃事务。
type Allocator struct {
	dir       string
	pageSize  uint32
	pageCount uint64
	free      *freeSet
	hooks     *Hooks

	mu    sync.Mutex // 串行化提交流程
	txnMu sync.Mutex // Begin 到 Commit/Rollback 期间持有
}

// Open 打开 Dir 下的分配器；目录为空时按 PageCount 创建。
// 打开时自动执行崩溃恢复：已提交日志重放，未提交日志丢弃。
func Open(opts Options) (*Allocator, error) {
	if opts.Dir == "" {
		return nil, errors.New("pagealloc: Dir is required")
	}
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return nil, err
	}
	a := &Allocator{dir: opts.Dir, hooks: opts.Hooks}
	if a.hooks == nil {
		a.hooks = &Hooks{}
	}
	st, err := loadState(a.statePath())
	switch {
	case errors.Is(err, os.ErrNotExist):
		if opts.PageCount == 0 {
			return nil, errors.New("pagealloc: PageCount required to create allocator")
		}
		ps := opts.PageSize
		if ps == 0 {
			ps = 4096
		}
		st = &state{pageSize: ps, pageCount: opts.PageCount, free: newFreeSet(opts.PageCount)}
		if err := persistState(a.dir, a.statePath(), st); err != nil {
			return nil, fmt.Errorf("pagealloc: init state: %w", err)
		}
	case err != nil:
		return nil, err
	default:
		if opts.PageCount != 0 && opts.PageCount != st.pageCount {
			return nil, fmt.Errorf("pagealloc: page count mismatch: on-disk %d, requested %d",
				st.pageCount, opts.PageCount)
		}
	}
	a.pageSize, a.pageCount, a.free = st.pageSize, st.pageCount, st.free
	if err := a.recover(); err != nil {
		return nil, err
	}
	return a, nil
}

// PageCount 返回总页数。
func (a *Allocator) PageCount() uint64 { return a.pageCount }

// PageSize 返回页大小（字节）。
func (a *Allocator) PageSize() uint32 { return a.pageSize }

// FreeCount 返回当前空闲页总数。
func (a *Allocator) FreeCount() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.free.total()
}

func (a *Allocator) statePath() string   { return filepath.Join(a.dir, stateFile) }
func (a *Allocator) journalPath() string { return filepath.Join(a.dir, journalFile) }

// recover 在打开时重放或丢弃日志。
func (a *Allocator) recover() error {
	data, err := os.ReadFile(a.journalPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	ops, committed, err := decodeJournal(data)
	if err != nil {
		return err
	}
	if committed {
		// 状态文件要么完整包含本事务、要么完全不包含（rename 原子），
		// 因此按幂等语义逐条重放即可。
		for _, o := range ops {
			switch o.typ {
			case opAlloc:
				if a.free.containsRange(o.start, o.count) {
					a.free.removeRange(o.start, o.count)
				}
			case opFree:
				if !a.free.overlaps(o.start, o.count) {
					a.free.insert(o.start, o.count)
				}
			}
		}
		if err := a.persist(); err != nil {
			return err
		}
	}
	// 无论提交与否，日志处理完毕后删除并落盘目录项。
	if err := os.Remove(a.journalPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDir(a.dir)
}

// Txn 是一次事务。事务内的释放只在 Commit 后对其他事务可见；
// Rollback 丢弃全部操作，已申请的页立即可重用。
type Txn struct {
	a      *Allocator
	view   *freeSet // 提交时分配器状态的快照，减去本事务已申请的页
	freed  *freeSet // 本事务内已释放的页，用于检测事务内双重释放
	ops    []op
	closed bool
}

// Begin 开启事务。同一时间只允许一个活跃事务。
func (a *Allocator) Begin() *Txn {
	a.txnMu.Lock()
	a.mu.Lock()
	view := a.free.clone()
	a.mu.Unlock()
	return &Txn{a: a, view: view, freed: &freeSet{}}
}

// Alloc 申请 n 个连续页，返回起始页号。结果在 Commit 后生效。
func (t *Txn) Alloc(n uint64) (uint64, error) {
	if t.closed {
		return 0, ErrTxnClosed
	}
	if n == 0 || n > t.a.pageCount {
		return 0, ErrOutOfRange
	}
	start, ok := t.view.alloc(n)
	if !ok {
		return 0, ErrNoSpace
	}
	t.ops = append(t.ops, op{typ: opAlloc, start: start, count: n})
	return start, nil
}

// Free 释放单页。
func (t *Txn) Free(id uint64) error { return t.FreeRange(id, 1) }

// FreeRange 释放 [start, start+count)。越界返回 ErrOutOfRange，
// 范围内任何页当前已空闲（含本事务内重复释放）返回 ErrDoubleFree。
func (t *Txn) FreeRange(start, count uint64) error {
	if t.closed {
		return ErrTxnClosed
	}
	if count == 0 || start >= t.a.pageCount || count > t.a.pageCount-start {
		return ErrOutOfRange
	}
	if t.view.overlaps(start, count) || t.freed.overlaps(start, count) {
		return ErrDoubleFree
	}
	// 注意：不加入 view，事务内释放的页在提交前不可被本事务重新申请。
	t.freed.insert(start, count)
	t.ops = append(t.ops, op{typ: opFree, start: start, count: count})
	return nil
}

// Commit 提交事务：先写日志并 fsync，再写 commit 记录并 fsync，
// 然后原子替换状态文件，最后清理日志。任何一步崩溃都可在下次
// Open 时恢复到一致状态。
func (t *Txn) Commit() error {
	defer t.a.txnMu.Unlock()
	if t.closed {
		return ErrTxnClosed
	}
	t.closed = true
	if len(t.ops) == 0 {
		return nil
	}
	a := t.a
	a.mu.Lock()
	defer a.mu.Unlock()

	// 1. 写操作记录 + fsync
	opsBytes := encodeJournalOps(t.ops)
	if err := a.writeJournal(opsBytes); err != nil {
		return err
	}
	if h := a.hooks.AfterJournalOpsSync; h != nil {
		if err := h(); err != nil {
			return err
		}
	}
	// 2. 写 commit 记录 + fsync（原子提交点）
	if err := a.commitJournal(opsBytes); err != nil {
		return err
	}
	if h := a.hooks.AfterJournalCommitSync; h != nil {
		if err := h(); err != nil {
			return err
		}
	}
	// 3. 应用到内存并原子持久化状态
	for _, o := range t.ops {
		if o.typ == opAlloc {
			a.free.removeRange(o.start, o.count)
		} else {
			a.free.insert(o.start, o.count)
		}
	}
	if err := a.persist(); err != nil {
		return err
	}
	if h := a.hooks.AfterStatePersist; h != nil {
		if err := h(); err != nil {
			return err
		}
	}
	// 4. 清理日志
	if err := os.Remove(a.journalPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDir(a.dir)
}

// Rollback 回滚事务，丢弃全部操作；磁盘与内存状态均不受影响。
func (t *Txn) Rollback() {
	defer t.a.txnMu.Unlock()
	t.closed = true
}

func (a *Allocator) writeJournal(opsBytes []byte) error {
	f, err := os.OpenFile(a.journalPath(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(opsBytes); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (a *Allocator) commitJournal(opsBytes []byte) error {
	f, err := os.OpenFile(a.journalPath(), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(encodeCommitRecord(opsBytes)); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return syncDir(a.dir)
}

func (a *Allocator) persist() error {
	return persistState(a.dir, a.statePath(), &state{
		pageSize:  a.pageSize,
		pageCount: a.pageCount,
		free:      a.free,
	})
}
