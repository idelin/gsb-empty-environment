// Package kvstore 是一个仅依赖标准库的日志结构（LSM 风格）键值存储。
//
// 设计要点：
//   - 所有写（含删除墓碑）只追加到活跃段文件，永不原地修改；
//   - 启动时按 MANIFEST 顺序扫描各段，重建内存索引（key -> 记录位置）；
//   - 活跃段超过阈值后轮转，不可变段由 Compact 合并，仅保留可见记录；
//   - 压缩通过 MANIFEST.tmp + 原子 rename 切换，前后崩溃均可恢复；
//   - 读者 pin 段句柄，引用归零前旧段文件不会被回收。
package kvstore

import (
	"bytes"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var (
	ErrNotFound = errors.New("kvstore: key not found")
	ErrClosed   = errors.New("kvstore: store is closed")
)

// Options 打开存储时的配置。
type Options struct {
	// MaxSegmentBytes 活跃段超过该字节数后轮转；<=0 时用默认值 16MiB。
	MaxSegmentBytes int64
	// SyncWrites 为 true 时，每次 Put/Delete 在 fsync 后才返回（见 README 断电保证边界）。
	SyncWrites bool
	// Faults 故障注入点，仅测试使用。
	Faults *Faults
}

// location 索引条目：记录在哪个段的哪个偏移、占多少字节。
type location struct {
	segID  uint64
	offset int64
	length int64
}

// Store 是并发安全的键值存储。所有文件都位于 Open 指定的目录中。
type Store struct {
	dir    string
	opts   Options
	faults *Faults

	mu       sync.RWMutex
	index    map[string]location
	segments map[uint64]*segment // 全部存活段（含活跃段）
	order    []uint64            // 段 id 按新旧排序，最后一个为活跃段
	nextID   uint64
	closed   bool

	compactMu sync.Mutex // 同一时刻只允许一个压缩
}

// Open 打开（必要时创建）dir 下的存储，并扫描各段重建索引。
func Open(dir string, opts *Options) (*Store, error) {
	o := Options{}
	if opts != nil {
		o = *opts
	}
	if o.MaxSegmentBytes <= 0 {
		o.MaxSegmentBytes = 16 << 20
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{
		dir:      dir,
		opts:     o,
		index:    make(map[string]location),
		segments: make(map[uint64]*segment),
		nextID:   1,
	}
	s.faults = o.Faults

	// 压缩/轮转在 rename 前崩溃会留下 MANIFEST.tmp，直接丢弃。
	os.Remove(filepath.Join(s.dir, "MANIFEST.tmp"))

	ids, hasManifest, err := loadManifest(s.dir)
	if err != nil {
		return nil, err
	}
	onDisk, err := listSegmentIDs(s.dir)
	if err != nil {
		return nil, err
	}
	if hasManifest {
		// rename 之后崩溃：旧段已不在清单中，是孤儿文件，清理掉。
		inManifest := make(map[uint64]bool, len(ids))
		for _, id := range ids {
			inManifest[id] = true
		}
		for _, id := range onDisk {
			if !inManifest[id] {
				os.Remove(segmentPath(s.dir, id))
			}
		}
	} else {
		// 无清单（全新目录）：以目录中的段文件为准。
		ids = onDisk
	}

	for i, id := range ids {
		writable := i == len(ids)-1 // 只有最后一段是活跃段，可写
		seg, err := openSegment(s.dir, id, writable)
		if err != nil {
			return nil, err
		}
		valid, err := s.scanSegment(seg)
		if err != nil {
			return nil, err
		}
		fi, err := seg.file.Stat()
		if err != nil {
			return nil, err
		}
		if valid < fi.Size() {
			// 尾部存在撕裂记录（断电/崩溃截断）。只有活跃段允许出现，
			// 截断到最后的有效边界；不可变段损坏则拒绝打开。
			if !writable {
				return nil, fmt.Errorf("kvstore: immutable segment %d is corrupt at offset %d", id, valid)
			}
			if err := seg.file.Truncate(valid); err != nil {
				return nil, err
			}
		}
		seg.size = valid
		s.segments[id] = seg
		s.order = append(s.order, id)
		if id >= s.nextID {
			s.nextID = id + 1
		}
	}

	if len(s.order) == 0 {
		seg, err := createSegment(s.dir, s.nextID)
		if err != nil {
			return nil, err
		}
		s.nextID++
		s.segments[seg.id] = seg
		s.order = append(s.order, seg.id)
		if err := s.writeManifestLocked(s.order); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// scanSegment 顺序扫描段文件，把记录重放到内存索引，返回有效字节数。
// 遇到长度/CRC 校验失败的尾部即停止——那是崩溃留下的撕裂写。
func (s *Store) scanSegment(seg *segment) (int64, error) {
	fi, err := seg.file.Stat()
	if err != nil {
		return 0, err
	}
	size := fi.Size()
	off := int64(0)
	hdr := make([]byte, headerSize)
	for off < size {
		if size-off < headerSize {
			return off, nil // 撕裂的头部
		}
		if _, err := seg.file.ReadAt(hdr, off); err != nil {
			return 0, err
		}
		klen := int64(bytesToUint32(hdr[4:8]))
		vlen := int64(bytesToUint32(hdr[8:12]))
		if klen > maxKeySize || vlen > maxValSize {
			return off, nil // 非法长度：视为撕裂尾
		}
		total := int64(headerSize) + klen + vlen
		if size-off < total {
			return off, nil // 撕裂的记录体
		}
		buf, err := seg.readRecordAt(off, total)
		if err != nil {
			return 0, err
		}
		key, _, tombstone, err := decodeRecord(buf)
		if err != nil {
			return off, nil // CRC 不符：撕裂尾
		}
		if tombstone {
			delete(s.index, string(key))
		} else {
			s.index[string(key)] = location{segID: seg.id, offset: off, length: total}
		}
		off += total
	}
	return off, nil
}

// Put 追加一条键值记录；同键后写覆盖先写。
func (s *Store) Put(key, value []byte) error {
	if len(key) == 0 {
		return errors.New("kvstore: empty key")
	}
	if len(key) > maxKeySize || len(value) > maxValSize {
		return errors.New("kvstore: key or value too large")
	}
	rec := encodeRecord(key, value, false)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	off, err := s.appendLocked(rec)
	if err != nil {
		return err
	}
	s.index[string(key)] = location{segID: s.activeLocked().id, offset: off, length: int64(len(rec))}
	return nil
}

// Delete 追加一条墓碑记录；键不存在时直接返回。
func (s *Store) Delete(key []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if _, ok := s.index[string(key)]; !ok {
		return nil
	}
	rec := encodeRecord(key, nil, true)
	if _, err := s.appendLocked(rec); err != nil {
		return err
	}
	delete(s.index, string(key))
	return nil
}

// Get 读取键的当前值。读取期间 pin 住所在段，保证段文件不会被压缩回收。
func (s *Store) Get(key []byte) ([]byte, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, ErrClosed
	}
	loc, ok := s.index[string(key)]
	if !ok {
		s.mu.RUnlock()
		return nil, ErrNotFound
	}
	seg := s.segments[loc.segID]
	seg.pin()
	s.mu.RUnlock()
	defer seg.unpin()

	buf, err := seg.readRecordAt(loc.offset, loc.length)
	if err != nil {
		return nil, err
	}
	_, value, tombstone, err := decodeRecord(buf)
	if err != nil {
		return nil, err
	}
	if tombstone {
		return nil, ErrNotFound // 索引不含墓碑，此处仅为防御
	}
	return value, nil
}

// appendLocked 把记录追加到活跃段（必要时先轮转），返回写入偏移。
func (s *Store) appendLocked(rec []byte) (int64, error) {
	act := s.activeLocked()
	if act.size > 0 && act.size+int64(len(rec)) > s.opts.MaxSegmentBytes {
		if err := s.rotateLocked(); err != nil {
			return 0, err
		}
		act = s.activeLocked()
	}
	off := act.size
	if _, err := act.file.WriteAt(rec, off); err != nil {
		return 0, err
	}
	act.size += int64(len(rec))
	s.fire(func(f *Faults) func() { return f.AfterRecordAppend })
	if s.opts.SyncWrites {
		if err := act.file.Sync(); err != nil {
			return 0, err
		}
	}
	return off, nil
}

// rotateLocked 关闭当前活跃段的追加，新建一个活跃段并持久化清单。
func (s *Store) rotateLocked() error {
	seg, err := createSegment(s.dir, s.nextID)
	if err != nil {
		return err
	}
	s.nextID++
	s.segments[seg.id] = seg
	s.order = append(s.order, seg.id)
	return s.writeManifestLocked(s.order)
}

// writeManifestLocked 把段清单写入 MANIFEST.tmp、fsync 后原子 rename，
// 再 fsync 目录。rename 前后各有一个故障注入点。
func (s *Store) writeManifestLocked(order []uint64) error {
	tmp := filepath.Join(s.dir, "MANIFEST.tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(manifestBytes(order)); err != nil {
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
	s.fire(func(f *Faults) func() { return f.BeforeManifestRename })
	if err := os.Rename(tmp, filepath.Join(s.dir, "MANIFEST")); err != nil {
		return err
	}
	s.fire(func(f *Faults) func() { return f.AfterManifestRename })
	return syncDir(s.dir)
}

// Close 关闭全部段文件。调用方需保证没有进行中的读写。
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var firstErr error
	for _, seg := range s.segments {
		if err := seg.file.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// SegmentCount 返回当前存活的段文件数（含活跃段），主要用于演示与测试。
func (s *Store) SegmentCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.order)
}

// Len 返回当前可见键的数量。
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.index)
}

func (s *Store) activeLocked() *segment {
	return s.segments[s.order[len(s.order)-1]]
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// --- MANIFEST 编解码 ---
// 格式：
//   v1\n
//   <段id十进制>\n   （按新旧顺序，每行一个）
//   crc:<此前全部字节的 crc32，8 位十六进制>\n

func manifestBytes(ids []uint64) []byte {
	var buf bytes.Buffer
	buf.WriteString("v1\n")
	for _, id := range ids {
		fmt.Fprintf(&buf, "%d\n", id)
	}
	fmt.Fprintf(&buf, "crc:%08x\n", crc32.ChecksumIEEE(buf.Bytes()))
	return buf.Bytes()
}

func parseManifest(data []byte) ([]uint64, error) {
	idx := bytes.LastIndex(data, []byte("crc:"))
	if idx <= 0 || data[idx-1] != '\n' {
		return nil, errors.New("kvstore: manifest missing crc line")
	}
	var want uint32
	if _, err := fmt.Sscanf(string(data[idx:]), "crc:%08x", &want); err != nil {
		return nil, fmt.Errorf("kvstore: bad manifest crc line: %w", err)
	}
	if crc32.ChecksumIEEE(data[:idx]) != want {
		return nil, errors.New("kvstore: manifest crc mismatch")
	}
	lines := strings.Split(strings.TrimRight(string(data[:idx]), "\n"), "\n")
	if len(lines) == 0 || lines[0] != "v1" {
		return nil, errors.New("kvstore: bad manifest header")
	}
	ids := make([]uint64, 0, len(lines)-1)
	for _, line := range lines[1:] {
		id, err := strconv.ParseUint(line, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("kvstore: bad manifest line %q", line)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func loadManifest(dir string) (ids []uint64, ok bool, err error) {
	data, err := os.ReadFile(filepath.Join(dir, "MANIFEST"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	ids, err = parseManifest(data)
	if err != nil {
		return nil, false, err
	}
	return ids, true, nil
}

func listSegmentIDs(dir string) ([]uint64, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var ids []uint64
	for _, e := range ents {
		name := e.Name()
		if !strings.HasSuffix(name, ".seg") {
			continue
		}
		id, err := strconv.ParseUint(strings.TrimSuffix(name, ".seg"), 10, 64)
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func bytesToUint32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}
