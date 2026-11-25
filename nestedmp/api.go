package nestedmp

import (
	"context"
	"crypto/sha256"
	"io"
)

// StagingHost 由宿主对象库实现，提供事务性暂存能力。
// 一个解析器生命周期内恰好使用一个事务：成功才提交，任何失败都回滚，
// 因此已经写入底层存储的暂存对象在回滚后不得变为可读取。
type StagingHost interface {
	// Begin 开启一个新的暂存事务。
	Begin(ctx context.Context) (StagingTxn, error)
}

// StagingTxn 是宿主提供的事务性暂存句柄。
//
// Put 为某个附件部件打开一个只增的暂存写入端；写入端收到的字节序列
// 即去除该部件自身定界符后的原始正文。Commit 可能迟到（阻塞），
// 在 Commit 成功返回前，暂存内容必须对外不可读；Rollback 丢弃该事务
// 的全部暂存内容（包括尚未 Close 的写入端）。
type StagingTxn interface {
	Put(ctx context.Context, partName string) (io.WriteCloser, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// EventKind 是验收追踪事件的种类。
type EventKind string

const (
	// EvTxnBegin：事务已开启。
	EvTxnBegin EventKind = "txn_begin"
	// EvPushBoundary：新一层边界进入活动栈。
	EvPushBoundary EventKind = "push_boundary"
	// EvPopBoundary：一层边界离开活动栈。
	EvPopBoundary EventKind = "pop_boundary"
	// EvPartOpen：部件头解析完成，部件开始（附件此时已调用 Put）。
	EvPartOpen EventKind = "part_open"
	// EvBodyWrite：一段原始正文字节被哈希，并写入暂存或交给子层。
	EvBodyWrite EventKind = "body_write"
	// EvDigestOK：部件 SHA-256 与头声明一致。
	EvDigestOK EventKind = "digest_ok"
	// EvPartClose：部件正常结束，写入端已关闭。
	EvPartClose EventKind = "part_close"
	// EvCommit：事务提交回调返回成功。
	EvCommit EventKind = "commit"
	// EvRollback：事务已回滚。
	EvRollback EventKind = "rollback"
	// EvMemoryLimit：常驻内存超过预算，解析被终止。
	EvMemoryLimit EventKind = "memory_limit"
)

// Event 记录一次解析动作的字节位置与层级，供验收重放。
type Event struct {
	Kind   EventKind
	Level  int    // 根层为 0
	Name   string // 部件名或边界名
	Offset int64  // 事件相关的绝对字节偏移（写入事件为该段起始位置）
	Length int    // 写入事件的字节数
	Digest string // 摘要事件的十六进制摘要
	Err    error  // 失败事件携带的原因
}

// Tracer 接收解析过程中的事件回调。实现必须允许在解析 goroutine 上同步调用。
type Tracer interface {
	OnEvent(Event)
}

// PartInfo 描述一个已验证部件的字节位置与元数据。
type PartInfo struct {
	Level       int
	Type        string
	Name        string
	Boundary    string // 该层使用的边界名
	HeaderStart int64  // 开放分隔行之后的首字节
	BodyStart   int64  // 头块空行之后的首字节
	BodyEnd     int64  // 结束/下一分隔行前导 CRLF 的首字节
	EndOffset   int64  // 该部件定界行之后的首字节
	Size        int64  // 原始正文长度
	Digest      [sha256.Size]byte
}

// Result 是成功解析的结构化结果。
type Result struct {
	// Parts 按部件在线上出现的顺序排列。
	Parts []PartInfo
	// TotalBytes 是消费的原始输入字节总数。
	TotalBytes int64
}

// Options 控制解析器行为。
type Options struct {
	// RootBoundary 是根层边界名，必须为 1..70 个 ASCII 字母数字。
	RootBoundary string
	// Host 是宿主提供的事务性暂存回调。
	Host StagingHost
	// MemoryBudget 限制常驻内存字节数；0 表示使用默认预算。
	// 预算按固定窗口加各活跃边界匹配器/头块的结构性占用核算，
	// 推入新一层导致超限时解析立即终止并回滚。
	MemoryBudget int64
	// Window 是正文批量写入暂存的固定窗口大小；0 表示默认 4096。
	Window int
	// Tracer 可选，用于接收字节位置与写入回调序列。
	Tracer Tracer
}

// Parser 是单线程使用的流式解析器。
//
// Write 可接受任意切块（包括单字节），数据按背压同步推进：
// 宿主暂存写入端阻塞时，Write 随之阻塞。Cancel 或 ctx 取消会让
// 后续 Write/Close 快速失败并回滚整个事务。一个解析器只能成功 Close 一次。
type Parser interface {
	io.Writer
	// WriteCtx 在 ctx 下写入一个切块，返回消费字节数。
	WriteCtx(ctx context.Context, chunk []byte) (int, error)
	// CloseCtx 声明输入结束；成功时提交事务并返回结果。
	CloseCtx(ctx context.Context) (*Result, error)
	// Cancel 请求取消，触发事务回滚。可从其他 goroutine 调用。
	Cancel()
}
