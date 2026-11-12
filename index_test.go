package netsec

import (
	"errors"
	"testing"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantErr(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want error %v, got %v", target, err)
	}
}

func wantVersions(t *testing.T, got []ExceptionVersion, want ...ExceptionVersion) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("want versions %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("want versions %v, got %v", want, got)
		}
	}
}

// 虚拟时钟 + 到期沿链传播 + drain 策略下在途操作显式终止。
func TestVirtualClockExpiryChainDrain(t *testing.T) {
	clock := NewVirtualClock(100)
	idx := NewDependencyIndex(clock)

	exc, err := idx.RegisterException("E1", Time(1000), TerminateDrain)
	mustOK(t, err)
	v1 := exc.Version()

	_, err = idx.RegisterResource("r1", []ExceptionVersion{v1}, nil)
	mustOK(t, err)
	_, err = idx.RegisterResource("r2", nil, []string{"r1"})
	mustOK(t, err)
	_, err = idx.RegisterResource("r3", nil, []string{"r2"})
	mustOK(t, err)

	_, err = idx.BeginSensitiveOperation("r1", "op1")
	mustOK(t, err)
	_, err = idx.BeginSensitiveOperation("r2", "op2")
	mustOK(t, err)

	clock.Advance(899) // 999，尚未到期
	if events := idx.SweepExpired(); len(events) != 0 {
		t.Fatalf("expected no expiry at 999, got %v", events)
	}
	if idx.Resource("r3").Restricted {
		t.Fatal("r3 must stay active before expiry")
	}

	clock.Advance(1) // 1000，到期边界
	events := idx.SweepExpired()
	if len(events) != 1 {
		t.Fatalf("want 1 expiry event, got %d", len(events))
	}
	if events[0].Reason != ReasonExpired || events[0].Version != v1 || events[0].At != 1000 {
		t.Fatalf("unexpected event: %+v", events[0])
	}

	for _, id := range []string{"r1", "r2", "r3"} {
		res := idx.Resource(id)
		if !res.Restricted {
			t.Fatalf("%s must be restricted after chain expiry", id)
		}
		if res.TerminationPolicy != TerminateDrain {
			t.Fatalf("%s policy = %v, want drain", id, res.TerminationPolicy)
		}
		wantVersions(t, res.Causes, v1)
	}

	// drain：在途操作保持 running，但禁止新增敏感操作。
	if got := idx.Operation("op1"); got.State() != OpRunning {
		t.Fatalf("op1 state = %v, want running under drain", got.State())
	}
	if _, err := idx.BeginSensitiveOperation("r3", "opX"); !errors.Is(err, ErrResourceRestricted) {
		t.Fatalf("begin on restricted resource: want ErrResourceRestricted, got %v", err)
	}

	// 明确终止：完成/失败均落终态，重复终止被拒绝。
	mustOK(t, idx.CompleteOperation("op1"))
	if got := idx.Operation("op1"); got.State() != OpCompleted || got.TerminatedAt() != 1000 {
		t.Fatalf("op1 termination wrong: %+v", got)
	}
	wantErr(t, idx.CompleteOperation("op1"), ErrOperationTerminated)
	mustOK(t, idx.FailOperation("op2"))
	if got := idx.Operation("op2"); got.State() != OpFailed {
		t.Fatalf("op2 state = %v, want failed", got.State())
	}

	// 重复扫描/撤销幂等：不产生新墓碑、不重复失效。
	if again := idx.SweepExpired(); len(again) != 0 {
		t.Fatalf("repeat sweep must be idempotent, got %v", again)
	}
	revoked, err := idx.Revoke("E1", "")
	mustOK(t, err)
	if revoked != nil {
		t.Fatalf("revoke of dead exception must return nil event, got %+v", revoked)
	}
	if len(idx.Events()) != 1 {
		t.Fatalf("want single tombstone, got %d", len(idx.Events()))
	}

	// 根记录保留：失效例外仍可按世代查询，资源原因仍可解释。
	gen1 := idx.ExceptionGeneration(v1)
	if gen1 == nil || gen1.Active() || gen1.State() != ExceptionExpired {
		t.Fatalf("root record must be retained as expired tombstone, got %+v", gen1)
	}
	if !idx.Resource("r1").Restricted {
		t.Fatal("r1 snapshot should remain restricted")
	}
}

// 菱形依赖去重 + abort 强制中止在途操作 + 重复失效幂等。
func TestDiamondDedupAndAbort(t *testing.T) {
	idx := NewDependencyIndex(NewVirtualClock(0))

	excA, err := idx.RegisterException("A", 0, TerminateAbort)
	mustOK(t, err)
	excB, err := idx.RegisterException("B", 0, TerminateDrain)
	mustOK(t, err)

	_, err = idx.RegisterResource("base", []ExceptionVersion{excA.Version()}, nil)
	mustOK(t, err)
	_, err = idx.RegisterResource("m1", []ExceptionVersion{excB.Version()}, []string{"base"})
	mustOK(t, err)
	_, err = idx.RegisterResource("m2", nil, []string{"base"})
	mustOK(t, err)
	_, err = idx.RegisterResource("top", nil, []string{"m1", "m2"})
	mustOK(t, err)

	for _, id := range []string{"base", "m1", "m2", "top"} {
		_, err := idx.BeginSensitiveOperation(id, "op-"+id)
		mustOK(t, err)
	}

	event, err := idx.Revoke("A", "incident-1")
	mustOK(t, err)
	if event == nil || event.Reason != ReasonRevoked {
		t.Fatalf("unexpected revoke event: %+v", event)
	}

	for _, id := range []string{"base", "m1", "m2", "top"} {
		res := idx.Resource(id)
		if !res.Restricted || res.TerminationPolicy != TerminateAbort {
			t.Fatalf("%s must be restricted/abort, got %+v", id, res)
		}
		wantVersions(t, res.Causes, excA.Version())
		if len(idx.resources[id].runningOps) != 0 {
			t.Fatalf("%s in-flight ops must be aborted and removed", id)
		}
		if _, err := idx.BeginSensitiveOperation(id, ""); !errors.Is(err, ErrResourceRestricted) {
			t.Fatalf("%s must reject new sensitive ops, got %v", id, err)
		}
	}

	for _, id := range []string{"base", "m1", "m2", "top"} {
		op := idx.Operation("op-" + id)
		if op.State() != OpAborted || op.TerminatedAt() != 0 || op.AbortNote() == "" {
			t.Fatalf("op-%s abort record wrong: %+v", id, op)
		}
	}

	again, err := idx.Revoke("A", "")
	mustOK(t, err)
	if again != nil || len(idx.Events()) != 1 {
		t.Fatalf("repeat revoke must not add events, again=%+v events=%d", again, len(idx.Events()))
	}

	// 第二个例外失效：原因合并、abort 策略不降级，已中止操作保持终态。
	_, err = idx.Revoke("B", "")
	mustOK(t, err)
	top := idx.Resource("top")
	wantVersions(t, top.Causes, excA.Version(), excB.Version())
	if top.TerminationPolicy != TerminateAbort {
		t.Fatalf("policy must stay abort, got %v", top.TerminationPolicy)
	}
	if idx.Operation("op-top").State() != OpAborted {
		t.Fatal("aborted op must not change state")
	}
	if len(idx.Events()) != 2 {
		t.Fatalf("want 2 tombstones, got %d", len(idx.Events()))
	}
}
