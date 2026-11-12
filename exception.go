package netsec

import (
	"errors"
	"fmt"
)

// 依赖索引在参数非法或状态冲突时返回的哨兵错误。
var (
	ErrEmptyID             = errors.New("netsec: id must not be empty")
	ErrExceptionExists     = errors.New("netsec: exception already registered")
	ErrExceptionNotFound   = errors.New("netsec: exception not found")
	ErrResourceExists      = errors.New("netsec: resource already registered")
	ErrResourceNotFound    = errors.New("netsec: resource not found")
	ErrDependencyInactive  = errors.New("netsec: referenced exception is not active")
	ErrUpstreamInactive    = errors.New("netsec: upstream resource is restricted")
	ErrUpstreamNotFound    = errors.New("netsec: upstream resource not found")
	ErrDependencyCycle     = errors.New("netsec: dependency cycle detected")
	ErrResourceRestricted  = errors.New("netsec: resource is restricted")
	ErrOperationNotFound   = errors.New("netsec: sensitive operation not found")
	ErrOperationTerminated = errors.New("netsec: sensitive operation already terminated")
)

// ExceptionState 描述临时安全例外的生命周期状态。
type ExceptionState int

const (
	// ExceptionActive 表示例外有效，可作为资源依赖。
	ExceptionActive ExceptionState = iota
	// ExceptionRevoked 表示例外被主动撤销。
	ExceptionRevoked
	// ExceptionExpired 表示例外到达到期时间而失效。
	ExceptionExpired
)

// String 返回状态的可读名称。
func (s ExceptionState) String() string {
	switch s {
	case ExceptionActive:
		return "active"
	case ExceptionRevoked:
		return "revoked"
	case ExceptionExpired:
		return "expired"
	default:
		return fmt.Sprintf("state(%d)", int(s))
	}
}

// InvalidationReason 记录例外失效的触发原因。
type InvalidationReason int

const (
	// ReasonRevoked 由显式撤销触发。
	ReasonRevoked InvalidationReason = iota
	// ReasonExpired 由到期扫描触发。
	ReasonExpired
)

// String 返回失效原因的可读名称。
func (r InvalidationReason) String() string {
	switch r {
	case ReasonRevoked:
		return "revoked"
	case ReasonExpired:
		return "expired"
	default:
		return fmt.Sprintf("reason(%d)", int(r))
	}
}

// TerminationPolicy 定义依赖链受限后，已经开始（in-flight）的
// 敏感操作如何终止。它挂在例外上，随失效沿依赖链传播。
type TerminationPolicy int

const (
	// TerminateUnset 是零值，表示资源当前未受限，没有生效中的终止策略。
	TerminateUnset TerminationPolicy = iota
	// TerminateAbort 立即中止在途操作：操作被标记为 aborted，
	// 不得再产生任何副作用。
	TerminateAbort
	// TerminateDrain 允许在途操作优雅排空：禁止新操作，
	// 已开始的操作只能显式完成或失败，不能继续扩张。
	TerminateDrain
)

// String 返回终止策略的可读名称。
func (p TerminationPolicy) String() string {
	switch p {
	case TerminateUnset:
		return "unset"
	case TerminateAbort:
		return "abort"
	case TerminateDrain:
		return "drain"
	default:
		return fmt.Sprintf("policy(%d)", int(p))
	}
}

// ExceptionVersion 唯一标识某条例外的一个世代。
// 例外重建后 ID 可复用，但 Generation 递增，旧依赖不会自动
// 绑定到新世代。
type ExceptionVersion struct {
	ID         string
	Generation int
}

// String 返回 例外ID#世代 的紧凑表示。
func (v ExceptionVersion) String() string {
	return fmt.Sprintf("%s#g%d", v.ID, v.Generation)
}

// InvalidationEvent 保留例外失效的审计记录（墓碑）。
// 根记录不会被删除，资源仍可通过它解释受限原因。
type InvalidationEvent struct {
	Version ExceptionVersion
	Reason  InvalidationReason
	At      Time
	Note    string
}

// Exception 是临时安全例外的元数据。
type Exception struct {
	ID         string
	Generation int
	// ExpiresAt 为零值表示不过期，由 SweepExpired 统一扫描。
	ExpiresAt Time
	// Policy 决定失效时在途操作的终止方式。
	Policy TerminationPolicy

	state     ExceptionState
	invalidAt Time
}

// Version 返回该例外当前世代的版本标识。
func (e *Exception) Version() ExceptionVersion {
	return ExceptionVersion{ID: e.ID, Generation: e.Generation}
}

// State 返回当前生命周期状态。
func (e *Exception) State() ExceptionState { return e.state }

// Active 报告例外是否仍可支撑敏感操作。
func (e *Exception) Active() bool { return e.state == ExceptionActive }

// Invalidation 返回失效事件；仍有效时返回 nil。
func (e *Exception) Invalidation() *InvalidationEvent {
	if e.Active() {
		return nil
	}
	reason := ReasonExpired
	if e.state == ExceptionRevoked {
		reason = ReasonRevoked
	}
	return &InvalidationEvent{
		Version: e.Version(),
		Reason:  reason,
		At:      e.invalidAt,
	}
}
