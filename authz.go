package netsec

import "context"

// Authorizer 由调用方（WebDAV 网关）实现，对单个资源做访问判定。
// 所有 path 均为已规范化的租户内绝对路径（以 "/" 开头，不含 "." 或 ".." 段）。
type Authorizer interface {
	CanRead(ctx context.Context, tenant, path string) bool
	CanWrite(ctx context.Context, tenant, path string) bool
	CanDelete(ctx context.Context, tenant, path string) bool
}

// Member 描述源集合树中的一个成员。
type Member struct {
	Path       string // 规范化绝对路径
	Collection bool   // 是否为集合（目录）
	Symlink    bool   // 是否为符号链接
}

// FileSystem 由调用方实现，提供执行 COPY/MOVE 所需的存储操作。
type FileSystem interface {
	// Stat 返回 path 对应的成员；不存在时返回 ErrNotExist。
	Stat(ctx context.Context, path string) (Member, error)
	// Walk 以确定顺序（父先于子）返回 path 自身及其全部后代成员。
	Walk(ctx context.Context, path string) ([]Member, error)
	// Copy 把 src 复制到 dst；对集合成员仅创建空集合。
	Copy(ctx context.Context, src, dst string) error
	// Remove 删除 path（集合则递归删除）。
	Remove(ctx context.Context, path string) error
}

// FailureMode 决定部分成员失败时的处理策略。
type FailureMode int

const (
	// RollbackAll 任一成员失败时撤销已复制的全部目标项，整体失败。
	RollbackAll FailureMode = iota
	// KeepCopied 保留已成功复制的目标项，以 207 逐项报告失败。
	KeepCopied
)

// Options 为引擎配置，由调用方在构造时给出。
type Options struct {
	// Tenant 当前请求的租户标识，同时用于授权回调。
	Tenant string
	// TenantRoot 该租户在 URL 命名空间中的根路径，如 "/tenants/acme"。
	// Destination 与源都必须落在该前缀之内。
	TenantRoot string
	// OnPartialFailure 部分失败策略，默认 RollbackAll。
	OnPartialFailure FailureMode
	// AllowSymlinks 为 false（默认）时，树中的符号链接成员逐项拒绝（403）。
	AllowSymlinks bool
}
