package netsec

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

type testPlugin struct {
	beforeRelease func(cb *Callback)
	onClosed      func(cb *Callback)
	beforeCalls   int
	onClosedCalls int
}

func (p *testPlugin) BeforeRelease(cb *Callback) {
	p.beforeCalls++
	if p.beforeRelease != nil {
		p.beforeRelease(cb)
	}
}

func (p *testPlugin) OnClosed(cb *Callback) {
	p.onClosedCalls++
	if p.onClosed != nil {
		p.onClosed(cb)
	}
}

func mustAddHandle(t *testing.T, h *Host, o *Object) Handle {
	t.Helper()
	hd, err := h.AddHandle(o)
	if err != nil {
		t.Fatalf("AddHandle(%s): %v", o.Name(), err)
	}
	return hd
}

func TestMutualMove(t *testing.T) {
	h := New(nil, nil)
	a := h.NewObject("A")
	b := h.NewObject("B")
	h1 := mustAddHandle(t, h, a)
	h2 := mustAddHandle(t, h, b)

	// 两个 Open 对象互相 Move，均应成功。
	if err := h.MoveTo(a, b, h1); err != nil {
		t.Fatalf("MoveTo(A->B): %v", err)
	}
	if err := h.MoveTo(b, a, h2); err != nil {
		t.Fatalf("MoveTo(B->A): %v", err)
	}
	if got := a.Handles(); len(got) != 1 || got[0].ID != h2.ID || got[0].Gen != a.Gen() {
		t.Fatalf("A 应持有重绑代次的 h2，实际 %+v", got)
	}
	if got := b.Handles(); len(got) != 1 || got[0].ID != h1.ID || got[0].Gen != b.Gen() {
		t.Fatalf("B 应持有重绑代次的 h1，实际 %+v", got)
	}

	// 自移动拒绝。
	if err := h.MoveTo(a, a, a.Handles()[0]); !errors.Is(err, ErrSelfMove) {
		t.Fatalf("自移动应返回 ErrSelfMove，实际 %v", err)
	}
	// 旧代次句柄拒绝。
	if err := h.MoveTo(a, b, Handle{ID: h2.ID, Gen: 99}); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("旧代次应返回 ErrStaleGeneration，实际 %v", err)
	}
	// 不拥有的句柄拒绝。
	if err := h.MoveTo(a, b, Handle{ID: 999, Gen: a.Gen()}); !errors.Is(err, ErrHandleNotOwned) {
		t.Fatalf("未拥有句柄应返回 ErrHandleNotOwned，实际 %v", err)
	}
	// 目标对象已满（4 个句柄）拒绝。
	full := h.NewObject("FULL")
	for i := 0; i < MaxHandles; i++ {
		mustAddHandle(t, h, full)
	}
	if err := h.MoveTo(a, full, a.Handles()[0]); !errors.Is(err, ErrTooManyHandles) {
		t.Fatalf("目标已满应返回 ErrTooManyHandles，实际 %v", err)
	}
	// 目标对象已关闭拒绝。
	closed := h.NewObject("CLOSED")
	if err := h.Close(closed); err != nil {
		t.Fatalf("Close(CLOSED): %v", err)
	}
	if err := h.MoveTo(a, closed, a.Handles()[0]); !errors.Is(err, ErrNotOpen) {
		t.Fatalf("目标已关闭应返回 ErrNotOpen，实际 %v", err)
	}
}

func TestMoveRejectedDuringClosing(t *testing.T) {
	var moveErr, callErr error
	p := &testPlugin{beforeRelease: func(cb *Callback) {
		hd, _ := cb.Handle()
		moveErr = cb.MoveTo(hd, cb.Object().host.Object("B"))
		callErr = cb.Call(hd)
	}}
	h := New(nil, p)
	a := h.NewObject("A")
	h.NewObject("B")
	mustAddHandle(t, h, a)

	if err := h.Close(a); err != nil {
		t.Fatalf("Close(A): %v", err)
	}
	// Closing 对象上的 Move/Call 请求全部拒绝（句柄代次已随冻结失效）。
	if !errors.Is(moveErr, ErrStaleGeneration) {
		t.Fatalf("Closing 中 MoveTo 应被拒绝，实际 %v", moveErr)
	}
	if !errors.Is(callErr, ErrStaleGeneration) {
		t.Fatalf("Closing 中 Call 应被拒绝，实际 %v", callErr)
	}
	if got := h.ReleaseCount(1); got != 1 {
		t.Fatalf("句柄应恰好 Release 一次，实际 %d", got)
	}
	if vs := h.Violations(); len(vs) != 0 {
		t.Fatalf("修订版不应出现违规，实际 %+v", vs)
	}
}

func TestReleaseFailureAggregation(t *testing.T) {
	releaseFn := func(hd Handle) error {
		if hd.ID == 2 {
			return fmt.Errorf("boom-%d", hd.ID)
		}
		return nil
	}
	h := New(releaseFn, nil)
	a := h.NewObject("A")
	mustAddHandle(t, h, a)
	mustAddHandle(t, h, a)
	mustAddHandle(t, h, a)

	err := h.Close(a)
	if err == nil || !strings.Contains(err.Error(), "boom-2") {
		t.Fatalf("Close 应聚合 h2 的释放失败，实际 %v", err)
	}
	if strings.Contains(err.Error(), "boom-1") || strings.Contains(err.Error(), "boom-3") {
		t.Fatalf("成功的释放不应出现在错误中，实际 %v", err)
	}
	// 失败不中断：其余句柄仍被释放，所有权终结。
	for id := 1; id <= 3; id++ {
		if got := h.ReleaseCount(id); got != 1 {
			t.Fatalf("h%d 应 Release 一次，实际 %d", id, got)
		}
	}
	if a.State() != StateClosed {
		t.Fatalf("对象应为 Closed，实际 %s", a.State())
	}
	if got := a.Handles(); len(got) != 0 {
		t.Fatalf("所有权应已终结，实际 %+v", got)
	}
	// 重复 Close 幂等返回原结果。
	if again := h.Close(a); again != err {
		t.Fatalf("重复 Close 应返回原结果 %v，实际 %v", err, again)
	}
}

func TestReentrantCloseInCallback(t *testing.T) {
	var reentrantErr error
	fired := false
	p := &testPlugin{beforeRelease: func(cb *Callback) {
		if fired {
			return
		}
		fired = true
		reentrantErr = cb.Close(cb.Object())
	}}
	h := New(nil, p)
	a := h.NewObject("A")
	mustAddHandle(t, h, a)
	mustAddHandle(t, h, a)

	if err := h.Close(a); err != nil {
		t.Fatalf("Close(A): %v", err)
	}
	if !errors.Is(reentrantErr, ErrCloseInProgress) {
		t.Fatalf("回调内重入 Close 应为 ErrCloseInProgress 空操作，实际 %v", reentrantErr)
	}
	for id := 1; id <= 2; id++ {
		if got := h.ReleaseCount(id); got != 1 {
			t.Fatalf("h%d 应恰好 Release 一次，实际 %d", id, got)
		}
	}
	if p.onClosedCalls != 1 {
		t.Fatalf("OnClosed 应恰好一次，实际 %d", p.onClosedCalls)
	}
}

func TestStaleGenerationHandles(t *testing.T) {
	h := New(nil, nil)
	a := h.NewObject("A")
	b := h.NewObject("B")
	stale := mustAddHandle(t, h, a)
	if err := h.Close(a); err != nil {
		t.Fatalf("Close(A): %v", err)
	}
	// 关闭后旧代次句柄失效。
	if err := h.Call(a, stale); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("旧代次 Call 应返回 ErrStaleGeneration，实际 %v", err)
	}
	if err := h.MoveTo(a, b, stale); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("旧代次 MoveTo 应返回 ErrStaleGeneration，实际 %v", err)
	}
	// Open 对象上当前代次句柄正常，伪造代次拒绝。
	c := h.NewObject("C")
	cur := mustAddHandle(t, h, c)
	if err := h.Call(c, cur); err != nil {
		t.Fatalf("当前代次 Call 应成功，实际 %v", err)
	}
	forged := Handle{ID: cur.ID, Gen: cur.Gen + 1}
	if err := h.Call(c, forged); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("伪造代次应返回 ErrStaleGeneration，实际 %v", err)
	}
}

func TestOnClosedReadOnlyNotification(t *testing.T) {
	releaseFn := func(Handle) error { return errors.New("kaboom") }
	var saved Handle
	var closeInOnClosed, moveInOnClosed, callInOnClosed error
	p := &testPlugin{
		beforeRelease: func(cb *Callback) { saved, _ = cb.Handle() },
		onClosed: func(cb *Callback) {
			closeInOnClosed = cb.Close(cb.Object())
			moveInOnClosed = cb.MoveTo(saved, cb.Object().host.Object("B"))
			callInOnClosed = cb.Call(saved)
		},
	}
	h := New(releaseFn, p)
	a := h.NewObject("A")
	h.NewObject("B")
	mustAddHandle(t, h, a)

	original := h.Close(a)
	if original == nil {
		t.Fatal("Close 应返回聚合的释放错误")
	}
	if p.onClosedCalls != 1 {
		t.Fatalf("OnClosed 应恰好一次，实际 %d", p.onClosedCalls)
	}
	// OnClosed 中的 Close 幂等返回原结果。
	if closeInOnClosed != original {
		t.Fatalf("OnClosed 中 Close 应返回原结果 %v，实际 %v", original, closeInOnClosed)
	}
	// OnClosed 中不能 Move 或 Call。
	if !errors.Is(moveInOnClosed, ErrCallbackReadOnly) {
		t.Fatalf("OnClosed 中 MoveTo 应拒绝，实际 %v", moveInOnClosed)
	}
	if !errors.Is(callInOnClosed, ErrCallbackReadOnly) {
		t.Fatalf("OnClosed 中 Call 应拒绝，实际 %v", callInOnClosed)
	}
	// 再次 Close 不重复触发 OnClosed。
	_ = h.Close(a)
	if p.onClosedCalls != 1 {
		t.Fatalf("OnClosed 不应重复触发，实际 %d", p.onClosedCalls)
	}
}

func TestCallbackErrorsNotSwallowed(t *testing.T) {
	releaseFn := func(Handle) error { return errors.New("release-boom") }
	var moveErr error
	p := &testPlugin{beforeRelease: func(cb *Callback) {
		hd, _ := cb.Handle()
		// 插件尝试把句柄转移到已关闭对象，必然失败。
		moveErr = cb.MoveTo(hd, cb.Object().host.Object("DEAD"))
	}}
	h := New(releaseFn, p)
	a := h.NewObject("A")
	dead := h.NewObject("DEAD")
	mustAddHandle(t, h, a)
	if err := h.Close(dead); err != nil {
		t.Fatalf("Close(DEAD): %v", err)
	}

	err := h.Close(a)
	// 回调动作的错误返回给插件本身，不被吞掉。
	if !errors.Is(moveErr, ErrNotOpen) {
		t.Fatalf("插件应收到 MoveTo 拒绝错误，实际 %v", moveErr)
	}
	// Release 失败仍然聚合进 Close 结果，不被回调错误掩盖。
	if err == nil || !strings.Contains(err.Error(), "release-boom") {
		t.Fatalf("Close 应聚合 Release 失败，实际 %v", err)
	}
	if got := h.ReleaseCount(1); got != 1 {
		t.Fatalf("句柄应恰好 Release 一次，实际 %d", got)
	}
	if a.State() != StateClosed {
		t.Fatalf("所有权应终结（Closed），实际 %s", a.State())
	}
}

func TestReleaseRecordedAtMostOnce(t *testing.T) {
	calls := 0
	h := New(func(Handle) error { calls++; return nil }, nil)
	o := h.NewObject("O")
	hd := Handle{ID: 42, Gen: 0}
	// 白盒：即使重复触发，宿主对每个句柄最多 Release 一次。
	_ = h.releaseOnce(o, hd)
	_ = h.releaseOnce(o, hd)
	if calls != 1 || h.ReleaseCount(42) != 1 {
		t.Fatalf("每句柄最多一次 Release，实际调用 %d 次", calls)
	}
}

func TestPendingReviewDoubleRelease(t *testing.T) {
	plugin := &ScriptPlugin{script: []Action{
		{Kind: ActMoveCurrent, Target: "B"},
		{Kind: ActClose, Target: "B"},
	}}
	h := NewPendingReview(nil, plugin)
	plugin.host = h
	a := h.NewObject("A")
	h.NewObject("B")
	h1 := mustAddHandle(t, h, a)

	if err := h.Close(a); err != nil {
		t.Fatalf("Close(A): %v", err)
	}
	// 待审版本：Move 与嵌套 Close 都“成功”，句柄被二次 Release。
	if len(plugin.Results) != 2 || plugin.Results[0] != nil || plugin.Results[1] != nil {
		t.Fatalf("待审版本中两个动作都应成功，实际 %v", plugin.Results)
	}
	if got := h.ReleaseCount(h1.ID); got != 2 {
		t.Fatalf("待审版本应出现二次 Release，实际 %d 次", got)
	}
	vs := h.Violations()
	if len(vs) == 0 || vs[0].Kind != ViolationDoubleRelease {
		t.Fatalf("应记录 DoubleRelease 违规，实际 %+v", vs)
	}
	if len(vs[0].Stack) == 0 {
		t.Fatal("违规应携带完整重入栈")
	}
}

func TestPendingReviewCallAfterClosing(t *testing.T) {
	plugin := &ScriptPlugin{script: []Action{{Kind: ActCallCurrent}}}
	h := NewPendingReview(nil, plugin)
	plugin.host = h
	a := h.NewObject("A")
	h.NewObject("B")
	mustAddHandle(t, h, a)

	if err := h.Close(a); err != nil {
		t.Fatalf("Close(A): %v", err)
	}
	if len(plugin.Results) != 1 || plugin.Results[0] != nil {
		t.Fatalf("待审版本中 Closing 后 Call 应成功，实际 %v", plugin.Results)
	}
	vs := h.Violations()
	if len(vs) == 0 || vs[0].Kind != ViolationCallAfterClosing {
		t.Fatalf("应记录 CallAfterClosing 违规，实际 %+v", vs)
	}
}
