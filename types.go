package auditjoin

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// NodeID 标识产生审计事件的节点。
type NodeID string

// Generation 是节点单次启动的唯一代次标识；节点重启（包括同名节点重新拉起）
// 必须生成新的 Generation，从而与历史代次的事件天然隔离。
type Generation string

// ShardID 标识承载片段的分片。
type ShardID string

// FragmentID 标识同一事件内的片段。
type FragmentID string

// 组装过程中的典型错误，均可用 errors.Is 判定。
var (
	// ErrInvalidFragment 片段缺少必填字段或序号越界。
	ErrInvalidFragment = errors.New("auditjoin: invalid fragment")
	// ErrConflict 同一 FragmentID 重复到达但载荷不一致。
	ErrConflict = errors.New("auditjoin: conflicting fragment payload")
	// ErrEventSealed 事件已发布且超出补正窗口，迟到片段被拒绝。
	ErrEventSealed = errors.New("auditjoin: event sealed beyond correction window")
	// ErrAssemblerClosed 组装器已关闭。
	ErrAssemblerClosed = errors.New("auditjoin: assembler closed")
)

// EventKey 是审计事件的逻辑键：节点 + 启动代次 + 事件序号。
// 补正发布沿用同一 EventKey，仅递增 Version，不产生新的逻辑事件。
type EventKey struct {
	NodeID     NodeID
	Generation Generation
	Seq        uint64
}

// String 返回稳定的可读键表示。
func (k EventKey) String() string {
	return fmt.Sprintf("%s/%s#%d", k.NodeID, k.Generation, k.Seq)
}

// Fragment 是跨分片到达的审计事件片段。
type Fragment struct {
	NodeID     NodeID
	Generation Generation
	// Seq 是节点在当前代次内单调递增的事件序号，可能发生回绕。
	Seq uint64
	// ShardID 是投递该片段的分片，用于全局水位线对齐。
	ShardID ShardID
	// FragmentID 标识同一事件内的唯一片段，重复投递用于幂等判重。
	FragmentID FragmentID
	// ExpectedFragments 可选：事件预期片段总数；>0 且收齐时立即完整发布。
	ExpectedFragments int
	Payload           []byte
}

// Event 是写入存储的组装结果。
type Event struct {
	Key EventKey
	// Cycle 是序号回绕周期（0 起算），仅用于存储侧内部消歧，
	// 业务侧的事件身份仍然由 Key 表达。
	Cycle uint64
	// Version 从 1 起算，每次补正发布递增。
	Version uint64
	// Complete 为 false 表示超时强制发布的半成品，后续可被补正。
	Complete    bool
	Payload     []byte
	PublishedAt time.Time
	UpdatedAt   time.Time
}

// Record 是存储层保存的事件记录，字段与 Event 对齐。
type Record struct {
	Key         EventKey  `json:"key"`
	Cycle       uint64    `json:"cycle"`
	Version     uint64    `json:"version"`
	Complete    bool      `json:"complete"`
	Payload     []byte    `json:"payload"`
	PublishedAt time.Time `json:"published_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Store 是组装器依赖的幂等存储抽象。
//
// Put 必须满足：
//   - 键不存在：写入，changed=true；
//   - 已存在且入参 Version 更高：覆盖更新，changed=true；
//   - 已存在且 Version 相等、内容一致：幂等重放，changed=false，无错误；
//   - 已存在且 Version 相等、内容不一致：返回 ErrConflict；
//   - 入参 Version 更旧：拒绝写入，changed=false，无错误。
type Store interface {
	Put(ctx context.Context, key EventKey, cycle uint64, rec Record) (stored Record, changed bool, err error)
	Get(ctx context.Context, key EventKey, cycle uint64) (Record, bool, error)
	List(ctx context.Context) ([]Record, error)
}
