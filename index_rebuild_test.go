package netsec

import (
	"errors"
	"testing"
)

// drain 先行、abort 后至：策略只能升级，running 操作随后被强制中止。
func TestPolicyEscalationDrainToAbort(t *testing.T) {
	idx := NewDependencyIndex(NewVirtualClock(0))

	drainExc, err := idx.RegisterException("D", 0, TerminateDrain)
	mustOK(t, err)
	abortExc, err := idx.RegisterException("X", 0, TerminateAbort)
	mustOK(t, err)

	_, err = idx.RegisterResource("r",
		[]ExceptionVersion{drainExc.Version(), abortExc.Version()}, nil)
	mustOK(t, err)
	_, err = idx.BeginSensitiveOperation("r", "op")
	mustOK(t, err)

	_, err = idx.Revoke("D", "")
	mustOK(t, err)
	res := idx.Resource("r")
	if !res.Restricted || res.TerminationPolicy != TerminateDrain {
		t.Fatalf("want restricted drain, got %+v", res)
	}
	if idx.Operation("op").State() != OpRunning {
		t.Fatal("drain must keep in-flight op running")
	}

	_, err = idx.Revoke("X", "")
	mustOK(t, err)
	res = idx.Resource("r")
	if res.TerminationPolicy != TerminateAbort {
		t.Fatalf("policy must escalate to abort, got %v", res.TerminationPolicy)
	}
	op := idx.Operation("op")
	if op.State() != OpAborted || op.AbortNote() == "" {
		t.Fatalf("op must be aborted on escalation, got %+v", op)
	}
}

// 新世代例外重建：旧依赖继续受限，必须显式 Rebase 才能恢复，
// 且新世代可独立重复失效。
func TestNewGenerationRebuild(t *testing.T) {
	clock := NewVirtualClock(0)
	idx := NewDependencyIndex(clock)

	g1, err := idx.RegisterException("E", Time(1000), TerminateDrain)
	mustOK(t, err)
	_, err = idx.RegisterResource("r", []ExceptionVersion{g1.Version()}, nil)
	mustOK(t, err)
	op1, err := idx.BeginSensitiveOperation("r", "op1")
	mustOK(t, err)

	clock.Set(1000)
	idx.SweepExpired()
	mustOK(t, idx.FailOperation(op1.ID))

	// 不允许在失效世代上建立新资源。
	_, err = idx.RegisterResource("new", []ExceptionVersion{g1.Version()}, nil)
	wantErr(t, err, ErrDependencyInactive)

	// 同 ID 注册新世代，旧资源不会自动迁移。
	g2, err := idx.RegisterException("E", Time(2000), TerminateAbort)
	mustOK(t, err)
	if g2.Generation != 2 {
		t.Fatalf("want generation 2, got %d", g2.Generation)
	}
	if res := idx.Resource("r"); !res.Restricted {
		t.Fatal("resource pinned to g1 must stay restricted after g2 registration")
	}
	if _, err := idx.BeginSensitiveOperation("r", "op2"); !errors.Is(err, ErrResourceRestricted) {
		t.Fatalf("want restricted begin error, got %v", err)
	}

	// 显式 Rebase 到新世代后恢复，可重新开始敏感操作。
	res, err := idx.RebaseResource("r", []ExceptionVersion{g2.Version()}, nil)
	mustOK(t, err)
	if res.Restricted || len(res.Causes) != 0 || res.TerminationPolicy != TerminateUnset {
		t.Fatalf("rebased resource must be unrestricted, got %+v", res)
	}
	op2, err := idx.BeginSensitiveOperation("r", "op2")
	mustOK(t, err)

	// 新世代再次到期，独立失效并按新策略 abort。
	clock.Set(2000)
	events := idx.SweepExpired()
	if len(events) != 1 || events[0].Version != g2.Version() {
		t.Fatalf("want g2 expiry event, got %v", events)
	}
	if op := idx.Operation(op2.ID); op.State() != OpAborted {
		t.Fatalf("g2 abort policy must abort op2, got %v", op.State())
	}

	// 两个世代墓碑都保留，可分别解释。
	old := idx.ExceptionGeneration(g1.Version())
	cur := idx.ExceptionGeneration(g2.Version())
	if old.State() != ExceptionExpired || cur.State() != ExceptionExpired {
		t.Fatalf("both generations must be retained, old=%+v cur=%+v", old, cur)
	}

	g3, err := idx.RegisterException("E", 0, TerminateDrain)
	mustOK(t, err)
	if g3.Generation != 3 {
		t.Fatalf("want generation 3, got %d", g3.Generation)
	}
	if len(idx.Events()) != 2 {
		t.Fatalf("want 2 tombstones across generations, got %d", len(idx.Events()))
	}
}

// 多例外资源：仅撤销其中一个即受限，Rebase 摘除失效依赖后恢复。
func TestPartialDependencyRebase(t *testing.T) {
	idx := NewDependencyIndex(NewVirtualClock(0))

	e1, err := idx.RegisterException("E1", 0, TerminateDrain)
	mustOK(t, err)
	e2, err := idx.RegisterException("E2", 0, TerminateDrain)
	mustOK(t, err)
	_, err = idx.RegisterResource("r",
		[]ExceptionVersion{e1.Version(), e2.Version()}, nil)
	mustOK(t, err)

	_, err = idx.Revoke("E1", "")
	mustOK(t, err)
	res := idx.Resource("r")
	if !res.Restricted {
		t.Fatal("resource must be restricted when one dependency dies")
	}
	wantVersions(t, res.Causes, e1.Version())

	res, err = idx.RebaseResource("r", []ExceptionVersion{e2.Version()}, nil)
	mustOK(t, err)
	if res.Restricted || res.TerminationPolicy != TerminateUnset {
		t.Fatalf("resource must recover after dropping dead dependency, got %+v", res)
	}
	if _, err := idx.BeginSensitiveOperation("r", "op"); err != nil {
		t.Fatalf("recovered resource should accept new ops, got %v", err)
	}
}

// 注册期校验：重复 ID、缺失/受限上游、成环等必须被拒绝。
func TestRegistrationValidation(t *testing.T) {
	idx := NewDependencyIndex(NewVirtualClock(0))

	_, err := idx.RegisterException("", 0, TerminateDrain)
	wantErr(t, err, ErrEmptyID)

	exc, err := idx.RegisterException("E", 0, TerminateDrain)
	mustOK(t, err)
	_, err = idx.RegisterException("E", 0, TerminateDrain)
	wantErr(t, err, ErrExceptionExists)

	_, err = idx.RegisterResource("r", []ExceptionVersion{{ID: "nope", Generation: 1}}, nil)
	wantErr(t, err, ErrExceptionNotFound)

	_, err = idx.RegisterResource("r", []ExceptionVersion{exc.Version()}, nil)
	mustOK(t, err)
	_, err = idx.RegisterResource("r", nil, nil)
	wantErr(t, err, ErrResourceExists)

	_, err = idx.RegisterResource("down", nil, []string{"ghost"})
	wantErr(t, err, ErrUpstreamNotFound)

	_, err = idx.RebaseResource("r", nil, []string{"r"})
	wantErr(t, err, ErrDependencyCycle)

	_, err = idx.RegisterResource("a", nil, []string{"r"})
	mustOK(t, err)
	_, err = idx.RegisterResource("b", nil, []string{"a"})
	mustOK(t, err)
	_, err = idx.RebaseResource("r", nil, []string{"b"})
	wantErr(t, err, ErrDependencyCycle)

	// 受限上游不能承载新的直接依赖边。
	_, err = idx.Revoke("E", "")
	mustOK(t, err)
	_, err = idx.RegisterResource("late", nil, []string{"r"})
	wantErr(t, err, ErrUpstreamInactive)

	_, err = idx.Revoke("ghost", "")
	wantErr(t, err, ErrExceptionNotFound)
	err = idx.CompleteOperation("ghost-op")
	wantErr(t, err, ErrOperationNotFound)
}

// 到期扫描对多个独立例外确定性排序并一次性全部失效。
func TestSweepMultipleDeterministic(t *testing.T) {
	clock := NewVirtualClock(0)
	idx := NewDependencyIndex(clock)

	ea, err := idx.RegisterException("EA", Time(50), TerminateAbort)
	mustOK(t, err)
	eb, err := idx.RegisterException("EB", Time(25), TerminateDrain)
	mustOK(t, err)
	_, err = idx.RegisterResource("ra", []ExceptionVersion{ea.Version()}, nil)
	mustOK(t, err)
	_, err = idx.RegisterResource("rb", []ExceptionVersion{eb.Version()}, nil)
	mustOK(t, err)

	clock.Set(100)
	events := idx.SweepExpired()
	if len(events) != 2 {
		t.Fatalf("want 2 expiry events, got %d", len(events))
	}
	if events[0].Version != eb.Version() || events[1].Version != ea.Version() {
		t.Fatalf("events not deterministically ordered: %v", events)
	}
	if idx.Resource("ra").TerminationPolicy != TerminateAbort {
		t.Fatal("ra should carry abort policy")
	}
	if idx.Resource("rb").TerminationPolicy != TerminateDrain {
		t.Fatal("rb should carry drain policy")
	}
	if again := idx.SweepExpired(); len(again) != 0 {
		t.Fatalf("second sweep must be empty, got %v", again)
	}
}
