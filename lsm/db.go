// Package lsm 实现一个极简 LSM 存储引擎：memtable + WAL + 不可变 SSTable +
// 合并式 Compaction。SSTable 一旦落盘即不可变，因此备份组件可以用硬链接
// 实现零拷贝快照。
package lsm

import (
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Options 控制 flush / compaction 触发条件。
type Options struct {
	// FlushThresholdBytes: memtable 达到该字节数即 flush 为 SSTable。
	FlushThresholdBytes int
	// CompactThreshold: SSTable 数量达到该值即触发全量合并。
	CompactThreshold int
}

// DB 是一个极简 LSM 存储引擎。
type DB struct {
	dir              string
	flushThreshold   int
	compactThreshold int

	mu             sync.RWMutex
	mem            map[string][]byte
	memBytes       int
	sstables       []string // 文件名，最旧的在前
	nextNum        int
	wal            *os.File
	compactedFiles int // 历史被 compaction 删除的 SSTable 数（观测用）
}

// Open 打开（或创建）位于 dir 的数据库，并回放残留 WAL。
func Open(dir string, opts Options) (*DB, error) {
	if opts.FlushThresholdBytes <= 0 {
		opts.FlushThresholdBytes = 1 << 20
	}
	if opts.CompactThreshold <= 0 {
		opts.CompactThreshold = 4
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	db := &DB{
		dir:              dir,
		flushThreshold:   opts.FlushThresholdBytes,
		compactThreshold: opts.CompactThreshold,
		mem:              make(map[string][]byte),
		nextNum:          1,
	}

	files, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, fi := range files {
		name := fi.Name()
		if strings.HasPrefix(name, "sst-") && strings.HasSuffix(name, ".sst") {
			db.sstables = append(db.sstables, name)
			var num int
			if _, err := fmt.Sscanf(name, "sst-%06d.sst", &num); err == nil && num >= db.nextNum {
				db.nextNum = num + 1
			}
		}
	}
	sort.Strings(db.sstables)

	// 回放 WAL（上次未 flush 的写入）。
	walPath := filepath.Join(dir, "wal.log")
	if _, err := os.Stat(walPath); err == nil {
		if err := db.replayWAL(walPath); err != nil {
			return nil, err
		}
	}
	wal, err := os.OpenFile(walPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	db.wal = wal
	return db, nil
}

func (db *DB) replayWAL(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := io.NewSectionReader(f, 0, 1<<62)
	br := newByteReader(r)
	for {
		klen, err := br.uvarint()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("replay wal: %v", err)
		}
		key, err := br.bytes(int(klen))
		if err != nil {
			return fmt.Errorf("replay wal: %v", err)
		}
		vlen, err := br.uvarint()
		if err != nil {
			return fmt.Errorf("replay wal: %v", err)
		}
		val, err := br.bytes(int(vlen))
		if err != nil {
			return fmt.Errorf("replay wal: %v", err)
		}
		db.mem[string(key)] = val
		db.memBytes += len(key) + len(val)
	}
}

// byteReader 让 SectionReader 满足 binary.ByteReader。
type byteReaderImpl struct{ r *io.SectionReader }

func newByteReader(r *io.SectionReader) *byteReaderImpl { return &byteReaderImpl{r: r} }

func (b *byteReaderImpl) uvarint() (uint64, error) {
	var x uint64
	var s uint
	for i := 0; ; i++ {
		var one [1]byte
		if _, err := b.r.Read(one[:]); err != nil {
			return 0, err
		}
		c := one[0]
		if c < 0x80 {
			if i > 9 || i == 9 && c > 1 {
				return 0, fmt.Errorf("uvarint overflow")
			}
			return x | uint64(c)<<s, nil
		}
		x |= uint64(c&0x7f) << s
		s += 7
	}
}

func (b *byteReaderImpl) bytes(n int) ([]byte, error) {
	buf := make([]byte, n)
	_, err := io.ReadFull(b.r, buf)
	return buf, err
}

// Put 写入一条键值。先落 WAL 再进 memtable，超阈值自动 flush。
func (db *DB) Put(key, value []byte) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	var rec [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(rec[:], uint64(len(key)))
	if _, err := db.wal.Write(rec[:n]); err != nil {
		return err
	}
	if _, err := db.wal.Write(key); err != nil {
		return err
	}
	n = binary.PutUvarint(rec[:], uint64(len(value)))
	if _, err := db.wal.Write(rec[:n]); err != nil {
		return err
	}
	if _, err := db.wal.Write(value); err != nil {
		return err
	}

	k := string(key)
	if old, ok := db.mem[k]; ok {
		db.memBytes -= len(old)
	} else {
		db.memBytes += len(key)
	}
	db.mem[k] = append([]byte(nil), value...)
	db.memBytes += len(value)

	if db.memBytes >= db.flushThreshold {
		if err := db.flushLocked(); err != nil {
			return err
		}
		return db.maybeCompactLocked()
	}
	return nil
}

// flushLocked 把 memtable 落盘为新的不可变 SSTable，并截断 WAL。
func (db *DB) flushLocked() error {
	if len(db.mem) == 0 {
		return nil
	}
	keys := make([]string, 0, len(db.mem))
	for k := range db.mem {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	entries := make([]Entry, 0, len(keys))
	for _, k := range keys {
		entries = append(entries, Entry{Key: []byte(k), Value: db.mem[k]})
	}

	name := fmt.Sprintf("sst-%06d.sst", db.nextNum)
	db.nextNum++
	if err := WriteSSTable(filepath.Join(db.dir, name), entries); err != nil {
		return err
	}
	db.sstables = append(db.sstables, name)

	// SSTable 已持久化，重置 memtable 与 WAL。
	db.mem = make(map[string][]byte)
	db.memBytes = 0
	if err := db.wal.Close(); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(db.dir, "wal.log")); err != nil {
		return err
	}
	wal, err := os.OpenFile(filepath.Join(db.dir, "wal.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	db.wal = wal
	return nil
}

// Compact 强制执行一次全量合并（供运维或测试手动触发）。
func (db *DB) Compact() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.compactLocked()
}

// maybeCompactLocked 在 SSTable 过多时触发合并。
func (db *DB) maybeCompactLocked() error {
	if len(db.sstables) < db.compactThreshold {
		return nil
	}
	return db.compactLocked()
}

// compactLocked 合并全部 SSTable 为一个，
// 随后删除被合并的旧文件——这正是快照必须能抵御的破坏性操作。
func (db *DB) compactLocked() error {
	if len(db.sstables) < 2 {
		return nil
	}
	merged := make(map[string][]byte)
	for _, name := range db.sstables { // 旧→新，新文件覆盖旧 key
		entries, err := ReadSSTable(filepath.Join(db.dir, name))
		if err != nil {
			return err
		}
		for _, e := range entries {
			merged[string(e.Key)] = e.Value
		}
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	entries := make([]Entry, 0, len(keys))
	for _, k := range keys {
		entries = append(entries, Entry{Key: []byte(k), Value: merged[k]})
	}

	name := fmt.Sprintf("sst-%06d.sst", db.nextNum)
	db.nextNum++
	if err := WriteSSTable(filepath.Join(db.dir, name), entries); err != nil {
		return err
	}
	old := db.sstables
	db.sstables = []string{name}
	for _, oldName := range old {
		if err := os.Remove(filepath.Join(db.dir, oldName)); err != nil {
			return err
		}
		db.compactedFiles++
	}
	return nil
}

// Get 读取 key 的最新值。
func (db *DB) Get(key []byte) ([]byte, bool, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if v, ok := db.mem[string(key)]; ok {
		return append([]byte(nil), v...), true, nil
	}
	for i := len(db.sstables) - 1; i >= 0; i-- { // 新→旧
		entries, err := ReadSSTable(filepath.Join(db.dir, db.sstables[i]))
		if err != nil {
			return nil, false, err
		}
		for _, e := range entries {
			if string(e.Key) == string(key) {
				return e.Value, true, nil
			}
		}
	}
	return nil, false, nil
}

// All 返回当前全部可见键值（合并 memtable 与所有 SSTable）。
func (db *DB) All() (map[string][]byte, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	out := make(map[string][]byte)
	for _, name := range db.sstables {
		entries, err := ReadSSTable(filepath.Join(db.dir, name))
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			out[string(e.Key)] = e.Value
		}
	}
	for k, v := range db.mem {
		out[k] = v
	}
	return out, nil
}

// ViewSSTables 在读锁保护下把当前 SSTable 绝对路径列表交给 fn。
// 持锁期间 compaction 无法删除任何文件，因此 fn 可以安全地
// 对不可变 SSTable 建立硬链接，得到一致性快照。
func (db *DB) ViewSSTables(fn func(files []string) error) error {
	db.mu.RLock()
	defer db.mu.RUnlock()
	files := make([]string, len(db.sstables))
	for i, name := range db.sstables {
		files[i] = filepath.Join(db.dir, name)
	}
	return fn(files)
}

// Stats 返回观测指标：当前 SSTable 数与历史被 compaction 删除的文件数。
func (db *DB) Stats() (sstableCount, compactedFiles int) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return len(db.sstables), db.compactedFiles
}

// Close 关闭 WAL。
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.wal != nil {
		err := db.wal.Close()
		db.wal = nil
		return err
	}
	return nil
}
