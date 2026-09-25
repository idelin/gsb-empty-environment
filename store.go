// Package slotstore 实现定长槽位记录存储：
// 句柄带代际号，删除后槽位可复用但旧句柄失效；
// 自由链与数据槽双向校验，恢复时识别并回收半写槽。
package slotstore

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sync"
)

const (
	DefaultSlotSize  = 512
	DefaultSlotCount = 1024

	dataFileName   = "slots.dat"
	headerFileName = "header.bin"

	headerSize    = 28
	headerMagic   = 0x31524448 // "HDR1"
	headerVersion = 1
)

var (
	ErrStaleHandle     = errors.New("slotstore: stale handle (slot deleted or reused by a newer generation)")
	ErrInvalidHandle   = errors.New("slotstore: handle slot index out of range")
	ErrCorruptSlot     = errors.New("slotstore: slot tail checksum mismatch, not a valid record")
	ErrFull            = errors.New("slotstore: no free slots")
	ErrTooLarge        = errors.New("slotstore: payload exceeds slot capacity")
	ErrFreeListCycle   = errors.New("slotstore: free list contains a cycle")
	ErrFreeListCorrupt = errors.New("slotstore: free list inconsistent with data slots")
	ErrClosed          = errors.New("slotstore: store is closed")
)

// Handle 是记录句柄：槽号 + 代际号。槽被删除并复用后代际号增加，旧句柄失效。
type Handle struct {
	Slot uint32
	Gen  uint32
}

func (h Handle) String() string { return fmt.Sprintf("{slot:%d gen:%d}", h.Slot, h.Gen) }

// FaultOp 标识故障注入点。
type FaultOp string

const (
	FaultSlotWrite   FaultOp = "slot.write"   // 写数据槽之前
	FaultSlotSync    FaultOp = "slot.sync"    // fsync 数据文件之前
	FaultHeaderWrite FaultOp = "header.write" // 写头文件之前
	FaultHeaderSync  FaultOp = "header.sync"  // fsync 头文件之前
)

// FaultHook 是故障注入钩子，在每次底层写/同步前被调用。
// 返回修改后的 buf 可模拟撕裂写（如截断缓冲区）；返回 error 模拟在该点崩溃。
// 传入 nil 表示不注入。
type FaultHook func(op FaultOp, buf []byte) ([]byte, error)

type Options struct {
	SlotSize  int       // 单槽字节数，默认 512（仅建库时生效）
	SlotCount uint32    // 槽总数，默认 1024（仅建库时生效）
	Hook      FaultHook // 故障注入点
}

type Store struct {
	mu        sync.Mutex
	dir       string
	data      *os.File
	slotSize  int
	slotCount uint32
	freeHead  uint32
	freeCount uint32
	hook      FaultHook
	closed    bool
}

type diskHeader struct {
	slotSize  uint32
	slotCount uint32
	freeHead  uint32
	freeCount uint32
}

func encodeDiskHeader(h diskHeader) []byte {
	buf := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(buf[0:4], headerMagic)
	binary.LittleEndian.PutUint32(buf[4:8], headerVersion)
	binary.LittleEndian.PutUint32(buf[8:12], h.slotSize)
	binary.LittleEndian.PutUint32(buf[12:16], h.slotCount)
	binary.LittleEndian.PutUint32(buf[16:20], h.freeHead)
	binary.LittleEndian.PutUint32(buf[20:24], h.freeCount)
	binary.LittleEndian.PutUint32(buf[24:28], crc32.ChecksumIEEE(buf[:24]))
	return buf
}

func decodeDiskHeader(buf []byte) (diskHeader, error) {
	var h diskHeader
	if len(buf) != headerSize {
		return h, errors.New("slotstore: bad header size")
	}
	if binary.LittleEndian.Uint32(buf[0:4]) != headerMagic ||
		binary.LittleEndian.Uint32(buf[4:8]) != headerVersion {
		return h, errors.New("slotstore: bad header magic/version")
	}
	if binary.LittleEndian.Uint32(buf[24:28]) != crc32.ChecksumIEEE(buf[:24]) {
		return h, errors.New("slotstore: header checksum mismatch")
	}
	h.slotSize = binary.LittleEndian.Uint32(buf[8:12])
	h.slotCount = binary.LittleEndian.Uint32(buf[12:16])
	h.freeHead = binary.LittleEndian.Uint32(buf[16:20])
	h.freeCount = binary.LittleEndian.Uint32(buf[20:24])
	if h.slotSize < slotHdrSize+slotCRCSize+4 || h.slotCount == 0 {
		return h, errors.New("slotstore: header geometry invalid")
	}
	return h, nil
}

// Open 打开（必要时创建）dir 下的存储，并执行崩溃恢复。
func Open(dir string, opts Options) (*Store, error) {
	if opts.SlotSize == 0 {
		opts.SlotSize = DefaultSlotSize
	}
	if opts.SlotCount == 0 {
		opts.SlotCount = DefaultSlotCount
	}
	if opts.SlotSize < slotHdrSize+slotCRCSize+4 {
		return nil, fmt.Errorf("slotstore: slot size %d too small", opts.SlotSize)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	st := &Store{dir: dir, hook: opts.Hook}
	dataPath := filepath.Join(dir, dataFileName)

	_, statErr := os.Stat(dataPath)
	if os.IsNotExist(statErr) {
		if err := st.create(dataPath, opts); err != nil {
			return nil, err
		}
		return st, nil
	}

	hdr, hdrValid := st.readHeader()
	if !hdrValid {
		return nil, errors.New("slotstore: header missing or corrupt; cannot determine slot geometry")
	}
	st.slotSize = int(hdr.slotSize)
	st.slotCount = hdr.slotCount

	f, err := os.OpenFile(dataPath, os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	st.data = f
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if fi.Size() != int64(st.slotSize)*int64(st.slotCount) {
		f.Close()
		return nil, fmt.Errorf("slotstore: data file size %d does not match header geometry", fi.Size())
	}
	if err := st.recover(hdr, hdrValid); err != nil {
		f.Close()
		return nil, err
	}
	return st, nil
}

// create 初始化空库：全部槽写为 free 并串成自由链。
func (st *Store) create(dataPath string, opts Options) error {
	st.slotSize = opts.SlotSize
	st.slotCount = opts.SlotCount
	f, err := os.OpenFile(dataPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	st.data = f
	buf := make([]byte, st.slotSize)
	for i := uint32(0); i < st.slotCount; i++ {
		next := uint32(i + 1)
		if i == st.slotCount-1 {
			next = nilLink
		}
		encodeSlot(buf, slot{state: stateFree, nextFree: next})
		if _, err := f.WriteAt(buf, int64(i)*int64(st.slotSize)); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	st.freeHead = 0
	st.freeCount = st.slotCount
	return st.writeHeader()
}

func (st *Store) readHeader() (diskHeader, bool) {
	buf, err := os.ReadFile(filepath.Join(st.dir, headerFileName))
	if err != nil {
		return diskHeader{}, false
	}
	h, err := decodeDiskHeader(buf)
	if err != nil {
		return diskHeader{}, false
	}
	return h, true
}

// writeHeader 通过 临时文件+rename 原子替换头文件，保证头文件要么是旧版要么是新版。
func (st *Store) writeHeader() error {
	buf, err := st.runHook(FaultHeaderWrite, encodeDiskHeader(diskHeader{
		slotSize:  uint32(st.slotSize),
		slotCount: st.slotCount,
		freeHead:  st.freeHead,
		freeCount: st.freeCount,
	}))
	if err != nil {
		return err
	}
	tmp := filepath.Join(st.dir, headerFileName+".tmp")
	if err := os.WriteFile(tmp, buf, 0o644); err != nil {
		return err
	}
	if _, err := st.runHook(FaultHeaderSync, nil); err != nil {
		return err
	}
	if tf, err := os.OpenFile(tmp, os.O_RDWR, 0); err == nil {
		tf.Sync()
		tf.Close()
	}
	if err := os.Rename(tmp, filepath.Join(st.dir, headerFileName)); err != nil {
		return err
	}
	if d, err := os.Open(st.dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

func (st *Store) runHook(op FaultOp, buf []byte) ([]byte, error) {
	if st.hook == nil {
		return buf, nil
	}
	return st.hook(op, buf)
}

// writeSlotLocked 编码并落盘一个槽，随后 fsync（槽级提交点）。
func (st *Store) writeSlotLocked(idx uint32, s slot) error {
	buf := make([]byte, st.slotSize)
	encodeSlot(buf, s)
	buf, err := st.runHook(FaultSlotWrite, buf)
	if err != nil {
		return err
	}
	if _, err := st.data.WriteAt(buf, int64(idx)*int64(st.slotSize)); err != nil {
		return err
	}
	if _, err := st.runHook(FaultSlotSync, nil); err != nil {
		return err
	}
	return st.data.Sync()
}

func (st *Store) readSlotLocked(idx uint32) (slot, error) {
	if idx >= st.slotCount {
		return slot{}, ErrInvalidHandle
	}
	buf := make([]byte, st.slotSize)
	if _, err := st.data.ReadAt(buf, int64(idx)*int64(st.slotSize)); err != nil {
		return slot{}, err
	}
	s, err := decodeSlot(buf)
	if err != nil {
		return slot{}, ErrCorruptSlot
	}
	return s, nil
}

// recover 崩溃恢复：扫描全部数据槽（以槽为权威），
// 半写槽（槽尾校验失败）回收为空闲；再对持久化自由链做双向校验。
func (st *Store) recover(hdr diskHeader, hdrValid bool) error {
	n := st.slotCount
	isFree := make([]bool, n)
	broken := make([]bool, n)
	next := make([]uint32, n)
	rawGen := make([]uint32, n)

	buf := make([]byte, st.slotSize)
	for i := uint32(0); i < n; i++ {
		if _, err := st.data.ReadAt(buf, int64(i)*int64(st.slotSize)); err != nil {
			return err
		}
		rawGen[i] = binary.LittleEndian.Uint32(buf[4:8])
		s, err := decodeSlot(buf)
		if err != nil {
			// 半写槽：识别为损坏，回收为空闲
			isFree[i], broken[i] = true, true
			continue
		}
		if s.state == stateFree {
			isFree[i], next[i] = true, s.nextFree
		}
	}

	// 双向校验之一：持久化自由链 -> 数据槽。
	// 链上节点必须确实指向空闲槽，且不得成环。
	if hdrValid {
		seen := make([]bool, n)
		for cur := hdr.freeHead; cur != nilLink; {
			if cur >= n {
				return ErrFreeListCorrupt
			}
			if seen[cur] {
				return ErrFreeListCycle
			}
			seen[cur] = true
			if !isFree[cur] {
				return ErrFreeListCorrupt
			}
			if broken[cur] {
				break // 半写槽的 next 不可信，无法继续沿链校验
			}
			cur = next[cur]
		}
	}

	// 双向校验之二：以数据槽扫描结果为权威重建自由链，
	// 保证每个空闲槽都在链上（崩溃丢失的链节点被找回，杜绝泄漏）。
	st.freeHead = nilLink
	st.freeCount = 0
	for i := n; i > 0; i-- {
		idx := i - 1
		if !isFree[idx] {
			continue
		}
		gen := rawGen[idx]
		if broken[idx] {
			gen++ // 半写槽代际不可信，抬高一代防止旧句柄碰巧命中
		}
		next[idx] = st.freeHead
		st.freeHead = idx
		st.freeCount++
		if err := st.writeSlotLocked(idx, slot{generation: gen, state: stateFree, nextFree: next[idx]}); err != nil {
			return err
		}
	}
	return st.writeHeader()
}

// Put 写入一条记录，返回其句柄。
// 写序：先原子更新头文件（弹出自由链头），再写数据槽并 fsync。
// 崩溃若发生在两步之间，恢复扫描会把该槽重新找回自由链，不会泄漏。
func (st *Store) Put(payload []byte) (Handle, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return Handle{}, ErrClosed
	}
	if len(payload) > st.Capacity() {
		return Handle{}, ErrTooLarge
	}
	if st.freeCount == 0 {
		return Handle{}, ErrFull
	}
	head, err := st.readSlotLocked(st.freeHead)
	if err != nil {
		return Handle{}, ErrFreeListCorrupt
	}
	if head.state != stateFree {
		return Handle{}, ErrFreeListCorrupt
	}
	idx := st.freeHead
	newGen := head.generation + 1 // 同槽新对象代际增加

	savedHead, savedCount := st.freeHead, st.freeCount
	st.freeHead, st.freeCount = head.nextFree, st.freeCount-1
	if err := st.writeHeader(); err != nil {
		st.freeHead, st.freeCount = savedHead, savedCount
		return Handle{}, err
	}
	if err := st.writeSlotLocked(idx, slot{generation: newGen, state: stateUsed, payload: payload}); err != nil {
		return Handle{}, err
	}
	return Handle{Slot: idx, Gen: newGen}, nil
}

// Get 按句柄读取记录。代际不符、槽已删除、槽尾校验失败都会报错。
func (st *Store) Get(h Handle) ([]byte, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return nil, ErrClosed
	}
	s, err := st.readSlotLocked(h.Slot)
	if err != nil {
		return nil, err
	}
	if s.state != stateUsed || s.generation != h.Gen {
		return nil, ErrStaleHandle
	}
	return append([]byte(nil), s.payload...), nil
}

// Delete 删除句柄指向的记录，槽位进入自由链等待复用。
// 写序：先写空闲槽并 fsync，再原子更新头文件（压入自由链头）。
func (st *Store) Delete(h Handle) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return ErrClosed
	}
	s, err := st.readSlotLocked(h.Slot)
	if err != nil {
		return err
	}
	if s.state != stateUsed || s.generation != h.Gen {
		return ErrStaleHandle
	}
	if err := st.writeSlotLocked(h.Slot, slot{
		generation: s.generation,
		state:      stateFree,
		nextFree:   st.freeHead,
	}); err != nil {
		return err
	}
	st.freeHead, st.freeCount = h.Slot, st.freeCount+1
	return st.writeHeader()
}

// Capacity 返回单槽可承载的最大 payload 字节数。
func (st *Store) Capacity() int { return st.slotSize - slotHdrSize - slotCRCSize }

// FreeCount 返回当前空闲槽数量。
func (st *Store) FreeCount() uint32 {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.freeCount
}

// Close 刷盘并关闭存储。
func (st *Store) Close() error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return nil
	}
	st.closed = true
	if err := st.data.Sync(); err != nil {
		st.data.Close()
		return err
	}
	return st.data.Close()
}
