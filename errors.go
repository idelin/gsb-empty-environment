package taskpkg

import "errors"

// 能力句柄在宿主上的全部失败模式。错误均为哨兵值，便于调用方精确判定。
var (
	// ErrUnknownTenant 表示票据或打开请求引用了宿主未登记的租户。
	ErrUnknownTenant = errors.New("taskpkg: unknown tenant")
	// ErrUnknownObject 表示对象 ID 在当前宿主中不存在。
	ErrUnknownObject = errors.New("taskpkg: unknown object")
	// ErrAccessDenied 表示句柄不具备该操作所需的读或写权限。
	// 锁的持有状态永远不会绕过这一检查。
	ErrAccessDenied = errors.New("taskpkg: access denied")
	// ErrStaleCapability 表示句柄绑定的能力代次与对象当前代次不一致。
	// 对象替换或跨租户移交都会推进代次，旧句柄随即失效。
	ErrStaleCapability = errors.New("taskpkg: stale capability generation")
	// ErrWrongTenant 表示句柄绑定的租户与对象当前归属租户不一致。
	ErrWrongTenant = errors.New("taskpkg: tenant mismatch")
	// ErrLockConflict 表示请求的锁与已持有的锁不兼容。
	ErrLockConflict = errors.New("taskpkg: lock conflict")
	// ErrNotLocked 表示调用方在没有持有必需锁的情况下执行受锁保护的写操作。
	ErrNotLocked = errors.New("taskpkg: required lock not held")
	// ErrInvalidTicket 表示目标绑定票据结构不完整或无法解析。
	ErrInvalidTicket = errors.New("taskpkg: invalid handoff ticket")
	// ErrSignature 表示票据上的源租户或目标租户签名校验失败。
	ErrSignature = errors.New("taskpkg: ticket signature verification failed")
	// ErrTicketReplay 表示同一票据被重复用于移交。
	ErrTicketReplay = errors.New("taskpkg: handoff ticket replay")
	// ErrTicketExpired 表示票据已超过宿主允许的有效期。
	ErrTicketExpired = errors.New("taskpkg: handoff ticket expired")
)
