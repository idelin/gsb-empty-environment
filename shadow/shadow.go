// Package shadow 提供 AddressSanitizer 风格的影子内存，供插桩回调在运行时
// 检测堆内存错误：越过块尾（右红区/部分块尾）、越到块前（左红区）以及
// 释放后使用（use-after-free）。
//
// 影子编码：每 8 字节应用内存对应 1 个影子字节。
//
//	0        ：对应 8 字节全部可访问
//	1 .. 7   ：仅前 k 字节可访问（块尾部分字节）
//	负值      ：左红区、右红区或已释放（见 Kind）
//
// 使用方式：分配挂钩调用 Malloc 登记并布置红区，释放挂钩调用 Free 将块
// 毒化并送入隔离区；每次 1/2/4/8/16 字节的读写前调用 Check 判定合法性。
// 地址需按 8 字节对齐。
package shadow

import "fmt"

// 影子粒度：1 个影子字节覆盖 8 字节应用内存。
const Granule = 8

// 影子字节中的负值编码。
const (
	codeLeftRedzone  int8 = -1
	codeRightRedzone int8 = -2
	codeFreed        int8 = -3
)

// Kind 是内存错误的类型。
type Kind int

const (
	// KindLeftRedzone 表示访问落入块前的左红区。
	KindLeftRedzone Kind = iota + 1
	// KindRightRedzone 表示访问越过块尾（含块尾部分字节之外的残留字节）。
	KindRightRedzone
	// KindUseAfterFree 表示访问已被释放、仍在隔离区中的块。
	KindUseAfterFree
)

func (k Kind) String() string {
	switch k {
	case KindLeftRedzone:
		return "left-redzone"
	case KindRightRedzone:
		return "right-redzone"
	case KindUseAfterFree:
		return "use-after-free"
	default:
		return "unknown"
	}
}

// Error 描述一次非法访问，实现 error 接口。
type Error struct {
	Kind  Kind   // 错误类型
	Addr  uint64 // 首个非法字节的应用地址
	Size  int    // 本次访问的字节数
	Write bool   // true 为写访问，false 为读访问
}

func (e *Error) Error() string {
	op := "read"
	if e.Write {
		op = "write"
	}
	return fmt.Sprintf("shadow: %s: %s of %d byte(s) at 0x%x", e.Kind, op, e.Size, e.Addr)
}

// block 记录一个已登记堆块的元信息。
type block struct {
	size  uint64
	freed bool
}

// Mem 是影子内存状态。请用 New 构造。
type Mem struct {
	// QuarantineLimit 是隔离区容纳已释放块的总字节上限；
	// 超过时才从最旧的块开始回收。
	QuarantineLimit uint64

	shadow map[uint64]int8 // 键为 addr>>3
	blocks map[uint64]*block

	queue []uint64 // 隔离区中的块地址，最旧的在前
	qbyte uint64   // 隔离区当前总字节数
}

// New 构造一个影子内存状态，quarantineLimit 为隔离区总字节上限。
func New(quarantineLimit uint64) *Mem {
	return &Mem{
		QuarantineLimit: quarantineLimit,
		shadow:          make(map[uint64]int8),
		blocks:          make(map[uint64]*block),
	}
}

// Malloc 在 addr 处登记一个 size 字节的堆块，并布置影子：
// 块前 8 字节为左红区，块尾所在槽按部分字节编码，块后 8 字节为右红区。
// addr 必须按 8 字节对齐，size 必须大于 0，且地址不得与已有块重复。
func (m *Mem) Malloc(addr, size uint64) error {
	if size == 0 {
		return fmt.Errorf("shadow: malloc of zero bytes at 0x%x", addr)
	}
	if addr%Granule != 0 {
		return fmt.Errorf("shadow: malloc address 0x%x is not %d-byte aligned", addr, Granule)
	}
	if _, ok := m.blocks[addr]; ok {
		return fmt.Errorf("shadow: malloc duplicates existing block at 0x%x", addr)
	}

	first := addr / Granule
	last := (addr + size - 1) / Granule

	m.shadow[first-1] = codeLeftRedzone
	for i := first; i <= last; i++ {
		m.shadow[i] = 0
	}
	if rem := (addr + size) % Granule; rem != 0 {
		m.shadow[last] = int8(rem) // 块尾部分字节：仅前 rem 字节可访问
	}
	m.shadow[last+1] = codeRightRedzone

	m.blocks[addr] = &block{size: size}
	return nil
}

// Free 释放 addr 处的堆块：用户区被毒化为“已释放”，块进入隔离区；
// 当隔离区总字节超过 QuarantineLimit 时，从最旧的块开始回收，
// 回收后其影子被清除，内存可再次分配。
// 重复释放或释放未登记的地址会返回错误。
func (m *Mem) Free(addr uint64) error {
	b, ok := m.blocks[addr]
	if !ok {
		return fmt.Errorf("shadow: free of untracked address 0x%x", addr)
	}
	if b.freed {
		return fmt.Errorf("shadow: double free of 0x%x", addr)
	}
	b.freed = true

	first := addr / Granule
	last := (addr + b.size - 1) / Granule
	for i := first; i <= last; i++ {
		m.shadow[i] = codeFreed
	}

	m.queue = append(m.queue, addr)
	m.qbyte += b.size
	for m.qbyte > m.QuarantineLimit && len(m.queue) > 0 {
		m.recycle(m.queue[0])
		m.queue = m.queue[1:]
	}
	return nil
}

// recycle 回收隔离区中最旧的块：清除其影子并注销元信息。
func (m *Mem) recycle(addr uint64) {
	b := m.blocks[addr]
	first := addr / Granule
	last := (addr + b.size - 1) / Granule
	for i := first - 1; i <= last+1; i++ {
		delete(m.shadow, i)
	}
	delete(m.blocks, addr)
	m.qbyte -= b.size
}

// Check 判定对 [addr, addr+size) 的一次读（write=false）或写（write=true）
// 是否合法。size 通常为 1、2、4、8、16。合法时返回 nil，否则返回描述
// 首个非法字节的 *Error。
func (m *Mem) Check(addr uint64, size int, write bool) *Error {
	for i := 0; i < size; i++ {
		a := addr + uint64(i)
		s, ok := m.shadow[a/Granule]
		if !ok || s == 0 {
			continue // 未跟踪的内存视为可访问
		}
		if s > 0 {
			if a%Granule < uint64(s) {
				continue // 块尾部分字节：前 s 字节可访问
			}
			return &Error{Kind: KindRightRedzone, Addr: a, Size: size, Write: write}
		}
		switch s {
		case codeLeftRedzone:
			return &Error{Kind: KindLeftRedzone, Addr: a, Size: size, Write: write}
		case codeRightRedzone:
			return &Error{Kind: KindRightRedzone, Addr: a, Size: size, Write: write}
		case codeFreed:
			return &Error{Kind: KindUseAfterFree, Addr: a, Size: size, Write: write}
		}
	}
	return nil
}

// QuarantineLen 返回隔离区中待回收的块数。
func (m *Mem) QuarantineLen() int { return len(m.queue) }

// QuarantineBytes 返回隔离区当前占用的总字节数。
func (m *Mem) QuarantineBytes() uint64 { return m.qbyte }
