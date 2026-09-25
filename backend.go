package rootslot

import (
	"os"
	"path/filepath"
)

// Backend 是存储层抽象。读语义：ReadAt 返回“当前进程可见”的字节
// （已写但未同步的字节对进程可见）；Sync 之后字节才进入“可恢复”集合。
type Backend interface {
	ReadAt(p []byte, off int64) (int, error)
	WriteAt(p []byte, off int64) (int, error)
	Sync() error
	Close() error
}

// FileBackend 是真实文件后端，文件位于调用方指定目录下。
type FileBackend struct {
	f *os.File
}

const slotsFileName = "root.slots"

func NewFileBackend(dir string) (*FileBackend, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, slotsFileName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	// 预分配到两个槽的大小，保证读写边界明确。
	if err := f.Truncate(numSlots * SlotSize); err != nil {
		f.Close()
		return nil, err
	}
	return &FileBackend{f: f}, nil
}

func (b *FileBackend) ReadAt(p []byte, off int64) (int, error)  { return b.f.ReadAt(p, off) }
func (b *FileBackend) WriteAt(p []byte, off int64) (int, error) { return b.f.WriteAt(p, off) }
func (b *FileBackend) Sync() error                              { return b.f.Sync() }
func (b *FileBackend) Close() error                             { return b.f.Close() }

// SimBackend 是可注入故障的持久化模拟器，明确区分三种字节状态：
//
//	已写(written)：   WriteAt 生效后进入 volatile，进程内可读；
//	已同步(synced)：  Sync 成功后从 volatile 复制到 durable；
//	可恢复(recoverable)：崩溃后仅 durable 存活（断电语义）。
//
// 进程崩溃（powerLoss=false）保留 volatile——操作系统页缓存仍在；
// 断电（powerLoss=true）丢弃 volatile，只剩 durable。
type SimBackend struct {
	volatile []byte
	durable  []byte

	// 故障注入程序，全部以下标索引（从 0 开始的第 N 次 WriteAt / Sync）。
	WriteCount int
	SyncCount  int

	TornAt     map[int]int  // 第 N 次写只持久化前 M 字节（撕裂写）
	DropAt     map[int]bool // 第 N 次写整体丢失（调用方仍看到成功）
	DelayAt    map[int]bool // 第 N 次写被重排到下一次 Sync 之后才落盘
	SyncNoopAt map[int]bool // 第 N 次 Sync 实际未刷盘（同步丢失）

	delayed []delayedWrite
	closed  bool
}

type delayedWrite struct {
	data []byte
	off  int64
}

func NewSimBackend() *SimBackend {
	size := numSlots * SlotSize
	return &SimBackend{
		volatile: make([]byte, size),
		durable:  make([]byte, size),
	}
}

func (b *SimBackend) ReadAt(p []byte, off int64) (int, error) {
	n := copy(p, b.volatile[off:])
	return n, nil
}

func (b *SimBackend) WriteAt(p []byte, off int64) (int, error) {
	idx := b.WriteCount
	b.WriteCount++
	if b.DropAt[idx] {
		return len(p), nil // 写丢失：对调用方表现为成功
	}
	if b.DelayAt[idx] {
		cp := append([]byte(nil), p...)
		b.delayed = append(b.delayed, delayedWrite{data: cp, off: off})
		return len(p), nil // 重排：推迟到下次 Sync 之后才生效
	}
	if keep, ok := b.TornAt[idx]; ok && keep < len(p) {
		p = p[:keep] // 撕裂：只落盘前缀
	}
	copy(b.volatile[off:], p)
	return len(p), nil
}

func (b *SimBackend) Sync() error {
	idx := b.SyncCount
	b.SyncCount++
	if !b.SyncNoopAt[idx] {
		b.durable = append([]byte(nil), b.volatile...)
	}
	// 被重排的写在本次 Sync 之后才进入 volatile（即可见但不可恢复）。
	for _, w := range b.delayed {
		copy(b.volatile[w.off:], w.data)
	}
	b.delayed = nil
	return nil
}

// Crash 模拟崩溃。powerLoss=true 表示断电：只有已同步字节可恢复；
// powerLoss=false 表示进程崩溃：已写字节仍在（OS 页缓存存活）。
func (b *SimBackend) Crash(powerLoss bool) {
	if powerLoss {
		b.volatile = append([]byte(nil), b.durable...)
	}
	b.delayed = nil
}

// DurableBytes 返回当前可恢复字节（调试用）。
func (b *SimBackend) DurableBytes() []byte { return append([]byte(nil), b.durable...) }

// WrittenBytes 返回当前已写字节（调试用）。
func (b *SimBackend) WrittenBytes() []byte { return append([]byte(nil), b.volatile...) }

func (b *SimBackend) Close() error { b.closed = true; return nil }
