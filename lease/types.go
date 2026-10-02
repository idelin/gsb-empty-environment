// Package lease 实现支持动态续期与平滑优雅移交的租约控制器。
package lease

import (
	"errors"
	"time"
)

var (
	ErrLeaseNotFound  = errors.New("lease: 租约不存在")
	ErrLeaseExpired   = errors.New("lease: 租约已过期")
	ErrNotHolder      = errors.New("lease: 调用方不是当前持有者")
	ErrNotPrimary     = errors.New("lease: 节点不是主节点")
	ErrStaleFencing   = errors.New("lease: 过期的 fencing token")
	ErrLeaseFrozen    = errors.New("lease: 租约正在移交,写入被冻结")
	ErrNoCandidate    = errors.New("lease: 接盘候选人未注册")
	ErrNodeDown       = errors.New("lease: 候选节点不可用")
	ErrHandoverFailed = errors.New("lease: 移交失败")
)

// Lease 租约凭证,包含 fencing token 与业务上下文。
type Lease struct {
	ID           string            // 租约标识
	HolderID     string            // 当前持有者
	FencingToken uint64            // 单调递增的 fencing token
	Term         time.Duration     // 每次续期延长的时长
	ExpiresAt    time.Time         // 到期时间
	Context      map[string]string // 随租约移交的业务上下文
}

// Remaining 返回租约剩余有效时长。
func (l Lease) Remaining(now time.Time) time.Duration {
	d := l.ExpiresAt.Sub(now)
	if d < 0 {
		return 0
	}
	return d
}

// HandoverPackage 交接包:持有者主动释放时交给候选人的完整租约上下文。
type HandoverPackage struct {
	Lease         Lease     // 继承剩余 TTL 与业务上下文的新凭证
	FromID        string    // 原持有者
	TransferredAt time.Time // 移交时刻
}

func copyContext(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
