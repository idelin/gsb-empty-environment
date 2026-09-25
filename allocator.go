// Package pagealloc 管理固定大小页的申请与释放。
//
// 特性：
//   - 连续区间申请（first-fit），相邻空闲区间自动合并；
//   - 事务语义：Begin/Alloc/Free/Commit/Rollback，回滚丢弃全部改动；
//   - 双重释放与越界释放被拒绝；
//   - 提交时通过 临时文件 + fsync + 原子 rename + 目录 fsync 持久化，
//     崩溃后从调用方指定的目录恢复，已提交状态不丢、不撕裂；
//   - 故障通过 FaultInjector 在提交路径的关键点注入，用于模拟崩溃。
package pagealloc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
)

const (
	magic     uint32 = 0x50414731 // "PAG1"
	stateName        = "pages.state"
	tmpName          = "pages.state.tmp"
)

var (
	// ErrNoSpace 表示没有足够大的连续空闲区间。
	ErrNoSpace = errors.New("pagealloc: not enough contiguous free pages")
	// ErrOutOfRange 表示页区间越界。
	ErrOutOfRange = errors.New("pagealloc: page range out of bounds")
	// ErrDoubleFree 表示释放的区间与空闲页重叠（双重释放或释放未占用的页）。
	ErrDoubleFree = errors.New("pagealloc: range is not allocated (double free)")
	// ErrTxDone 表示事务已结束。
	ErrTxDone = errors.New("pagealloc: transaction already finished")
	// ErrTxBusy 表示已有未结束的事务。
	ErrTxBusy = errors.New("pagealloc: another transaction is open")
	// ErrCrash 是故障注入模拟的崩溃错误。
	ErrCrash = errors.New("pagealloc: simulated crash (fault injected)")
)

// FaultPoint 标识提交路径上可注入故障的位置。
type FaultPoint string

const (
	FaultBeforeWrite    FaultPoint = "before_write"     // 写临时文件之前
	FaultBeforeFileSync FaultPoint = "before_file_sync" // 临时文件 fsync 之前
	FaultBeforeRename   FaultPoint = "before_rename"    // 原子 rename 之前
	FaultBeforeDirSync  FaultPoint = "before_dir_sync"  // 目录 fsync 之前
)

// FaultInjector 在指定注入点制造一次 ErrCrash。
type FaultInjector struct {
	armed map[FaultPoint]bool
}

// NewFaultInjector 返回空的注入器。
func NewFaultInjector() *FaultInjector {
	return &FaultInjector{armed: make(map[FaultPoint]bool)}
}

// Arm 武装一个注入点，触发一次后自动解除。
func (f *FaultInjector) Arm(p FaultPoint) { f.armed[p] = true }

func (f *FaultInjector) check(p FaultPoint) error {
	if f != nil && f.armed[p] {
		delete(f.armed, p)
		return ErrCrash
	}
	return nil
}

// interval 表示半开区间 [start, end)。
type interval struct {
	start, end int
}

// Allocator 管理 total 个固定大小的页，状态持久化在 dir 目录下。
type Allocator struct {
	dir      string
	total    int
	free     []interval // 已提交的空闲区间，有序且不相邻
	injector *FaultInjector
	txOpen   bool
}

// Open 打开（或创建）位于 dir 的分配器。
// 若 dir 中已存在已提交的状态文件，则恢复该状态（忽略 totalPages）；
// 否则以 totalPages 个全部空闲的页初始化并立即落盘。崩溃残留的临时文件会被清理。
func Open(dir string, totalPages int) (*Allocator, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	// 上次提交若在 rename 前崩溃，会留下临时文件，直接丢弃。
	if err := os.Remove(filepath.Join(dir, tmpName)); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	a := &Allocator{dir: dir, injector: NewFaultInjector()}
	data, err := os.ReadFile(filepath.Join(dir, stateName))
	switch {
	case os.IsNotExist(err):
		if totalPages <= 0 {
			return nil, fmt.Errorf("pagealloc: totalPages must be > 0 for a new allocator")
		}
		a.total = totalPages
		a.free = []interval{{0, totalPages}}
		// 创建即落盘，保证此后任何时刻目录里都有可恢复的状态。
		if err := a.persist(a.free); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		total, free, err := decode(data)
		if err != nil {
			return nil, err
		}
		a.total, a.free = total, free
	}
	return a, nil
}

// Injector 返回故障注入器，测试可用它武装注入点。
func (a *Allocator) Injector() *FaultInjector { return a.injector }

// Total 返回总页数。
func (a *Allocator) Total() int { return a.total }

// FreeCount 返回已提交状态中的空闲页数。
func (a *Allocator) FreeCount() int {
	n := 0
	for _, iv := range a.free {
		n += iv.end - iv.start
	}
	return n
}

// AllocatedCount 返回已提交状态中的已占用页数。
func (a *Allocator) AllocatedCount() int { return a.total - a.FreeCount() }

// FreeRanges 返回已提交空闲区间的副本，形如 [start, end)。
func (a *Allocator) FreeRanges() [][2]int {
	out := make([][2]int, len(a.free))
	for i, iv := range a.free {
		out[i] = [2]int{iv.start, iv.end}
	}
	return out
}

// Begin 开启一个事务。事务在已提交状态的快照上工作。
func (a *Allocator) Begin() (*Tx, error) {
	if a.txOpen {
		return nil, ErrTxBusy
	}
	a.txOpen = true
	free := make([]interval, len(a.free))
	copy(free, a.free)
	return &Tx{a: a, free: free}, nil
}

// Tx 是一个进行中的事务，提交或回滚后失效。
type Tx struct {
	a    *Allocator
	free []interval // 工作副本
	done bool
}

// Alloc 申请 n 个连续空闲页，返回起始页号（first-fit）。
func (t *Tx) Alloc(n int) (int, error) {
	if t.done {
		return 0, ErrTxDone
	}
	if n <= 0 {
		return 0, fmt.Errorf("pagealloc: alloc size must be > 0")
	}
	for i, iv := range t.free {
		if iv.end-iv.start >= n {
			start := iv.start
			if iv.end-iv.start == n {
				t.free = append(t.free[:i:i], t.free[i+1:]...)
			} else {
				t.free[i].start += n
			}
			return start, nil
		}
	}
	return 0, ErrNoSpace
}

// Free 释放区间 [start, start+n)。区间越界或与空闲页重叠（双重释放）都会被拒绝。
func (t *Tx) Free(start, n int) error {
	if t.done {
		return ErrTxDone
	}
	if n <= 0 || start < 0 || start+n > t.a.total {
		return ErrOutOfRange
	}
	for _, iv := range t.free {
		if start < iv.end && iv.start < start+n {
			return ErrDoubleFree
		}
	}
	t.free = insertMerged(t.free, interval{start, start + n})
	return nil
}

// Commit 持久化事务改动。返回 nil 后，改动可抗进程崩溃与断电。
// 若注入故障返回 ErrCrash，已提交状态保持不变（模拟崩溃，需重新 Open 恢复）。
func (t *Tx) Commit() error {
	if t.done {
		return ErrTxDone
	}
	t.done = true
	t.a.txOpen = false
	if err := t.a.persist(t.free); err != nil {
		return err
	}
	t.a.free = t.free
	return nil
}

// Rollback 丢弃事务的全部改动，已提交状态不受影响。
func (t *Tx) Rollback() error {
	if t.done {
		return ErrTxDone
	}
	t.done = true
	t.a.txOpen = false
	return nil
}

// persist 将空闲区间表原子地写入状态文件：
// 写临时文件 -> fsync -> rename -> 目录 fsync。
func (a *Allocator) persist(free []interval) error {
	if err := a.injector.check(FaultBeforeWrite); err != nil {
		return err
	}
	tmp := filepath.Join(a.dir, tmpName)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(encode(a.total, free)); err != nil {
		f.Close()
		return err
	}
	if err := a.injector.check(FaultBeforeFileSync); err != nil {
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
	if err := a.injector.check(FaultBeforeRename); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(a.dir, stateName)); err != nil {
		return err
	}
	if err := a.injector.check(FaultBeforeDirSync); err != nil {
		return err
	}
	d, err := os.Open(a.dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// insertMerged 将区间插入有序空闲表，并与左右相邻区间合并。
func insertMerged(free []interval, iv interval) []interval {
	i := sort.Search(len(free), func(i int) bool { return free[i].start >= iv.start })
	free = append(free, interval{})
	copy(free[i+1:], free[i:])
	free[i] = iv
	if i > 0 && free[i-1].end == free[i].start {
		free[i-1].end = free[i].end
		free = append(free[:i], free[i+1:]...)
		i--
	}
	if i+1 < len(free) && free[i].end == free[i+1].start {
		free[i].end = free[i+1].end
		free = append(free[:i+1], free[i+2:]...)
	}
	return free
}

// 文件格式：magic(4) | total(8) | count(8) | crc32(4) | [start,end)...（各 8 字节）
// crc32 覆盖除自身外的全部字节，用于识别损坏的状态文件。
func encode(total int, free []interval) []byte {
	buf := make([]byte, 24+16*len(free))
	binary.LittleEndian.PutUint32(buf[0:4], magic)
	binary.LittleEndian.PutUint64(buf[4:12], uint64(total))
	binary.LittleEndian.PutUint64(buf[12:20], uint64(len(free)))
	off := 24
	for _, iv := range free {
		binary.LittleEndian.PutUint64(buf[off:off+8], uint64(iv.start))
		binary.LittleEndian.PutUint64(buf[off+8:off+16], uint64(iv.end))
		off += 16
	}
	crc := crc32.ChecksumIEEE(append(buf[:20:20], buf[24:]...))
	binary.LittleEndian.PutUint32(buf[20:24], crc)
	return buf
}

func decode(data []byte) (int, []interval, error) {
	if len(data) < 24 {
		return 0, nil, fmt.Errorf("pagealloc: state file too short")
	}
	if binary.LittleEndian.Uint32(data[0:4]) != magic {
		return 0, nil, fmt.Errorf("pagealloc: bad magic in state file")
	}
	crc := crc32.ChecksumIEEE(append(data[:20:20], data[24:]...))
	if binary.LittleEndian.Uint32(data[20:24]) != crc {
		return 0, nil, fmt.Errorf("pagealloc: state file checksum mismatch")
	}
	total := int(binary.LittleEndian.Uint64(data[4:12]))
	count := int(binary.LittleEndian.Uint64(data[12:20]))
	if total <= 0 || count < 0 || len(data) != 24+16*count {
		return 0, nil, fmt.Errorf("pagealloc: corrupt state file")
	}
	free := make([]interval, count)
	prevEnd := 0
	for i := range free {
		off := 24 + 16*i
		free[i].start = int(binary.LittleEndian.Uint64(data[off : off+8]))
		free[i].end = int(binary.LittleEndian.Uint64(data[off+8 : off+16]))
		if free[i].start >= free[i].end || free[i].start < prevEnd || free[i].end > total {
			return 0, nil, fmt.Errorf("pagealloc: invalid free interval in state file")
		}
		prevEnd = free[i].end
	}
	return total, free, nil
}
