package netsec

import (
	"errors"
	"testing"
)

func TestCapSetMutators(t *testing.T) {
	set := NewCapSet()
	if set.Has(CAP_SYS_PTRACE) {
		t.Fatal("empty set should not contain capability")
	}
	set.Add(CAP_SYS_PTRACE)
	if !set.Has(CAP_SYS_PTRACE) {
		t.Fatal("Add did not record capability")
	}
	set.Drop(CAP_SYS_PTRACE)
	if set.Has(CAP_SYS_PTRACE) {
		t.Fatal("Drop did not remove capability")
	}

	var nilSet CapSet
	if nilSet.Has(CAP_SYS_PTRACE) {
		t.Fatal("nil set must report no capability")
	}
}

func TestTreeAccessors(t *testing.T) {
	tree := newTestTree(t)
	if tree.InitPID() != 100 {
		t.Fatalf("init pid = %d, want 100", tree.InitPID())
	}

	parent, err := tree.Parent(103)
	mustOK(t, err)
	if parent.PID != 102 {
		t.Fatalf("parent of 103 = %d, want 102", parent.PID)
	}

	if _, err := tree.Parent(999); !errors.Is(err, ErrNoSuchProcess) {
		t.Fatalf("parent of missing pid: got %v, want ErrNoSuchProcess", err)
	}

	// Add 的成功路径。
	mustOK(t, tree.Add(180, 100, 1000, NewCapSet()))
	if !tree.IsDescendant(100, 180) {
		t.Fatal("newly added process should be descendant of init")
	}
}

func TestAuthorizerTreeAccessor(t *testing.T) {
	tree := newTestTree(t)
	auth := NewAuthorizer(tree, ScopeRestricted)
	if auth.Tree() != tree {
		t.Fatal("Tree() must return the bound process tree")
	}
}

func TestUnknownStringers(t *testing.T) {
	if got := Scope(9).String(); got != "YAMA_SCOPE_UNKNOWN(9)" {
		t.Fatalf("scope string = %q", got)
	}
	if got := PtraceOp(9).String(); got != "PTRACE_UNKNOWN(9)" {
		t.Fatalf("op string = %q", got)
	}
	if got := Reason(9).String(); got != "unknown reason(9)" {
		t.Fatalf("reason string = %q", got)
	}

	// 触发各枚举的全部分支，防止后续重命名时漏掉常量。
	scopes := []Scope{ScopeClassic, ScopeRestricted, ScopeAdminOnly, ScopeNoAttach}
	for _, scope := range scopes {
		if !scope.Valid() {
			t.Fatalf("scope %d should be valid", scope)
		}
		_ = scope.String()
	}
	ops := []PtraceOp{OpAttach, OpTraceMe}
	for _, op := range ops {
		_ = op.String()
	}
	reasons := []Reason{
		ReasonNone, ReasonCallerMissing, ReasonTargetMissing, ReasonCredential,
		ReasonScopeInvalid, ReasonNotDescendant, ReasonPtracerMismatch,
		ReasonMissingCap, ReasonNoAttach,
	}
	for _, reason := range reasons {
		_ = reason.String()
	}
}

func TestErrorPaths(t *testing.T) {
	tree := newTestTree(t)

	// Add 重复 PID。
	assertErrorIs(t, tree.Add(101, 100, 1000, nil), ErrProcessExists)

	// ForkAs 的两类非法输入。
	assertErrorIs(t, tree.ForkAs(999, 190, 1000, nil), ErrNoSuchProcess)
	mustOK(t, tree.ForkAs(100, 190, 1000, nil))
	assertErrorIs(t, tree.ForkAs(100, 190, 1000, nil), ErrProcessExists)

	// ClearPtracer / HasPtracer 对未知进程。
	assertErrorIs(t, tree.ClearPtracer(999), ErrNoSuchProcess)
	_, _, err := tree.HasPtracer(999)
	assertErrorIs(t, err, ErrNoSuchProcess)

	// IsDescendant 中 next == cur 的防御分支：手工破坏不了内部结构，
	// 这里通过 init 自身与不存在节点验证边界返回。
	if tree.IsDescendant(100, 100) {
		t.Fatal("init must not be its own descendant")
	}

	// 未设置 ptracer 时 HasPtracer 返回 ok=false。
	if tracer, ok, err := tree.HasPtracer(190); err != nil || ok || tracer != 0 {
		t.Fatalf("unset ptracer = (%d, %v, %v)", tracer, ok, err)
	}
}
