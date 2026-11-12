package netsec

// Capability 对应 Linux capability 编号（linux/capability.h）。
type Capability uint32

const (
	CapNetAdmin Capability = 12 // CAP_NET_ADMIN
	CapNetRaw   Capability = 13 // CAP_NET_RAW
	CapSysAdmin Capability = 21 // CAP_SYS_ADMIN
	CapSysBoot  Capability = 22 // CAP_SYS_BOOT
	CapMknod    Capability = 27 // CAP_MKNOD
)

// capLast 是内核当前定义的最大 capability 编号（CAP_CHECKPOINT_RESTORE = 40）。
const capLast = 40

// CapSet 是 capability 位图，语义同内核 kernel_cap_t。
type CapSet uint64

// FullCapSet 表示一个用户命名空间内的全部 capability。
const FullCapSet CapSet = (CapSet(1) << (capLast + 1)) - 1

// NewCapSet 由若干 capability 构造位图。
func NewCapSet(caps ...Capability) CapSet {
	var s CapSet
	for _, c := range caps {
		s |= CapSet(1) << c
	}
	return s
}

// Has 报告位图中是否置位了 c。
func (s CapSet) Has(c Capability) bool {
	if c > capLast {
		return false
	}
	return s&(CapSet(1)<<c) != 0
}

// Union 返回两个位图的并集。
func (s CapSet) Union(o CapSet) CapSet { return s | o }

// Intersect 返回两个位图的交集。
func (s CapSet) Intersect(o CapSet) CapSet { return s & o }
