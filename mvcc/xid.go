// Package mvcc 实现 PostgreSQL 风格的 32 位回绕 MVCC 可见性判定，
// 以及 VACUUM FREEZE 堆页扫描清理引擎（含 MultiXact 行锁组语义与
// Visibility Map 的 ALL_VISIBLE / ALL_FROZEN 精确判定）。
package mvcc

// XID 是 32 位无符号事务号。
type XID uint32

// 三个特殊事务号。它们不参与模 2^32 的环形运算，在比较顺序中被
// 固定钉在所有普通事务号之前：Invalid < Bootstrap < Frozen < 普通 XID。
const (
	InvalidXid     XID = 0
	BootstrapXid   XID = 1
	FrozenXid      XID = 2
	FirstNormalXid XID = 3
	MaxXid         XID = ^XID(0) // 0xFFFFFFFF
)

// XidPrecedes 判定事务号 a 在环形时间线上是否早于 b。
//
// 任意一方是特殊事务号（0..2）时走固定线性顺序，绝不做模运算；
// 双方均为普通事务号 [3, 2^32-1] 时，按 PostgreSQL 规则使用有符号
// 32 位差值 int32(a-b) < 0 判定，天然支持 0xFFFFFFFF -> 3 的回绕。
func XidPrecedes(a, b XID) bool {
	if a < FirstNormalXid || b < FirstNormalXid {
		return a < b
	}
	return int32(a-b) < 0
}

// XidPrecedesOrEqual 判定 a 早于或等于 b。
func XidPrecedesOrEqual(a, b XID) bool {
	return a == b || XidPrecedes(a, b)
}

// XidFollows 判定 a 在环形时间线上晚于 b。
func XidFollows(a, b XID) bool {
	return XidPrecedes(b, a)
}
