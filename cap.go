package netsec

// Cap 是 Linux 能力（capability）的模拟编号，取值与内核
// include/uapi/linux/capability.h 中的 CAP_* 常量保持一致。
type Cap int

// CAP_SYS_PTRACE 允许任意 ptrace 附着，是绕过 Yama scope 1/2 的关键能力。
const CAP_SYS_PTRACE Cap = 19

// CapSet 是进程持有的能力集合，map 取值恒为 true。
type CapSet map[Cap]bool

// NewCapSet 用给定能力创建集合。
func NewCapSet(caps ...Cap) CapSet {
	set := make(CapSet, len(caps))
	for _, capability := range caps {
		set[capability] = true
	}
	return set
}

// Has 报告集合是否持有指定能力。
func (set CapSet) Has(capability Cap) bool {
	return set != nil && set[capability]
}

// Add 向集合加入能力。接收者必须由 NewCapSet 或 make 初始化，
// 向 nil map 写入会 panic，与内建 map 的语义保持一致。
func (set CapSet) Add(capability Cap) {
	set[capability] = true
}

// Drop 从集合移除能力。
func (set CapSet) Drop(capability Cap) {
	delete(set, capability)
}
