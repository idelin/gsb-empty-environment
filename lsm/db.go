// Package lsm 实现一个带 Tombstone(墓碑)生命周期管理的最小 LSM 树内核。
//
// 删除语义:Delete 不原地移除数据,而是写入一条 Tombstone 标记;
// Point Get 与 Range Scan 读到 Tombstone 时将其视为 NotFound 并屏蔽旧版本;
// Compaction 把数据逐层下沉,只有当 Tombstone 抵达 Bottom-most(最后一层)、
// 确认更深层不再存在任何更老版本时,才将其物理擦除,释放空间。
package lsm

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Options 控制 Memtable 与 Compaction 行为。
type Options struct {
	// MemtableFlushThreshold:Memtable 条目数达到该值时自动 Flush,默认 1024。
	MemtableFlushThreshold int
	// MaxLevels:总层数,最后一层为 Bottom-most,默认 4。
	MaxLevels int
	// Level0Capacity:L0 触发 Compaction 的条目数上限,默认 8。
	Level0Capacity int
	// LevelSizeRatio:每往下一层容量放大的倍数,默认 4。
	LevelSizeRatio int
	// AutoCompact:Flush 后是否自动级联 Compaction,默认 false(手动驱动)。
	AutoCompact bool
}

func (o *Options) withDefaults() Options {
	out := Options{
		MemtableFlushThreshold: 1024,
		MaxLevels:              4,
		Level0Capacity:         8,
		LevelSizeRatio:         4,
	}
	if o != nil {
		if o.MemtableFlushThreshold > 0 {
			out.MemtableFlushThreshold = o.MemtableFlushThreshold
		}
		if o.MaxLevels > 0 {
			out.MaxLevels = o.MaxLevels
		}
		if o.Level0Capacity > 0 {
			out.Level0Capacity = o.Level0Capacity
		}
		if o.LevelSizeRatio > 0 {
			out.LevelSizeRatio = o.LevelSizeRatio
		}
		out.AutoCompact = o.AutoCompact
	}
	if out.MaxLevels < 2 {
		out.MaxLevels = 2
	}
	return out
}

// DB 是 LSM 树实例。levels[0] 为 L0(最新),levels[MaxLevels-1] 为 Bottom-most。
// 每层内的 SSTable 按"新的在前"排列。
type DB struct {
	dir    string
	opts   Options
	mu     sync.RWMutex
	mem    *Memtable
	seq    uint64
	nextID uint64
	levels [][]*SSTable
	closed bool
}

// Open 打开(必要时创建)dir 下的数据库,并恢复已落盘的 SSTable 与序号。
func Open(dir string, opts *Options) (*DB, error) {
	o := opts.withDefaults()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	db := &DB{
		dir:    dir,
		opts:   o,
		mem:    newMemtable(),
		nextID: 1,
		levels: make([][]*SSTable, o.MaxLevels),
	}
	if data, err := ioutil.ReadFile(filepath.Join(dir, "CURRENT")); err == nil {
		fmt.Sscanf(string(data), "%d\n%d\n", &db.seq, &db.nextID)
	}
	fis, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, fi := range fis {
		name := fi.Name()
		if !strings.HasSuffix(name, ".sst") {
			continue
		}
		var level int
		var id uint64
		if _, err := fmt.Sscanf(name, "L%d-%d.sst", &level, &id); err != nil {
			continue
		}
		if level < 0 || level >= o.MaxLevels {
			continue
		}
		t, err := openSSTable(filepath.Join(dir, name), id)
		if err != nil {
			return nil, err
		}
		db.levels[level] = append(db.levels[level], t)
		if id >= db.nextID {
			db.nextID = id + 1
		}
	}
	for l := range db.levels {
		sort.Slice(db.levels[l], func(i, j int) bool {
			return db.levels[l][i].id > db.levels[l][j].id
		})
	}
	return db, nil
}

// Put 写入一个键值对。
func (db *DB) Put(key string, value []byte) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return ErrClosed
	}
	db.seq++
	db.mem.Put(Entry{Key: key, Value: value, Seq: db.seq})
	if db.mem.Len() >= db.opts.MemtableFlushThreshold {
		return db.flushLocked()
	}
	return nil
}

// Delete 写入 Tombstone。数据本身留待 Bottom-most 层 Compaction 物理清除。
func (db *DB) Delete(key string) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return ErrClosed
	}
	db.seq++
	db.mem.Put(Entry{Key: key, Seq: db.seq, Tombstone: true})
	if db.mem.Len() >= db.opts.MemtableFlushThreshold {
		return db.flushLocked()
	}
	return nil
}

// Get 点查。Memtable -> L0(新到旧)-> L1 ... 依次查找,
// 命中的第一条记录若是 Tombstone,立即返回 ErrNotFound。
func (db *DB) Get(key string) ([]byte, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.closed {
		return nil, ErrClosed
	}
	if e, ok := db.mem.Get(key); ok {
		if e.Tombstone {
			return nil, ErrNotFound
		}
		return append([]byte(nil), e.Value...), nil
	}
	for _, tables := range db.levels {
		for _, t := range tables {
			e, found, err := t.Get(key)
			if err != nil {
				return nil, err
			}
			if found {
				if e.Tombstone {
					return nil, ErrNotFound
				}
				return append([]byte(nil), e.Value...), nil
			}
		}
	}
	return nil, ErrNotFound
}

// Scan 范围扫描 [start, end),空字符串表示无界。
// 归并所有数据源,每个 Key 只保留最新版本,Tombstone 版本被跳过。
func (db *DB) Scan(start, end string) ([]KV, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.closed {
		return nil, ErrClosed
	}
	var iters []entryIterator
	iters = append(iters, newSliceIterator(db.mem.Sorted()))
	for _, tables := range db.levels {
		for _, t := range tables {
			it, err := t.Iterator()
			if err != nil {
				return nil, err
			}
			iters = append(iters, it)
		}
	}
	defer func() {
		for _, it := range iters {
			it.Close()
		}
	}()
	mi := newMergeIterator(iters)
	var out []KV
	for mi.Next() {
		e := mi.Entry()
		if start != "" && e.Key < start {
			continue
		}
		if end != "" && e.Key >= end {
			break
		}
		if e.Tombstone {
			continue
		}
		out = append(out, KV{Key: e.Key, Value: append([]byte(nil), e.Value...)})
	}
	return out, nil
}

// Flush 把当前 Memtable 落盘为 L0 的 SSTable。
func (db *DB) Flush() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return ErrClosed
	}
	return db.flushLocked()
}

func (db *DB) flushLocked() error {
	if db.mem.Len() == 0 {
		return nil
	}
	entries := db.mem.Sorted()
	id := db.nextID
	db.nextID++
	path := filepath.Join(db.dir, fmt.Sprintf("L0-%d.sst", id))
	t, err := writeSSTable(path, id, entries)
	if err != nil {
		return err
	}
	db.levels[0] = append([]*SSTable{t}, db.levels[0]...)
	db.mem = newMemtable()
	if err := db.persistLocked(); err != nil {
		return err
	}
	if db.opts.AutoCompact {
		return db.compactLocked()
	}
	return nil
}

// Compact 反复执行单步合并,直到所有层都低于容量水位。
func (db *DB) Compact() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return ErrClosed
	}
	return db.compactLocked()
}

// CompactOnce 执行一次单步合并(把第一个超容的层整体并入下一层)。
// 返回是否发生了合并,便于调用方观察 Tombstone 逐层下沉的过程。
func (db *DB) CompactOnce() (bool, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return false, ErrClosed
	}
	return db.compactOnceLocked()
}

func (db *DB) compactLocked() error {
	for {
		merged, err := db.compactOnceLocked()
		if err != nil {
			return err
		}
		if !merged {
			return nil
		}
	}
}

func (db *DB) compactOnceLocked() (bool, error) {
	for l := 0; l < db.opts.MaxLevels-1; l++ {
		if db.levelEntries(l) > db.levelCap(l) {
			return true, db.mergeIntoLocked(l, l+1)
		}
	}
	return false, nil
}

// mergeIntoLocked 把 from 层与 to 层的全部 SSTable 归并为一个新 SSTable 放入 to 层。
//
// Tombstone 生命周期规则:
//   - to 不是 Bottom-most:更深层可能仍有被该 Tombstone 屏蔽的旧版本,
//     必须保留 Tombstone;
//   - to 是 Bottom-most:之下再无数据,Tombstone 已完成使命,
//     连同被它屏蔽的旧版本一起物理擦除,释放空间。
func (db *DB) mergeIntoLocked(from, to int) error {
	tables := make([]*SSTable, 0, len(db.levels[from])+len(db.levels[to]))
	tables = append(tables, db.levels[from]...)
	tables = append(tables, db.levels[to]...)
	dropTombstones := to == db.opts.MaxLevels-1

	var iters []entryIterator
	for _, t := range tables {
		it, err := t.Iterator()
		if err != nil {
			return err
		}
		iters = append(iters, it)
	}
	mi := newMergeIterator(iters)
	var merged []Entry
	for mi.Next() {
		e := mi.Entry()
		if dropTombstones && e.Tombstone {
			continue // Bottom-most:安全物理消除 Tombstone
		}
		merged = append(merged, e)
	}
	for _, it := range iters {
		it.Close()
	}

	var out *SSTable
	if len(merged) > 0 {
		id := db.nextID
		db.nextID++
		path := filepath.Join(db.dir, fmt.Sprintf("L%d-%d.sst", to, id))
		var err error
		out, err = writeSSTable(path, id, merged)
		if err != nil {
			return err
		}
	}
	for _, t := range tables {
		t.Remove()
	}
	db.levels[from] = nil
	if out != nil {
		db.levels[to] = []*SSTable{out}
	} else {
		db.levels[to] = nil
	}
	return db.persistLocked()
}

// levelCap 返回第 l 层的条目容量,Bottom-most 层不设限。
func (db *DB) levelCap(l int) int {
	if l >= db.opts.MaxLevels-1 {
		return int(^uint(0) >> 1)
	}
	cap := db.opts.Level0Capacity
	for i := 0; i < l; i++ {
		cap *= db.opts.LevelSizeRatio
	}
	return cap
}

func (db *DB) levelEntries(l int) int {
	total := 0
	for _, t := range db.levels[l] {
		total += t.entries
	}
	return total
}

func (db *DB) persistLocked() error {
	content := fmt.Sprintf("%d\n%d\n", db.seq, db.nextID)
	return ioutil.WriteFile(filepath.Join(db.dir, "CURRENT"), []byte(content), 0644)
}

// LevelStats 描述某一层的占用情况。
type LevelStats struct {
	Level   int
	Tables  int
	Entries int
}

// Stats 返回各层统计信息。
func (db *DB) Stats() []LevelStats {
	db.mu.RLock()
	defer db.mu.RUnlock()
	out := make([]LevelStats, db.opts.MaxLevels)
	for l := range db.levels {
		out[l].Level = l
		out[l].Tables = len(db.levels[l])
		for _, t := range db.levels[l] {
			out[l].Entries += t.entries
		}
	}
	return out
}

// MaxLevels 返回层数配置。
func (db *DB) MaxLevels() int { return db.opts.MaxLevels }

// ProbeResult 描述某个 Key 在存储体系中的一处物理痕迹。
type ProbeResult struct {
	Level     int // -1 表示 Memtable
	TableID   uint64
	Tombstone bool
	Seq       uint64
}

// Probe 列出 key 当前在 Memtable 与各层 SSTable 中的全部物理记录,
// 用于观测 Tombstone 的下沉与最终擦除。
func (db *DB) Probe(key string) ([]ProbeResult, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	var out []ProbeResult
	if e, ok := db.mem.Get(key); ok {
		out = append(out, ProbeResult{Level: -1, Tombstone: e.Tombstone, Seq: e.Seq})
	}
	for l, tables := range db.levels {
		for _, t := range tables {
			e, found, err := t.Get(key)
			if err != nil {
				return nil, err
			}
			if found {
				out = append(out, ProbeResult{
					Level:     l,
					TableID:   t.id,
					Tombstone: e.Tombstone,
					Seq:       e.Seq,
				})
			}
		}
	}
	return out, nil
}

// Close 把 Memtable 落盘并关闭数据库。
// 注意:本实现不含 WAL,未 Flush 的 Memtable 数据在进程崩溃时会丢失。
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return nil
	}
	if err := db.flushLocked(); err != nil {
		return err
	}
	db.closed = true
	return db.persistLocked()
}
