// Package netcode 在确定性模拟层之上实现双人回滚同步。
package netcode

import "example.org/rollback/sim"

// HashEntry 是一次定期状态哈希交换的载荷。
type HashEntry struct {
	Frame int
	Hash  [32]byte
}

// Bundle 是一名玩家每个 tick 发给对端的消息包。
//
// 输入以“窗口内全量重发”的方式携带，因此单个包丢失后，
// 后续包仍能补齐所有已确认输入，无需独立的 ACK/重传协议。
type Bundle struct {
	FromPlayer int
	ToPlayer   int
	Inputs     []sim.Input
	Hashes     []HashEntry
	// NeedState 非零时请求对端返回该帧边界的完整状态快照，
	// 用于超出本地回滚窗口后的恢复。
	NeedState int
	// StateSnapshot 是对端请求的权威状态快照（含帧号）。
	StateSnapshot *sim.State
}

// DesyncEvent 记录一次双方同帧哈希不一致。
type DesyncEvent struct {
	Frame  int
	Local  [32]byte
	Remote [32]byte
}

// Stats 暴露端点内部计数器，供测试与观测使用。
type Stats struct {
	FramesSimulated int // 实际执行的模拟步数（含回滚重算）
	Rollbacks       int // 触发回滚的次数
	Resimulated     int // 回滚重算的帧数
	Predictions     int // 远端输入缺失、按上一帧预测的次数
	Pauses          int // 超出回滚窗口而暂停等待的次数
}
