package netsec

import "fmt"

// Scope 对应 /proc/sys/kernel/yama/ptrace_scope 的四个取值。
type Scope int

const (
	// ScopeClassic（0）：经典 ptrace 权限，同 uid 进程之间可互相调试。
	ScopeClassic Scope = 0
	// ScopeRestricted（1）：受限 ptrace，仅允许后代或经 PR_SET_PTRACER
	// 显式授权的调用者附着；持有 CAP_SYS_PTRACE 可绕过。
	ScopeRestricted Scope = 1
	// ScopeAdminOnly（2）：仅持有 CAP_SYS_PTRACE 的进程可 ptrace。
	ScopeAdminOnly Scope = 2
	// ScopeNoAttach（3）：一律禁止 ptrace，任何能力都无法绕过。
	ScopeNoAttach Scope = 3
)

// String 返回 scope 的可读名称。
func (scope Scope) String() string {
	switch scope {
	case ScopeClassic:
		return "YAMA_SCOPE_CLASSIC(0)"
	case ScopeRestricted:
		return "YAMA_SCOPE_RESTRICTED(1)"
	case ScopeAdminOnly:
		return "YAMA_SCOPE_ADMIN_ONLY(2)"
	case ScopeNoAttach:
		return "YAMA_SCOPE_NO_ATTACH(3)"
	default:
		return fmt.Sprintf("YAMA_SCOPE_UNKNOWN(%d)", int(scope))
	}
}

// Valid 报告 scope 是否为受支持的四个取值之一。
func (scope Scope) Valid() bool {
	return scope >= ScopeClassic && scope <= ScopeNoAttach
}

// PtraceOp 区分 ptrace 请求的方向。
type PtraceOp int

const (
	// OpAttach 对应 PTRACE_ATTACH / PTRACE_SEIZE：调用者主动附着目标。
	OpAttach PtraceOp = iota
	// OpTraceMe 对应 PTRACE_TRACEME：目标进程声明让其父进程跟踪自己。
	OpTraceMe
)

// String 返回操作的可读名称。
func (op PtraceOp) String() string {
	switch op {
	case OpAttach:
		return "PTRACE_ATTACH"
	case OpTraceMe:
		return "PTRACE_TRACEME"
	default:
		return fmt.Sprintf("PTRACE_UNKNOWN(%d)", int(op))
	}
}

// PtraceRequest 描述一次待授权的 ptrace 调用。
type PtraceRequest struct {
	// Op 是请求方向：主动附着或子进程 TRACEME。
	Op PtraceOp
	// CallerPID 是发起 ptrace 的进程；TRACEME 时为目标进程的父进程。
	CallerPID int
	// TargetPID 是被附着的目标；TRACEME 时为发起 TRACEME 的子进程。
	TargetPID int
}

// Reason 标识一次 ptrace 请求被拒绝的具体原因，便于审计与测试断言。
type Reason int

const (
	// ReasonNone 表示请求被允许。
	ReasonNone Reason = iota
	// ReasonCallerMissing 表示调用者进程不存在（已退出）。
	ReasonCallerMissing
	// ReasonTargetMissing 表示目标进程不存在（已退出）。
	ReasonTargetMissing
	// ReasonCredential 表示未通过 uid 与能力的基本权限检查。
	ReasonCredential
	// ReasonScopeInvalid 表示配置的 ptrace_scope 不在 0..3 范围内。
	ReasonScopeInvalid
	// ReasonNotDescendant 表示 scope 1 下目标不是调用者的后代。
	ReasonNotDescendant
	// ReasonPtracerMismatch 表示 scope 1 下目标未用 PR_SET_PTRACER
	// 指定该调用者（或任意进程）。
	ReasonPtracerMismatch
	// ReasonMissingCap 表示 scope 2 下调用者缺少 CAP_SYS_PTRACE。
	ReasonMissingCap
	// ReasonNoAttach 表示 scope 3 一律禁止。
	ReasonNoAttach
)

// String 返回拒绝原因的可读名称。
func (reason Reason) String() string {
	switch reason {
	case ReasonNone:
		return "allowed"
	case ReasonCallerMissing:
		return "caller process does not exist"
	case ReasonTargetMissing:
		return "target process does not exist"
	case ReasonCredential:
		return "neither same uid nor CAP_SYS_PTRACE"
	case ReasonScopeInvalid:
		return "invalid ptrace_scope"
	case ReasonNotDescendant:
		return "target is not a descendant of the caller"
	case ReasonPtracerMismatch:
		return "target did not PR_SET_PTRACER the caller"
	case ReasonMissingCap:
		return "caller lacks CAP_SYS_PTRACE"
	case ReasonNoAttach:
		return "ptrace is fully disabled (scope 3)"
	default:
		return fmt.Sprintf("unknown reason(%d)", int(reason))
	}
}

// Decision 是一次 ptrace 授权判定的结果。
type Decision struct {
	// Allowed 报告请求是否放行。
	Allowed bool
	// Reason 在拒绝时给出原因；放行时为 ReasonNone。
	Reason Reason
	// Scope 是本次判定使用的 Yama scope。
	Scope Scope
}

// Authorizer 在给定进程树上按 Yama ptrace_scope 模拟 ptrace 授权。
type Authorizer struct {
	tree  *ProcessTree
	scope Scope
}

// NewAuthorizer 创建绑定到进程树与 scope 的授权器。
func NewAuthorizer(tree *ProcessTree, scope Scope) *Authorizer {
	return &Authorizer{tree: tree, scope: scope}
}

// Tree 返回授权器绑定的进程树。
func (authorizer *Authorizer) Tree() *ProcessTree {
	return authorizer.tree
}

// Scope 返回授权器当前使用的 Yama scope。
func (authorizer *Authorizer) Scope() Scope {
	return authorizer.scope
}

// WithScope 返回使用同一进程树、但切换到新 scope 的授权器，方便对同一
// 进程树依次验证各级 scope。
func (authorizer *Authorizer) WithScope(scope Scope) *Authorizer {
	return &Authorizer{tree: authorizer.tree, scope: scope}
}

// Authorize 按内核 Yama 语义对一次 ptrace 请求做出授权判定。
//
// 判定顺序：
//  1. 进程存在性检查；
//  2. uid 与能力的基本检查（同 uid 或调用者持有 CAP_SYS_PTRACE）；
//  3. 按 scope 0..3 施加 Yama 限制。
//
// PTRACE_TRACEME 在 scope 1 下不受“目标须为后代”的关系限制；
// scope 2 仍要求 CAP_SYS_PTRACE，scope 3 仍一律禁止。
func (authorizer *Authorizer) Authorize(request PtraceRequest) Decision {
	if !authorizer.scope.Valid() {
		return Decision{Allowed: false, Reason: ReasonScopeInvalid, Scope: authorizer.scope}
	}

	caller, err := authorizer.tree.Lookup(request.CallerPID)
	if err != nil {
		return Decision{Allowed: false, Reason: ReasonCallerMissing, Scope: authorizer.scope}
	}
	target, err := authorizer.tree.Lookup(request.TargetPID)
	if err != nil {
		return Decision{Allowed: false, Reason: ReasonTargetMissing, Scope: authorizer.scope}
	}

	// 基本权限检查：同 uid，或调用者具备 CAP_SYS_PTRACE。
	// 对应内核 ptrace_has_cap / 同 uid 判定（PTRACE_MODE_ATTACH_REALCREDS）。
	if caller.UID != target.UID && !caller.Caps.Has(CAP_SYS_PTRACE) {
		return Decision{Allowed: false, Reason: ReasonCredential, Scope: authorizer.scope}
	}

	switch authorizer.scope {
	case ScopeClassic:
		// scope 0：通过基本检查即可。
		return authorizer.allow()
	case ScopeRestricted:
		return authorizer.authorizeRestricted(request, caller, target)
	case ScopeAdminOnly:
		// scope 2：基本检查里的同 uid 分支不再够用，必须持有能力。
		if !caller.Caps.Has(CAP_SYS_PTRACE) {
			return authorizer.deny(ReasonMissingCap)
		}
		return authorizer.allow()
	case ScopeNoAttach:
		// scope 3：任何请求、任何能力都禁止。
		return authorizer.deny(ReasonNoAttach)
	default:
		return authorizer.deny(ReasonScopeInvalid)
	}
}

// CanAttach 是 Authorize 在 PTRACE_ATTACH 场景下的便捷封装。
func (authorizer *Authorizer) CanAttach(callerPID, targetPID int) Decision {
	return authorizer.Authorize(PtraceRequest{
		Op:        OpAttach,
		CallerPID: callerPID,
		TargetPID: targetPID,
	})
}

// CanTraceMe 是 Authorize 在 PTRACE_TRACEME 场景下的便捷封装。
// callerPID 为将跟踪子进程的父进程，targetPID 为发起 TRACEME 的子进程。
func (authorizer *Authorizer) CanTraceMe(callerPID, targetPID int) Decision {
	return authorizer.Authorize(PtraceRequest{
		Op:        OpTraceMe,
		CallerPID: callerPID,
		TargetPID: targetPID,
	})
}

func (authorizer *Authorizer) authorizeRestricted(request PtraceRequest, caller, target Process) Decision {
	// 持有 CAP_SYS_PTRACE 可直接绕过 scope 1 的关系限制。
	if caller.Caps.Has(CAP_SYS_PTRACE) {
		return authorizer.allow()
	}

	// PTRACE_TRACEME 不受 scope 1 限制：子进程本就由其父 ptrace，
	// 内核在该路径上跳过 Yama 的后代/ptracer 关系检查。
	if request.Op == OpTraceMe {
		return authorizer.allow()
	}

	// 关系限制一：目标必须是调用者的严格后代。
	if authorizer.tree.IsDescendant(caller.PID, target.PID) {
		return authorizer.allow()
	}

	// 关系限制二：目标通过 PR_SET_PTRACER 指定了该调用者，或指定任意进程。
	tracer, ok, err := authorizer.tree.HasPtracer(target.PID)
	if err == nil && ok && (tracer == PtracerAny || tracer == caller.PID) {
		return authorizer.allow()
	}

	// 目标设置过 ptracer 但指向别的进程时，给出更精确的不匹配原因；
	// 从未设置授权则属于后代关系检查失败。
	if err == nil && ok {
		return authorizer.deny(ReasonPtracerMismatch)
	}
	return authorizer.deny(ReasonNotDescendant)
}

func (authorizer *Authorizer) allow() Decision {
	return Decision{Allowed: true, Reason: ReasonNone, Scope: authorizer.scope}
}

func (authorizer *Authorizer) deny(reason Reason) Decision {
	return Decision{Allowed: false, Reason: reason, Scope: authorizer.scope}
}

// CanPtrace 是包级便捷函数，在给定进程树与 scope 下判定一次附着请求。
func CanPtrace(tree *ProcessTree, scope Scope, callerPID, targetPID int) Decision {
	return NewAuthorizer(tree, scope).CanAttach(callerPID, targetPID)
}
