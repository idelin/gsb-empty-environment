// Package dma 为虚拟设备宿主提供可嵌入的 DMA 描述符“先冻结、后执行”工具包。
//
// 核心安全目标是关闭时间差（TOCTOU）窗口：描述符位于客户机可写内存中，
// 设备执行时若重新读取该内存，攻击者可以把校验通过的地址替换成受保护页。
// 本包在提交前把整条描述符链复制到宿主私有、不可变的 FrozenChain 中，
// 设备回调只接受该私有数组，执行阶段不再触碰客户机描述符内存。
package dma

import (
	"encoding/binary"
	"errors"
	"math/bits"
)

// 描述符环的硬约束。
const (
	MaxRingEntries = 64
	DescriptorSize = 16
	MinTransferLen = 1
	MaxTransferLen = 1 << 20 // 1 MiB
	PageSize       = 4096
)

// Flag 是描述符 flags 字段的位定义，仅允许 READ/WRITE/END 三位。
type Flag uint16

const (
	FlagRead  Flag = 1 << 0
	FlagWrite Flag = 1 << 1
	FlagEnd   Flag = 1 << 2
	flagMask       = FlagRead | FlagWrite | FlagEnd
)

// Read 报告描述符是否请求设备到客户机方向的读取。
func (f Flag) Read() bool { return f&FlagRead != 0 }

// Write 报告描述符是否请求客户机到设备方向的写入。
func (f Flag) Write() bool { return f&FlagWrite != 0 }

// End 报告该描述符是否为链尾。
func (f Flag) End() bool { return f&FlagEnd != 0 }

// Perm 是已登记 DMA 页的权限位。
type Perm uint8

const (
	PermRead  Perm = 1 << 0
	PermWrite Perm = 1 << 1
)

// Contains 报告 p 是否包含 q 要求的全部权限。
func (p Perm) Contains(q Perm) bool { return p&q == q }

// 调用方可用 errors.Is 匹配下列哨兵错误。
var (
	// ErrRingSize 环容量不在 1..MaxRingEntries 范围内。
	ErrRingSize = errors.New("dma: descriptor ring size out of range")
	// ErrIndexOutOfRange next/head 索引越过环边界。
	ErrIndexOutOfRange = errors.New("dma: descriptor index out of range")
	// ErrBadFlags flags 出现保留位或缺少读写方向。
	ErrBadFlags = errors.New("dma: invalid descriptor flags")
	// ErrBadLength 长度不在 1..1MiB。
	ErrBadLength = errors.New("dma: descriptor length out of range")
	// ErrAddressOverflow 地址加长度发生 64 位回绕。
	ErrAddressOverflow = errors.New("dma: address plus length overflows")
	// ErrDuplicateEntry 遍历中第二次访问同一个环槽位（共享子链/环）。
	ErrDuplicateEntry = errors.New("dma: descriptor chain visits an entry more than once")
	// ErrUnterminatedChain 遍历耗尽环容量仍未遇到 END。
	ErrUnterminatedChain = errors.New("dma: descriptor chain is not terminated by END")
	// ErrBudgetExceeded 链上传输字节总和超过预算。
	ErrBudgetExceeded = errors.New("dma: total transfer byte budget exceeded")

	// ErrVersionChanged 快照期间版本号发生了前进。
	ErrVersionChanged = errors.New("dma: guest memory version changed while taking snapshot")
	// ErrVersionRegressed 版本号出现倒退，属于 ABA 迹象。
	ErrVersionRegressed = errors.New("dma: guest memory version regressed (ABA)")
	// ErrABAMutation 版本号不变但两次读取字节不同，属于同版本 ABA 改写。
	ErrABAMutation = errors.New("dma: guest memory bytes changed while version held steady (ABA)")
	// ErrShortRead 客户内存未能填满请求缓冲区。
	ErrShortRead = errors.New("dma: short read from guest memory")
	// ErrBadMemory 客户内存 ReadAt 返回了其它 I/O 错误。
	ErrBadMemory = errors.New("dma: guest memory read failed")

	// ErrAlignment 登记/保留区间未按页对齐。
	ErrAlignment = errors.New("dma: region is not page aligned")
	// ErrRegionOverlap 新区间与已登记区间重叠。
	ErrRegionOverlap = errors.New("dma: region overlaps an existing registration")
	// ErrPageNotMapped 传输区间落在未登记 DMA 页上。
	ErrPageNotMapped = errors.New("dma: transfer range is not backed by a registered DMA page")
	// ErrReservedRegion 传输区间触碰保留区。
	ErrReservedRegion = errors.New("dma: transfer range touches a reserved region")
	// ErrPermissionDenied 传输权限超过底层页登记权限。
	ErrPermissionDenied = errors.New("dma: transfer exceeds page permissions")

	// ErrCallbackFailed 设备回调拒绝了本次提交。
	ErrCallbackFailed = errors.New("dma: device callback rejected submission")
	// ErrStaleCompletion 完成回执属于旧代次、未知项或重复提交。
	ErrStaleCompletion = errors.New("dma: stale or duplicate completion receipt")
)

// Descriptor 是一个 16 字节描述符解码后的只读值拷贝。
type Descriptor struct {
	Addr   uint64
	Length uint32
	Next   uint16
	Flags  Flag
}

// Perm 把描述符方向翻译成页权限位。
func (d Descriptor) Perm() Perm {
	var p Perm
	if d.Flags.Read() {
		p |= PermRead
	}
	if d.Flags.Write() {
		p |= PermWrite
	}
	return p
}

func (d Descriptor) validate() error {
	if d.Flags&^flagMask != 0 {
		return ErrBadFlags
	}
	// 只有 END 而没有任何传输方向的描述符没有意义，一并拒绝。
	if d.Flags&(FlagRead|FlagWrite) == 0 {
		return ErrBadFlags
	}
	if d.Length < MinTransferLen || d.Length > MaxTransferLen {
		return ErrBadLength
	}
	if _, carry := bits.Add64(d.Addr, uint64(d.Length), 0); carry != 0 {
		return ErrAddressOverflow
	}
	return nil
}

// parseDescriptor 按小端布局解析 16 字节：
//
//	0..7   uint64 地址
//	8..11  uint32 长度
//	12..13 uint16 next 索引
//	14..15 uint16 flags
func parseDescriptor(b []byte) Descriptor {
	return Descriptor{
		Addr:   binary.LittleEndian.Uint64(b[0:8]),
		Length: binary.LittleEndian.Uint32(b[8:12]),
		Next:   binary.LittleEndian.Uint16(b[12:14]),
		Flags:  Flag(binary.LittleEndian.Uint16(b[14:16])),
	}
}

// encodeDescriptor 是测试与适配器构造客户机镜像时使用的编码助手。
func encodeDescriptor(b []byte, d Descriptor) {
	binary.LittleEndian.PutUint64(b[0:8], d.Addr)
	binary.LittleEndian.PutUint32(b[8:12], d.Length)
	binary.LittleEndian.PutUint16(b[12:14], d.Next)
	binary.LittleEndian.PutUint16(b[14:16], uint16(d.Flags))
}

// GuestMemory 抽象客户机内存。Version 必须单调不减；
// 任何写操作都应当推动版本号前进，本包另外防御性地检测版本倒退
// 以及“版本不动、字节变化”的 ABA 场景。
type GuestMemory interface {
	ReadAt(p []byte, off uint64) (n int, err error)
	Version() uint64
}

// readFull 要求 ReadAt 精确填满 p，否则返回 ErrShortRead/ErrBadMemory。
func readFull(mem GuestMemory, p []byte, off uint64) error {
	n, err := mem.ReadAt(p, off)
	if n == len(p) {
		return nil
	}
	if err != nil {
		return errors.Join(ErrBadMemory, err)
	}
	return ErrShortRead
}
