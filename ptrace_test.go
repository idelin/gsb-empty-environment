package netsec

import (
	"fmt"
	"testing"
)

func assertDecision(t *testing.T, got Decision, wantAllowed bool, wantReason Reason) {
	t.Helper()
	if got.Allowed != wantAllowed || got.Reason != wantReason {
		t.Fatalf("decision = allowed:%v reason:%s, want allowed:%v reason:%s",
			got.Allowed, got.Reason, wantAllowed, wantReason)
	}
}

func TestScopeClassicSameUID(t *testing.T) {
	tree := newTestTree(t)
	auth := NewAuthorizer(tree, ScopeClassic)

	// scope 0：同 uid 即可，兄弟分支也允许（浏览器场景）。
	assertDecision(t, auth.CanAttach(101, 104), true, ReasonNone)
	assertDecision(t, auth.CanAttach(101, 102), true, ReasonNone)

	// 不同 uid 且无能力：基本检查失败，Yama 还没介入。
	assertDecision(t, auth.CanAttach(101, 106), false, ReasonCredential)

	// 不同 uid 但持有 CAP_SYS_PTRACE：基本检查通过。
	mustOK(t, tree.ForkAs(100, 110, 1000, NewCapSet(CAP_SYS_PTRACE)))
	assertDecision(t, auth.CanAttach(110, 106), true, ReasonNone)
}

func TestScopeRestrictedDescendant(t *testing.T) {
	tree := newTestTree(t)
	auth := NewAuthorizer(tree, ScopeRestricted)

	// 后代（子、孙）允许。
	assertDecision(t, auth.CanAttach(101, 102), true, ReasonNone)
	assertDecision(t, auth.CanAttach(101, 103), true, ReasonNone)

	// 同 uid 的兄弟进程不是后代，默认拒绝，这正是 Yama 防护浏览器/ssh-agent 的点。
	assertDecision(t, auth.CanAttach(101, 104), false, ReasonNotDescendant)

	// 反向（晚辈附着长辈）同样不允许。
	assertDecision(t, auth.CanAttach(102, 101), false, ReasonNotDescendant)

	// 自身附着自身不算后代。
	assertDecision(t, auth.CanAttach(101, 101), false, ReasonNotDescendant)
}

func TestScopeRestrictedExplicitPtracer(t *testing.T) {
	tree := newTestTree(t)
	auth := NewAuthorizer(tree, ScopeRestricted)

	// 目标显式指定攻击者为 ptracer。
	mustOK(t, tree.SetPtracer(104, 101))
	assertDecision(t, auth.CanAttach(101, 104), true, ReasonNone)

	// 其他未被指定的同 uid 进程仍被拒绝。
	assertDecision(t, auth.CanAttach(102, 104), false, ReasonPtracerMismatch)

	// 撤销授权后恢复拒绝。
	mustOK(t, tree.ClearPtracer(104))
	assertDecision(t, auth.CanAttach(101, 104), false, ReasonNotDescendant)

	// PR_SET_PTRACER_ANY：任意同 uid 调用者都可附着。
	mustOK(t, tree.SetPtracer(104, PtracerAny))
	assertDecision(t, auth.CanAttach(102, 104), true, ReasonNone)

	// ptracer 授权不能跨越基本 uid 检查。
	assertDecision(t, auth.CanAttach(106, 104), false, ReasonCredential)
}

func TestScopeRestrictedCapabilityBypass(t *testing.T) {
	tree := newTestTree(t)
	auth := NewAuthorizer(tree, ScopeRestricted)

	// 给攻击者授予 CAP_SYS_PTRACE，scope 1 的关系限制被绕过。
	mustOK(t, tree.ForkAs(100, 120, 1000, NewCapSet(CAP_SYS_PTRACE)))
	assertDecision(t, auth.CanAttach(120, 104), true, ReasonNone)

	// 能力同时满足跨 uid 的基本检查。
	assertDecision(t, auth.CanAttach(120, 106), true, ReasonNone)
}

func TestScopeRestrictedOrphanReparent(t *testing.T) {
	tree := newTestTree(t)
	auth := NewAuthorizer(tree, ScopeRestricted)

	// 退出前攻击者可附着自己的后代。
	assertDecision(t, auth.CanAttach(101, 103), true, ReasonNone)

	// 攻击者退出，102 重挂 init；若 101 重新出现也不再是其祖先。
	mustOK(t, tree.Exit(101))

	// 攻击者已不存在：直接报调用者缺失。
	assertDecision(t, auth.CanAttach(101, 103), false, ReasonCallerMissing)

	// 新攻击者与孤儿 103 同在 init 下，互为兄弟，不是后代。
	mustOK(t, tree.ForkAs(100, 130, 1000, nil))
	assertDecision(t, auth.CanAttach(130, 103), false, ReasonNotDescendant)

	// init 作为树根，是重挂后孤儿的祖先。
	assertDecision(t, auth.CanAttach(100, 103), true, ReasonNone)
}

func TestPtracerSurvivesReparenting(t *testing.T) {
	tree := newTestTree(t)
	auth := NewAuthorizer(tree, ScopeRestricted)

	// 浏览器 104 显式授权攻击者 101，随后 104 的父进程 init 不会退出，
	// 这里改为让 104 自己退出前先验证其授权随目标退出而消失；
	// 另构造一对父子，验证子进程被重挂后其 ptracer 授权依旧生效。
	mustOK(t, tree.ForkAs(101, 170, 1000, nil))
	mustOK(t, tree.SetPtracer(170, 104))

	// 101 退出导致 170 重挂 init，但 170 对 104 的授权是其自身属性，不受影响。
	mustOK(t, tree.Exit(101))
	reparented, err := tree.Lookup(170)
	mustOK(t, err)
	if reparented.PPID != 100 {
		t.Fatalf("pid 170 ppid = %d, want init 100", reparented.PPID)
	}
	assertDecision(t, auth.CanAttach(104, 170), true, ReasonNone)

	// 未被授权的兄弟进程仍然不能附着。
	assertDecision(t, auth.CanAttach(105, 170), false, ReasonPtracerMismatch)
}

func TestScopeAdminOnlyRequiresCapability(t *testing.T) {
	tree := newTestTree(t)
	auth := NewAuthorizer(tree, ScopeAdminOnly)

	// 即使是同 uid 父子关系，没有 CAP_SYS_PTRACE 也拒绝。
	assertDecision(t, auth.CanAttach(101, 102), false, ReasonMissingCap)
	assertDecision(t, auth.CanAttach(101, 104), false, ReasonMissingCap)

	// 后代关系 + 能力才放行。
	mustOK(t, tree.ForkAs(100, 140, 1000, NewCapSet(CAP_SYS_PTRACE)))
	assertDecision(t, auth.CanAttach(140, 101), true, ReasonNone)

	// 能力也覆盖跨 uid 目标。
	assertDecision(t, auth.CanAttach(140, 106), true, ReasonNone)
}

func TestScopeNoAttachDeniesEverything(t *testing.T) {
	tree := newTestTree(t)
	auth := NewAuthorizer(tree, ScopeNoAttach)

	assertDecision(t, auth.CanAttach(101, 102), false, ReasonNoAttach)
	assertDecision(t, auth.CanAttach(101, 104), false, ReasonNoAttach)

	// CAP_SYS_PTRACE 也无法绕过 scope 3。
	mustOK(t, tree.ForkAs(100, 150, 1000, NewCapSet(CAP_SYS_PTRACE)))
	assertDecision(t, auth.CanAttach(150, 106), false, ReasonNoAttach)
}

func TestTraceMeNotRestrictedByScopeOne(t *testing.T) {
	tree := newTestTree(t)

	// 构造非同 uid 的“父子”：父 101(uid 1000)，子 160(uid 1002)。
	mustOK(t, tree.ForkAs(101, 160, 1002, nil))

	// scope 1：TRACEME 不受关系限制，但基本 uid/能力检查仍然生效。
	restricted := NewAuthorizer(tree, ScopeRestricted)
	assertDecision(t, restricted.CanTraceMe(101, 160), false, ReasonCredential)

	// 同 uid 的 TRACEME 在 scope 1 直接放行，即便两者并非祖先关系模型中的
	// 附着方向（这里就是真实父子）。
	assertDecision(t, restricted.CanTraceMe(101, 102), true, ReasonNone)

	// scope 2：TRACEME 仍要求 CAP_SYS_PTRACE。
	adminOnly := NewAuthorizer(tree, ScopeAdminOnly)
	assertDecision(t, adminOnly.CanTraceMe(101, 102), false, ReasonMissingCap)

	// scope 3：TRACEME 同样被禁止。
	noAttach := NewAuthorizer(tree, ScopeNoAttach)
	assertDecision(t, noAttach.CanTraceMe(101, 102), false, ReasonNoAttach)

	// scope 0：同 uid 放行。
	classic := NewAuthorizer(tree, ScopeClassic)
	assertDecision(t, classic.CanTraceMe(101, 102), true, ReasonNone)
}

func TestAuthorizeMissingProcesses(t *testing.T) {
	tree := newTestTree(t)
	auth := NewAuthorizer(tree, ScopeRestricted)

	assertDecision(t, auth.CanAttach(999, 102), false, ReasonCallerMissing)
	assertDecision(t, auth.CanAttach(101, 999), false, ReasonTargetMissing)

	mustOK(t, tree.Exit(104))
	assertDecision(t, auth.CanAttach(101, 104), false, ReasonTargetMissing)
}

func TestInvalidScope(t *testing.T) {
	tree := newTestTree(t)
	auth := NewAuthorizer(tree, Scope(7))

	if auth.Scope().Valid() {
		t.Fatal("scope 7 must be invalid")
	}
	assertDecision(t, auth.CanAttach(101, 102), false, ReasonScopeInvalid)
}

func TestWithScopeAndPackageHelper(t *testing.T) {
	tree := newTestTree(t)
	base := NewAuthorizer(tree, ScopeClassic)

	// scope 0 允许兄弟附着，切换到 scope 1 后拒绝。
	assertDecision(t, base.CanAttach(101, 104), true, ReasonNone)
	restricted := base.WithScope(ScopeRestricted)
	if restricted.Scope() != ScopeRestricted || base.Scope() != ScopeClassic {
		t.Fatal("WithScope must not mutate the original authorizer")
	}
	assertDecision(t, restricted.CanAttach(101, 104), false, ReasonNotDescendant)

	// 包级便捷函数与 Authorizer 行为一致。
	got := CanPtrace(tree, ScopeClassic, 101, 104)
	assertDecision(t, got, true, ReasonNone)
}

func TestStringers(t *testing.T) {
	cases := map[fmt.Stringer]string{
		ScopeClassic:     "YAMA_SCOPE_CLASSIC(0)",
		ScopeNoAttach:    "YAMA_SCOPE_NO_ATTACH(3)",
		OpAttach:         "PTRACE_ATTACH",
		OpTraceMe:        "PTRACE_TRACEME",
		ReasonNoAttach:   "ptrace is fully disabled (scope 3)",
		ReasonCredential: "neither same uid nor CAP_SYS_PTRACE",
	}
	for value, want := range cases {
		if got := value.String(); got != want {
			t.Fatalf("%v.String() = %q, want %q", value, got, want)
		}
	}
}
