package waldb

import (
	"encoding/binary"
	"sync"
)

// PageSize 页大小：pageLSN(8) + value(8)。有限页数据库，页数固定。
const PageSize = 16

type page struct {
	id      int32
	pageLSN int64 // 页上最近一次更新对应的日志 LSN
	value   uint64
	dirty   bool
	recLSN  int64 // 自上次落盘以来第一次弄脏该页的 LSN（脏页表用）
}

// BufferPool 是页缓存，强制执行 WAL：页落盘前对应日志必须先持久化。
type BufferPool struct {
	file     *InjectedFile
	numPages int
	lm       *LogManager
	cache    map[int32]*page
	hook     func(CrashPoint)
	locks    *lockTable
}

func newBufferPool(file *InjectedFile, numPages int, lm *LogManager, hook func(CrashPoint)) *BufferPool {
	return &BufferPool{
		file:     file,
		numPages: numPages,
		lm:       lm,
		cache:    make(map[int32]*page),
		hook:     hook,
		locks:    newLockTable(),
	}
}

func (bp *BufferPool) fetch(id int32) *page {
	if p, ok := bp.cache[id]; ok {
		return p
	}
	p := &page{id: id, pageLSN: NilLSN}
	buf := make([]byte, PageSize)
	if n, _ := bp.file.ReadAt(buf, int64(id)*PageSize); n == PageSize {
		p.pageLSN = int64(binary.LittleEndian.Uint64(buf[0:]))
		p.value = binary.LittleEndian.Uint64(buf[8:])
	}
	bp.cache[id] = p
	return p
}

// apply 把 after 镜像写入页并记录 pageLSN。
func (bp *BufferPool) apply(p *page, value uint64, lsn int64) {
	if !p.dirty {
		p.dirty = true
		p.recLSN = lsn
	}
	p.value = value
	p.pageLSN = lsn
}

// flushPage 把脏页写回页文件。WAL：先保证 pageLSN 之前的日志持久化。
func (bp *BufferPool) flushPage(p *page) {
	bp.lm.Flush(p.pageLSN)
	buf := make([]byte, PageSize)
	binary.LittleEndian.PutUint64(buf[0:], uint64(p.pageLSN))
	binary.LittleEndian.PutUint64(buf[8:], p.value)
	bp.file.WriteAt(buf, int64(p.id)*PageSize)
	bp.file.Sync()
	p.dirty = false
	if bp.hook != nil {
		bp.hook(CutPageFlush)
	}
}

// FlushAll 刷出全部脏页（steal：允许刷出未提交事务的页，撤销由日志保证）。
func (bp *BufferPool) FlushAll() {
	for _, p := range bp.cache {
		if p.dirty {
			bp.flushPage(p)
		}
	}
}

// dirtyPageTable 返回当前脏页表快照：pageID -> recLSN。
func (bp *BufferPool) dirtyPageTable() map[int32]int64 {
	m := make(map[int32]int64)
	for id, p := range bp.cache {
		if p.dirty {
			m[id] = p.recLSN
		}
	}
	return m
}

// lockTable 实现严格两阶段独占页锁：锁持有到事务提交/回滚才释放，
// 因此不存在脏写（一个事务不会覆盖另一个事务未提交的数据）。
type lockTable struct {
	mu    sync.Mutex
	cond  *sync.Cond
	owner map[int32]int64
}

func newLockTable() *lockTable {
	lt := &lockTable{owner: make(map[int32]int64)}
	lt.cond = sync.NewCond(&lt.mu)
	return lt
}

func (lt *lockTable) lock(txnID int64, pageID int32) {
	lt.mu.Lock()
	for {
		o, held := lt.owner[pageID]
		if !held || o == txnID {
			lt.owner[pageID] = txnID
			break
		}
		lt.cond.Wait()
	}
	lt.mu.Unlock()
}

func (lt *lockTable) releaseAll(txnID int64) {
	lt.mu.Lock()
	for p, o := range lt.owner {
		if o == txnID {
			delete(lt.owner, p)
		}
	}
	lt.mu.Unlock()
	lt.cond.Broadcast()
}
