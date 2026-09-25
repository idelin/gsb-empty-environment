// Package diskbtree 实现一个磁盘上的 B 树, 支持插入、点查和范围扫描。
//
// 核心机制: 写时复制(copy-on-write) + 双槽 superblock。
//   - 每次 Put 从叶到根复制整条路径上的节点, 写入数据文件中从未使用
//     过的新页, 绝不原地修改已提交的页。
//   - 叶子分裂时在新页内维护逻辑叶链(后继首键), 旧版本的链保持不动。
//   - 提交 = 先 fsync 数据文件, 再写入并 fsync superblock 的一个槽位。
//     superblock 翻转前崩溃, 新页全部不可达, 恢复出旧的完整状态;
//     翻转后崩溃, 恢复出新的完整状态。不存在中间态。
//   - 旧快照(Snapshot)持有旧根页号, 由于旧页永不被覆盖, 根分裂后
//     旧快照依然可以完整、一致地遍历。
package diskbtree

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

var (
	// ErrKeyTooLarge 表示单条键值对超出一页的容量。
	ErrKeyTooLarge  = errors.New("diskbtree: key/value too large for page")
	errCorrupt      = errors.New("diskbtree: corrupt page")
	errPageOverflow = errors.New("diskbtree: page overflow")
)

const (
	dataFileName = "data.db"
	metaFileName = "meta"
)

// Options 打开数据库时的选项。
type Options struct {
	// PageSize 页大小(字节), 默认 4096。测试中可调小以制造频繁分裂。
	PageSize int
	// CrashHook 故障注入钩子, 返回 true 表示在该注入点模拟崩溃。
	CrashHook func(FaultPoint) bool
}

// DB 是一个磁盘 B 树。同一时刻允许一个写事务和任意多个只读快照。
type DB struct {
	mu       sync.Mutex
	dir      string
	pageSize int
	data     *os.File
	meta     *os.File
	root     uint64 // 当前已提交的根页号
	txid     uint64 // 当前已提交的事务号
	numPages uint64 // 下一个可分配的页号
	hook     func(FaultPoint) bool
}

// Open 打开(或创建)dir 下的数据库, 并执行崩溃恢复:
// 选择 CRC 校验通过且 txid 最大的 superblock 槽位作为当前状态。
func Open(dir string, opts *Options) (*DB, error) {
	if opts == nil {
		opts = &Options{}
	}
	pageSize := opts.PageSize
	if pageSize == 0 {
		pageSize = 4096
	}
	if pageSize < headerSize+8 {
		return nil, errors.New("diskbtree: page size too small")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	data, err := os.OpenFile(filepath.Join(dir, dataFileName), os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	meta, err := os.OpenFile(filepath.Join(dir, metaFileName), os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		data.Close()
		return nil, err
	}
	db := &DB{dir: dir, pageSize: pageSize, data: data, meta: meta, hook: opts.CrashHook}

	m, err := readMeta(meta)
	if err == errNoValidMeta {
		// 没有任何合法 superblock: 仅当数据文件也为空(或只有初始化
		// 残留)时按全新库处理, 否则视为损坏。
		fi, statErr := data.Stat()
		if statErr != nil {
			db.Close()
			return nil, statErr
		}
		if fi.Size() > int64(pageSize) {
			db.Close()
			return nil, errors.New("diskbtree: meta corrupt but data file non-empty")
		}
		if err := db.initEmpty(); err != nil {
			db.Close()
			return nil, err
		}
		return db, nil
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	if int(m.pageSize) != pageSize {
		db.Close()
		return nil, errors.New("diskbtree: page size mismatch with existing database")
	}
	db.root, db.txid, db.numPages = m.root, m.txid, m.numPages
	return db, nil
}

// initEmpty 初始化空库: 页 0 为空叶子, 提交 txid=0 的 superblock。
func (db *DB) initEmpty() error {
	db.root, db.txid, db.numPages = 0, 0, 1
	if err := db.writePage(0, &page{typ: pageTypeLeaf}); err != nil {
		return err
	}
	if err := db.data.Sync(); err != nil {
		return err
	}
	if err := db.writeMeta(0, db.root, db.numPages); err != nil {
		return err
	}
	return db.meta.Sync()
}

// SetCrashHook 在运行中更换故障注入钩子(测试用)。
func (db *DB) SetCrashHook(h func(FaultPoint) bool) {
	db.mu.Lock()
	db.hook = h
	db.mu.Unlock()
}

// Close 关闭底层文件。
func (db *DB) Close() error {
	err1 := db.data.Close()
	err2 := db.meta.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

// --- 页读写 ---

func (db *DB) readPage(id uint64) (*page, error) {
	buf := make([]byte, db.pageSize)
	if _, err := db.data.ReadAt(buf, int64(id)*int64(db.pageSize)); err != nil {
		return nil, err
	}
	return decodePage(buf)
}

// allocPage 分配一个从未使用过的页号。崩溃后未提交的分配随旧
// superblock 的 numPages 一起被丢弃, 空间泄漏但不影响正确性。
func (db *DB) allocPage() uint64 {
	id := db.numPages
	db.numPages++
	return id
}

func (db *DB) writePage(id uint64, p *page) error {
	buf := make([]byte, db.pageSize)
	if err := p.encode(buf); err != nil {
		return err
	}
	_, err := db.data.WriteAt(buf, int64(id)*int64(db.pageSize))
	return err
}

// --- 插入 ---

// Put 插入或覆盖一个键值对, 每次调用是一个独立的已提交事务。
func (db *DB) Put(key, val []byte) error {
	if len(key) == 0 {
		return errors.New("diskbtree: empty key")
	}
	if headerSize+4+len(key)+len(val) > db.pageSize {
		return ErrKeyTooLarge
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	newRoot, splitKey, rightID, split, err := db.insert(db.root, key, val)
	if err != nil {
		return err
	}
	if split {
		// 根分裂: 新建更高的根。旧根页不被触碰, 旧快照仍指向它。
		nr := &page{typ: pageTypeInternal, keys: [][]byte{splitKey}, children: []uint64{newRoot, rightID}}
		nid := db.allocPage()
		if err := db.writePage(nid, nr); err != nil {
			return err
		}
		newRoot = nid
	}

	// 提交协议: 数据落盘 -> 写 superblock -> superblock 落盘。
	db.fire(FaultBeforeDataSync)
	if err := db.data.Sync(); err != nil {
		return err
	}
	db.fire(FaultAfterDataSync)
	if err := db.writeMeta(db.txid+1, newRoot, db.numPages); err != nil {
		return err
	}
	db.fire(FaultAfterMetaWrite)
	if err := db.meta.Sync(); err != nil {
		return err
	}
	db.txid++
	db.root = newRoot
	return nil
}

// insert 递归执行 COW 插入。返回新子树根的页号; 若发生分裂,
// 同时返回右兄弟的分隔键和页号。
func (db *DB) insert(id uint64, key, val []byte) (newID uint64, splitKey []byte, rightID uint64, split bool, err error) {
	pg, err := db.readPage(id)
	if err != nil {
		return 0, nil, 0, false, err
	}

	if pg.typ == pageTypeLeaf {
		i := pg.lowerBound(key)
		if i < len(pg.keys) && bytes.Equal(pg.keys[i], key) {
			pg.vals[i] = append([]byte(nil), val...) // 重复键: 覆盖
		} else {
			pg.keys = append(pg.keys, nil)
			copy(pg.keys[i+1:], pg.keys[i:])
			pg.keys[i] = append([]byte(nil), key...)
			pg.vals = append(pg.vals, nil)
			copy(pg.vals[i+1:], pg.vals[i:])
			pg.vals[i] = append([]byte(nil), val...)
		}
		if pg.size() <= db.pageSize {
			nid := db.allocPage()
			if err := db.writePage(nid, pg); err != nil {
				return 0, nil, 0, false, err
			}
			return nid, nil, 0, false, nil
		}
		// 叶子分裂: 左半留在左页, 右半进新页, 在新页内接好逻辑叶链。
		// 左页首键不变, 因此前驱叶子的 nextKey 无需修改。
		half := len(pg.keys) / 2
		left := &page{typ: pageTypeLeaf, keys: pg.keys[:half], vals: pg.vals[:half],
			hasNext: true, nextKey: pg.keys[half]}
		right := &page{typ: pageTypeLeaf, keys: pg.keys[half:], vals: pg.vals[half:],
			hasNext: pg.hasNext, nextKey: pg.nextKey}
		lid, rid := db.allocPage(), db.allocPage()
		if err := db.writePage(lid, left); err != nil {
			return 0, nil, 0, false, err
		}
		if err := db.writePage(rid, right); err != nil {
			return 0, nil, 0, false, err
		}
		return lid, right.keys[0], rid, true, nil
	}

	// 内部节点: 找到子树递归, 再用新子页号替换旧指针。
	ci := pg.childIndex(key)
	childNew, sk, rid, childSplit, err := db.insert(pg.children[ci], key, val)
	if err != nil {
		return 0, nil, 0, false, err
	}
	pg.children[ci] = childNew
	if childSplit {
		pg.keys = append(pg.keys, nil)
		copy(pg.keys[ci+1:], pg.keys[ci:])
		pg.keys[ci] = sk
		pg.children = append(pg.children, 0)
		copy(pg.children[ci+2:], pg.children[ci+1:])
		pg.children[ci+1] = rid
	}
	if pg.size() <= db.pageSize {
		nid := db.allocPage()
		if err := db.writePage(nid, pg); err != nil {
			return 0, nil, 0, false, err
		}
		return nid, nil, 0, false, nil
	}
	// 内部节点分裂: 中间键上提。
	mid := len(pg.keys) / 2
	upKey := pg.keys[mid]
	left := &page{typ: pageTypeInternal, keys: pg.keys[:mid], children: pg.children[:mid+1]}
	right := &page{typ: pageTypeInternal, keys: pg.keys[mid+1:], children: pg.children[mid+1:]}
	lid, rid2 := db.allocPage(), db.allocPage()
	if err := db.writePage(lid, left); err != nil {
		return 0, nil, 0, false, err
	}
	if err := db.writePage(rid2, right); err != nil {
		return 0, nil, 0, false, err
	}
	return lid, upKey, rid2, true, nil
}

// --- 查询 ---

// Get 点查。返回的切片是副本。
func (db *DB) Get(key []byte) ([]byte, bool, error) {
	db.mu.Lock()
	root := db.root
	db.mu.Unlock()
	return db.getFrom(root, key)
}

func (db *DB) getFrom(root uint64, key []byte) ([]byte, bool, error) {
	id := root
	for {
		pg, err := db.readPage(id)
		if err != nil {
			return nil, false, err
		}
		if pg.typ == pageTypeLeaf {
			i := pg.lowerBound(key)
			if i < len(pg.keys) && bytes.Equal(pg.keys[i], key) {
				return append([]byte(nil), pg.vals[i]...), true, nil
			}
			return nil, false, nil
		}
		id = pg.children[pg.childIndex(key)]
	}
}

// Snapshot 捕获当前已提交状态。旧快照引用的页永不被覆盖,
// 因此之后的插入、覆盖、根分裂都不影响该快照的遍历结果。
type Snapshot struct {
	db   *DB
	root uint64
}

// Snapshot 返回当前已提交状态的快照。
func (db *DB) Snapshot() *Snapshot {
	db.mu.Lock()
	defer db.mu.Unlock()
	return &Snapshot{db: db, root: db.root}
}

// Get 在快照上点查。
func (s *Snapshot) Get(key []byte) ([]byte, bool, error) {
	return s.db.getFrom(s.root, key)
}

// Scan 在快照上创建范围迭代器, [start, end), end 为 nil 表示到末尾。
func (s *Snapshot) Scan(start, end []byte) *Iterator {
	return &Iterator{db: s.db, root: s.root, start: start, end: end}
}

// Scan 在当前已提交状态上创建范围迭代器, [start, end)。
func (db *DB) Scan(start, end []byte) *Iterator {
	return db.Snapshot().Scan(start, end)
}

// Iterator 沿逻辑叶链做范围遍历。叶子页不可变, 跨叶时按 nextKey
// 从快照根重新下钻, 因此跨叶遍历不会重复也不会遗漏。
type Iterator struct {
	db      *DB
	root    uint64
	start   []byte
	end     []byte
	leaf    *page
	idx     int
	started bool
	done    bool
	err     error
}

// Next 前进到下一个条目。
func (it *Iterator) Next() bool {
	if it.done {
		return false
	}
	if !it.started {
		it.started = true
		if !it.seek() {
			it.done = true
			return false
		}
	} else {
		it.idx++
	}
	for {
		if it.idx < len(it.leaf.keys) {
			if it.end != nil && bytes.Compare(it.leaf.keys[it.idx], it.end) >= 0 {
				it.done = true
				return false
			}
			return true
		}
		if !it.leaf.hasNext {
			it.done = true
			return false
		}
		// 跨叶: 按后继首键从快照根下钻定位下一个叶子。
		if !it.descend(it.leaf.nextKey) {
			it.done = true
			return false
		}
	}
}

func (it *Iterator) seek() bool {
	if it.start == nil {
		return it.descend(nil)
	}
	return it.descend(it.start)
}

// descend 从快照根下钻到 key 所在叶子并定位到 lowerBound;
// key 为 nil 时定位到最左叶子的开头。
func (it *Iterator) descend(key []byte) bool {
	id := it.root
	for {
		pg, err := it.db.readPage(id)
		if err != nil {
			it.err = err
			return false
		}
		if pg.typ == pageTypeLeaf {
			it.leaf = pg
			if key != nil {
				it.idx = pg.lowerBound(key)
			} else {
				it.idx = 0
			}
			return true
		}
		if key == nil {
			id = pg.children[0]
		} else {
			id = pg.children[pg.childIndex(key)]
		}
	}
}

// Key 返回当前键(副本)。
func (it *Iterator) Key() []byte { return append([]byte(nil), it.leaf.keys[it.idx]...) }

// Value 返回当前值(副本)。
func (it *Iterator) Value() []byte { return append([]byte(nil), it.leaf.vals[it.idx]...) }

// Err 返回遍历过程中遇到的错误。
func (it *Iterator) Err() error { return it.err }
