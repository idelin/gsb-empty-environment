// Package deque 实现一个磁盘持久化的双端队列。
//
// 设计要点:
//   - 元素定长,按逻辑槽位顺序存放在定长块文件中,随机区间读按
//     槽位号直接定位到 (块文件, 偏移),不需要整体搬移数据。
//   - 块文件编号在一个大小为 maxBlocks 的环上复用;容量上限保证
//     环绕后的块编号永远不会映射到仍被引用的活跃块。
//   - 全部状态(head, count)落在一个很小的 meta 文件中,
//     提交 = 写临时文件 + fsync + rename + 目录 fsync,
//     rename 是唯一提交点,因此一次事务对两端的修改原子生效。
//   - 崩溃恢复:Open 时丢弃未完成的 meta 临时文件,并清理不被
//     当前 meta 引用的孤儿块文件。
package deque

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

var (
	ErrEmpty    = errors.New("deque: empty")
	ErrFull     = errors.New("deque: capacity exceeded, wrapped block id would overwrite an active block")
	ErrRange    = errors.New("deque: index out of range")
	ErrElemSize = errors.New("deque: element size mismatch")
	ErrCorrupt  = errors.New("deque: corrupt meta file")
	ErrTxClosed = errors.New("deque: transaction already closed")
	ErrTxActive = errors.New("deque: another transaction is active")
)

const (
	metaFile    = "deque.meta"
	metaTmpFile = "deque.meta.tmp"

	defaultElemSize      = 64
	defaultElemsPerBlock = 64
	defaultMaxBlocks     = 1024
)

// Options 打开队列时的配置。尺寸参数只在首次创建目录时生效,
// 之后以 meta 文件中持久化的值为准;Fail 每次打开都可重新指定。
type Options struct {
	ElemSize      uint32                 // 元素定长字节数,默认 64
	ElemsPerBlock uint32                 // 每块元素数,默认 64
	MaxBlocks     uint32                 // 块编号环大小(>=2),默认 1024
	Fail          func(FaultPoint) error // 故障注入钩子,返回非 nil 即模拟在该点崩溃
}

// Deque 是打开在某一目录上的磁盘双端队列。
// 同一目录同一时间只允许一个进程打开(进程内用互斥锁串行化,
// 不做跨进程文件锁)。
type Deque struct {
	dir  string
	fail func(FaultPoint) error

	mu       sync.RWMutex
	m        meta
	txActive bool
}

// Open 打开(必要时创建)dir 下的队列,并执行崩溃恢复。
func Open(dir string, opts *Options) (*Deque, error) {
	if opts == nil {
		opts = &Options{}
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	d := &Deque{dir: dir, fail: opts.Fail}

	// 恢复步骤 1:上次提交在 rename 前崩溃会留下 meta 临时文件,作废。
	tmp := filepath.Join(dir, metaTmpFile)
	if _, err := os.Stat(tmp); err == nil {
		if err := os.Remove(tmp); err != nil {
			return nil, err
		}
	}

	// 恢复步骤 2:加载最后一个已提交的 meta(rename 的原子性保证
	// 它要么是旧版本要么是新版本,不会是中间态)。
	buf, err := ioutil.ReadFile(filepath.Join(dir, metaFile))
	switch {
	case os.IsNotExist(err):
		d.m = meta{
			elemSize:      orDefault(opts.ElemSize, defaultElemSize),
			elemsPerBlock: orDefault(opts.ElemsPerBlock, defaultElemsPerBlock),
			maxBlocks:     orDefault(opts.MaxBlocks, defaultMaxBlocks),
			head:          initialHead,
			count:         0,
		}
		if d.m.maxBlocks < 2 {
			return nil, fmt.Errorf("deque: MaxBlocks must be >= 2")
		}
		if err := d.persistMeta(); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		m, err := parseMeta(buf)
		if err != nil {
			return nil, err
		}
		d.m = m
	}

	// 恢复步骤 3:清理不被当前 meta 引用的孤儿块文件
	// (崩溃前已写盘但未随 meta 提交的块)。
	if err := d.sweepOrphanBlocks(); err != nil {
		return nil, err
	}
	return d, nil
}

func orDefault(v, def uint32) uint32 {
	if v == 0 {
		return def
	}
	return v
}

func blockFileName(fileNum uint32) string {
	return fmt.Sprintf("blk_%08d.dat", fileNum)
}

// maxCount 是容量上限(元素数)。约束:任意两个活跃槽位的块序号
// 之差必须 < maxBlocks,否则环绕后两个活跃块会映射到同一个块文件。
// 活跃槽位跨度 <= maxCount-1,块序号跨度 <= (maxCount-1)/epb+1,
// 取 maxCount = (maxBlocks-1)*epb 可保证跨度 <= maxBlocks-1。
func (d *Deque) maxCount() uint64 {
	return uint64(d.m.maxBlocks-1) * uint64(d.m.elemsPerBlock)
}

// fileNum 返回逻辑槽位对应的块文件编号(环上取模)。
func (d *Deque) fileNum(slot uint64) uint32 {
	return uint32((slot / uint64(d.m.elemsPerBlock)) % uint64(d.m.maxBlocks))
}

// slotOffset 返回逻辑槽位在块文件内的字节偏移。
func (d *Deque) slotOffset(slot uint64) int64 {
	return int64(slot%uint64(d.m.elemsPerBlock)) * int64(d.m.elemSize)
}

// activeBlocks 返回当前 meta 引用的全部块文件编号。
func (d *Deque) activeBlocks() map[uint32]bool {
	active := make(map[uint32]bool)
	if d.m.count == 0 {
		return active
	}
	epb := uint64(d.m.elemsPerBlock)
	first := d.m.head / epb
	last := (d.m.head + d.m.count - 1) / epb
	for b := first; b <= last; b++ {
		active[uint32(b%uint64(d.m.maxBlocks))] = true
	}
	return active
}

func (d *Deque) sweepOrphanBlocks() error {
	active := d.activeBlocks()
	entries, err := ioutil.ReadDir(d.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "blk_") || !strings.HasSuffix(name, ".dat") {
			continue
		}
		n, err := strconv.ParseUint(name[4:len(name)-4], 10, 32)
		if err != nil {
			continue
		}
		if !active[uint32(n)] {
			if err := os.Remove(filepath.Join(d.dir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// persistMeta 把 d.m 原子落盘:写临时文件 + fsync + rename + 目录 fsync。
func (d *Deque) persistMeta() error {
	if err := writeFileSync(filepath.Join(d.dir, metaTmpFile), d.m.marshal()); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(d.dir, metaTmpFile), filepath.Join(d.dir, metaFile)); err != nil {
		return err
	}
	return syncDir(d.dir)
}

func writeFileSync(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// readSlotLocked 从磁盘读取一个已提交槽位的元素,调用方须持有锁。
func (d *Deque) readSlotLocked(slot uint64) ([]byte, error) {
	path := filepath.Join(d.dir, blockFileName(d.fileNum(slot)))
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := make([]byte, d.m.elemSize)
	if _, err := f.ReadAt(out, d.slotOffset(slot)); err != nil {
		return nil, err
	}
	return out, nil
}

// Len 返回已提交的元素个数。
func (d *Deque) Len() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return int(d.m.count)
}

// ElemSize 返回元素定长字节数。
func (d *Deque) ElemSize() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return int(d.m.elemSize)
}

// Bounds 返回逻辑头尾槽位号(诊断/测试用)。空队列时 head == tail。
func (d *Deque) Bounds() (head, tail uint64) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.m.head, d.m.tail()
}

// At 读取逻辑下标 i 处的元素(0 为队首)。
func (d *Deque) At(i int) ([]byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if i < 0 || uint64(i) >= d.m.count {
		return nil, ErrRange
	}
	return d.readSlotLocked(d.m.head + uint64(i))
}

// ReadRange 读取逻辑下标 [start, start+n) 的元素,支持随机区间读。
func (d *Deque) ReadRange(start, n int) ([][]byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if start < 0 || n < 0 || uint64(start+n) > d.m.count {
		return nil, ErrRange
	}
	out := make([][]byte, 0, n)
	for i := start; i < start+n; i++ {
		v, err := d.readSlotLocked(d.m.head + uint64(i))
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Front 返回队首元素。
func (d *Deque) Front() ([]byte, error) { return d.At(0) }

// Back 返回队尾元素。
func (d *Deque) Back() ([]byte, error) {
	d.mu.RLock()
	n := int(d.m.count)
	d.mu.RUnlock()
	return d.At(n - 1)
}

// 以下是便捷方法:单操作事务。

func (d *Deque) PushLeft(v []byte) error {
	return d.withTx(func(tx *Tx) error { return tx.PushLeft(v) })
}

func (d *Deque) PushRight(v []byte) error {
	return d.withTx(func(tx *Tx) error { return tx.PushRight(v) })
}

func (d *Deque) PopLeft() ([]byte, error) {
	var out []byte
	err := d.withTx(func(tx *Tx) error {
		v, err := tx.PopLeft()
		if err != nil {
			return err
		}
		out = v
		return nil
	})
	return out, err
}

func (d *Deque) PopRight() ([]byte, error) {
	var out []byte
	err := d.withTx(func(tx *Tx) error {
		v, err := tx.PopRight()
		if err != nil {
			return err
		}
		out = v
		return nil
	})
	return out, err
}

func (d *Deque) withTx(fn func(*Tx) error) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
