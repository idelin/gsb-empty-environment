package aries

import (
	"errors"
	"sort"
	"sync"
)

// ErrOutOfBounds 表示页内访问越界。
var ErrOutOfBounds = errors.New("aries: page offset out of bounds")

// Page 是缓冲池中的一帧。
type Page struct {
	ID      uint64
	Data    []byte // 长度恒为 PageDataSize
	PageLSN LSN    // 最近一次修改该页的日志 LSN
	RecLSN  LSN    // 自上次刷盘以来首次使该页变脏的日志 LSN
	Dirty   bool
}

// PageSnapshot 是增量快照归档用的页拷贝。
type PageSnapshot struct {
	ID      uint64
	PageLSN LSN
	Data    []byte
}

// BufferPool 实现 steal / no-force 缓冲池:
//   - steal: 未提交事务的脏页可被驱逐写盘;
//   - no-force: 事务提交时不强制刷页。
//
// 写盘前遵循 WAL 规则:先 Flush 日志至 pageLSN。
//
// 锁序约定:db.mu < bp.mu < lm.mu,严禁反向持锁。
type BufferPool struct {
	mu       sync.Mutex
	dm       *DiskManager
	lm       *LogManager
	capacity int
	frames   map[uint64]*Page
	changed  map[uint64]bool // 自上次快照排水(DrainChanged)以来被修改过的页
}

// NewBufferPool 创建缓冲池。
func NewBufferPool(dm *DiskManager, lm *LogManager, capacity int) *BufferPool {
	if capacity < 1 {
		capacity = 1
	}
	return &BufferPool{
		dm:       dm,
		lm:       lm,
		capacity: capacity,
		frames:   make(map[uint64]*Page),
		changed:  make(map[uint64]bool),
	}
}

// fetchLocked 取页(必要时从磁盘加载或驱逐)。调用方须持有 bp.mu。
func (bp *BufferPool) fetchLocked(id uint64) (*Page, error) {
	if p, ok := bp.frames[id]; ok {
		return p, nil
	}
	if len(bp.frames) >= bp.capacity {
		if err := bp.evictOneLocked(); err != nil {
			return nil, err
		}
	}
	data, pageLSN, err := bp.dm.ReadPage(id)
	if err != nil {
		return nil, err
	}
	p := &Page{ID: id, Data: data, PageLSN: pageLSN}
	bp.frames[id] = p
	return p, nil
}

// evictOneLocked 驱逐一页;脏页先按 WAL 规则刷盘。
func (bp *BufferPool) evictOneLocked() error {
	for id, p := range bp.frames {
		if err := bp.flushLocked(p); err != nil {
			return err
		}
		delete(bp.frames, id)
		return nil
	}
	return nil
}

// flushLocked 将脏页写盘。调用方须持有 bp.mu。
func (bp *BufferPool) flushLocked(p *Page) error {
	if !p.Dirty {
		return nil
	}
	// WAL 规则:日志先落盘。
	if err := bp.lm.Flush(p.PageLSN); err != nil {
		return err
	}
	if err := bp.dm.WritePage(p.ID, p.Data, p.PageLSN); err != nil {
		return err
	}
	p.Dirty = false
	p.RecLSN = NilLSN
	return nil
}

// BeforeImage 读取页内 [off, off+length) 的当前内容(用于写日志前像)。
func (bp *BufferPool) BeforeImage(id uint64, off int, length int) ([]byte, error) {
	if off < 0 || length < 0 || off+length > PageDataSize {
		return nil, ErrOutOfBounds
	}
	bp.mu.Lock()
	defer bp.mu.Unlock()
	p, err := bp.fetchLocked(id)
	if err != nil {
		return nil, err
	}
	out := make([]byte, length)
	copy(out, p.Data[off:off+length])
	return out, nil
}

// ApplyLogged 在日志已追加(LSN 已知)后,把修改应用到缓冲页。
func (bp *BufferPool) ApplyLogged(id uint64, off int, data []byte, lsn LSN) error {
	if off < 0 || off+len(data) > PageDataSize {
		return ErrOutOfBounds
	}
	bp.mu.Lock()
	defer bp.mu.Unlock()
	p, err := bp.fetchLocked(id)
	if err != nil {
		return err
	}
	if !p.Dirty {
		p.RecLSN = lsn
	}
	copy(p.Data[off:], data)
	p.Dirty = true
	p.PageLSN = lsn
	bp.changed[id] = true
	return nil
}

// ReadPage 返回页内容拷贝与其 pageLSN。
func (bp *BufferPool) ReadPage(id uint64) ([]byte, LSN, error) {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	p, err := bp.fetchLocked(id)
	if err != nil {
		return nil, 0, err
	}
	out := make([]byte, PageDataSize)
	copy(out, p.Data)
	return out, p.PageLSN, nil
}

// DirtyPageTable 返回当前脏页表:页 ID -> recLSN。
func (bp *BufferPool) DirtyPageTable() map[uint64]LSN {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	dpt := make(map[uint64]LSN, len(bp.frames))
	for id, p := range bp.frames {
		if p.Dirty {
			dpt[id] = p.RecLSN
		}
	}
	return dpt
}

// FlushAll 刷全部脏页(干净关闭时使用)。
func (bp *BufferPool) FlushAll() error {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	for _, p := range bp.frames {
		if err := bp.flushLocked(p); err != nil {
			return err
		}
	}
	return bp.dm.Sync()
}

// FlushPages 刷指定页(若仍在池中且脏)。
func (bp *BufferPool) FlushPages(ids []uint64) error {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	for _, id := range ids {
		if p, ok := bp.frames[id]; ok {
			if err := bp.flushLocked(p); err != nil {
				return err
			}
		}
	}
	return nil
}

// DrainChanged 收集自 base 以来、且不晚于 upto 被修改的页拷贝,
// 并将这些页从 changed 集合中移除(下次快照不再重复归档)。
// PageLSN > upto 的页保留在集合中,留给下一次快照。
// 已不在池中的页从磁盘读取(驱逐前已刷盘,内容完整)。
func (bp *BufferPool) DrainChanged(base, upto LSN) ([]PageSnapshot, error) {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	var out []PageSnapshot
	for id := range bp.changed {
		var data []byte
		var pageLSN LSN
		if p, ok := bp.frames[id]; ok {
			data = p.Data
			pageLSN = p.PageLSN
		} else {
			d, l, err := bp.dm.ReadPage(id)
			if err != nil {
				return nil, err
			}
			data, pageLSN = d, l
		}
		if pageLSN <= base {
			delete(bp.changed, id)
			continue
		}
		if pageLSN > upto {
			continue
		}
		cp := make([]byte, PageDataSize)
		copy(cp, data)
		out = append(out, PageSnapshot{ID: id, PageLSN: pageLSN, Data: cp})
		delete(bp.changed, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ChangedCount 返回待快照的变更页数(测试用)。
func (bp *BufferPool) ChangedCount() int {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	return len(bp.changed)
}
