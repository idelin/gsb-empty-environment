package coververify

import (
	"errors"
	"sync"
	"testing"
)

// TestOracleErrorAndPanicNeverCovered：转换回调返回错误或 panic 的边一律不计覆盖，
// 且结果不缓存；回调修复后重新提交即可完成核验。
func TestOracleErrorAndPanicNeverCovered(t *testing.T) {
	f := newFixture(t)
	errEdge := TransitionKey{ModelVersion: "bank/v1", PrevState: f.rootV1, Input: f.deposit}
	injected := errors.New("model backend unavailable")

	// 先在无故障时取得合法回执内容，再注入故障，避免测试宿主自身被故障中断。
	depositReceipt := legitReceipt(f.host, "bank/v1", f.rootV1, f.deposit)
	withdrawReceipt := legitReceipt(f.host, "bank/v1", f.rootV1, f.withdraw)
	overdrawReceipt := legitReceipt(f.host, "bank/v1", f.rootV1, f.overdraw)
	baselineCalls := f.host.calls[errEdge]
	f.host.failEdges[errEdge] = injected

	res, err := f.verifier.Submit(depositReceipt)
	if err != nil {
		t.Fatalf("submit error-edge receipt: %v", err)
	}
	assertStatus(t, f.verifier, res.UID, StatusErrored)
	if st := f.verifier.Stats(); st.CoveredEdges != 0 || st.Errored != 1 {
		t.Fatalf("errored edge must not be covered: %+v", st)
	}
	if calls := f.host.calls[errEdge]; calls != baselineCalls+1 {
		t.Fatalf("errored replay must not be cached, calls=%d baseline=%d", calls, baselineCalls)
	}

	// 修复回调后重新提交同内容回执：重复提交同样触发全链重核，错误结果未缓存，
	// 该边应恢复为已覆盖。
	delete(f.host.failEdges, errEdge)
	second, err := f.verifier.Submit(depositReceipt)
	if err != nil {
		t.Fatalf("resubmit: %v", err)
	}
	if !second.Duplicate {
		t.Fatalf("resubmission must be recognized as duplicate: %+v", second)
	}
	assertStatus(t, f.verifier, res.UID, StatusCovered)

	// panic 注入另一条边，验证回调 panic 同样不算覆盖。
	panicEdge := TransitionKey{ModelVersion: "bank/v1", PrevState: f.rootV1, Input: f.withdraw}
	f.host.panicEdges[panicEdge] = "boom"
	panicRes, err := f.verifier.Submit(withdrawReceipt)
	if err != nil {
		t.Fatalf("submit panic-edge receipt: %v", err)
	}
	assertStatus(t, f.verifier, panicRes.UID, StatusErrored)

	delete(f.host.panicEdges, panicEdge)
	// 任意一次提交都会触发全链重核，panic 边因未缓存而再次调用回调并恢复覆盖。
	heal, err := f.verifier.Submit(overdrawReceipt)
	if err != nil {
		t.Fatalf("heal submit: %v", err)
	}
	assertStatus(t, f.verifier, heal.UID, StatusCovered)
	assertStatus(t, f.verifier, panicRes.UID, StatusCovered)
	assertStatus(t, f.verifier, res.UID, StatusCovered)
	if st := f.verifier.Stats(); st.Errored != 0 || st.CoveredEdges != 3 {
		t.Fatalf("after recovery all edges must be covered: %+v", st)
	}
}

// TestMismatchedOutputIsRejected：回执输出与确定性重放不一致时判定 mismatch，
// 该边不得覆盖，其下游继续保持断链待定。
func TestMismatchedOutputIsRejected(t *testing.T) {
	f := newFixture(t)
	forged := legitReceipt(f.host, "bank/v1", f.rootV1, f.deposit)
	forged.Output = SumDigest([]byte("plugin-claimed-output"))

	res, err := f.verifier.Submit(forged)
	if err != nil {
		t.Fatalf("forged output: %v", err)
	}
	assertStatus(t, f.verifier, res.UID, StatusMismatch)

	afterDeposit := f.host.register("bank/v1", testAccount{Account: "acct-1", Balance: 20})
	downstream, err := f.verifier.Submit(legitReceipt(f.host, "bank/v1", afterDeposit, f.withdraw))
	if err != nil {
		t.Fatalf("downstream: %v", err)
	}
	assertStatus(t, f.verifier, downstream.UID, StatusPending)

	st := f.verifier.Stats()
	if st.CoveredEdges != 0 || st.Mismatch != 1 || st.Pending != 1 {
		t.Fatalf("mismatch must block chain continuation: %+v", st)
	}
}

// TestConflictIsolationQuarantinesWholeEdge：同一转换边出现互异输出时，冲突边
// 整体隔离，不计覆盖；隔离解除前同边的所有回执都是 conflict。
func TestConflictIsolationQuarantinesWholeEdge(t *testing.T) {
	f := newFixture(t)

	legit := legitReceipt(f.host, "bank/v1", f.rootV1, f.deposit)
	first, err := f.verifier.Submit(legit)
	if err != nil {
		t.Fatalf("legit: %v", err)
	}
	assertStatus(t, f.verifier, first.UID, StatusCovered)

	rival := legit
	rival.ID = "rival-report"
	rival.Output = SumDigest([]byte("different-observed-output"))
	second, err := f.verifier.Submit(rival)
	if err != nil {
		t.Fatalf("rival: %v", err)
	}

	assertStatus(t, f.verifier, second.UID, StatusConflict)
	assertStatus(t, f.verifier, first.UID, StatusConflict) // 回溯隔离，原先的覆盖被撤销

	st := f.verifier.Stats()
	if st.CoveredEdges != 0 || st.Conflict != 2 || st.DistinctEdges != 1 {
		t.Fatalf("conflict edge must be fully quarantined: %+v", st)
	}

	// 冲突边下游无法续接，保持待定。
	afterDeposit := f.host.register("bank/v1", testAccount{Account: "acct-1", Balance: 20})
	down, err := f.verifier.Submit(legitReceipt(f.host, "bank/v1", afterDeposit, f.withdraw))
	if err != nil {
		t.Fatalf("downstream: %v", err)
	}
	assertStatus(t, f.verifier, down.UID, StatusPending)
}

// TestBareStateVisitClaimCannotForgeCoverage：仅声明“访问过某状态”而没有完整四要素、
// 或使用未知输入摘要的回执，都不能产生任何覆盖。
func TestBareStateVisitClaimCannotForgeCoverage(t *testing.T) {
	f := newFixture(t)

	cases := []Receipt{
		{ModelVersion: "bank/v1", Input: f.deposit, Output: SumDigest([]byte("x"))},    // 缺前状态
		{ModelVersion: "bank/v1", PrevState: f.rootV1, Output: SumDigest([]byte("x"))}, // 缺输入
		{PrevState: f.rootV1, Input: f.deposit, Output: SumDigest([]byte("x"))},        // 缺模型版本
		{ModelVersion: "bank/v1", PrevState: f.rootV1, Input: f.deposit},               // 缺输出
	}
	for i, bad := range cases {
		if _, err := f.verifier.Submit(bad); !errors.Is(err, ErrInvalidReceipt) {
			t.Fatalf("case %d: want ErrInvalidReceipt, got %v", i, err)
		}
	}

	// 未知输入摘要：宿主回调无法重放，按 errored 处理而不是放行。
	unknownInput := SumDigest([]byte("input-never-seen-by-oracle"))
	res, err := f.verifier.Submit(Receipt{
		ModelVersion: "bank/v1",
		PrevState:    f.rootV1,
		Input:        unknownInput,
		Output:       SumDigest([]byte("whatever")),
	})
	if err != nil {
		t.Fatalf("unknown input submit: %v", err)
	}
	assertStatus(t, f.verifier, res.UID, StatusErrored)

	st := f.verifier.Stats()
	if st.CoveredEdges != 0 || st.Invalid != 4 || st.Errored != 1 {
		t.Fatalf("no fabricated coverage allowed: %+v", st)
	}
}

// TestConcurrentSubmissionsAreSafe：并发提交下计数与去重保持一致，无数据竞争。
func TestConcurrentSubmissionsAreSafe(t *testing.T) {
	f := newFixture(t)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rcpt := legitReceipt(f.host, "bank/v1", f.rootV1, f.deposit)
			_, _ = f.verifier.Submit(rcpt)
			_ = f.verifier.Stats()
			_ = f.verifier.Snapshot()
		}(i)
	}
	wg.Wait()

	st := f.verifier.Stats()
	if st.DistinctEdges != 1 || st.CoveredEdges != 1 || st.Submissions != 50 || st.Duplicate != 49 {
		t.Fatalf("concurrent accounting inconsistent: %+v", st)
	}
}

// TestRequiredRejectExpectationIsEnforced：拒绝预期与普通覆盖不可互相冒充。
func TestRequiredRejectExpectationIsEnforced(t *testing.T) {
	f := newFixture(t)

	// bank/v2 的同额取款是普通转换（允许透支），不能满足 v1 上的拒绝边预期。
	v2Edge := TransitionKey{ModelVersion: "bank/v2", PrevState: f.rootV2, Input: f.overdraw}
	f.verifier.RequireEdge(v2Edge, true)

	res, err := f.verifier.Submit(legitReceipt(f.host, "bank/v2", f.rootV2, f.overdraw))
	if err != nil {
		t.Fatalf("v2 overdraw: %v", err)
	}
	assertStatus(t, f.verifier, res.UID, StatusCovered)

	st := f.verifier.Stats()
	if st.RequiredCovered != 0 || len(st.MissingRequired) != 1 {
		t.Fatalf("normal transition must not satisfy reject expectation: %+v", st)
	}

	// 改为普通预期后同一条已覆盖边即满足要求。
	f.verifier.RequireEdge(v2Edge, false)
	st = f.verifier.Stats()
	if st.RequiredCovered != 1 || len(st.MissingRequired) != 0 {
		t.Fatalf("normal expectation should now be met: %+v", st)
	}
}

// TestRequireTransitionConvenienceAndMultipleMissingEdges：验证便捷声明 API，
// 以及多条必需边缺失时 MissingRequired 的确定性排序与覆盖率分母。
func TestRequireTransitionConvenienceAndMultipleMissingEdges(t *testing.T) {
	f := newFixture(t)
	f.verifier.RequireTransition("bank/v1", f.rootV1, f.deposit)
	f.verifier.RequireTransition("bank/v1", f.rootV1, f.withdraw)

	st := f.verifier.Stats()
	if st.RequiredTotal != 2 || st.RequiredCovered != 0 || len(st.MissingRequired) != 2 {
		t.Fatalf("both required edges missing: %+v", st)
	}

	res, err := f.verifier.Submit(legitReceipt(f.host, "bank/v1", f.rootV1, f.deposit))
	if err != nil {
		t.Fatalf("deposit: %v", err)
	}
	assertStatus(t, f.verifier, res.UID, StatusCovered)

	st = f.verifier.Stats()
	if st.RequiredCovered != 1 || len(st.MissingRequired) != 1 {
		t.Fatalf("one required edge still missing: %+v", st)
	}
	if st.MissingRequired[0].Input != f.withdraw {
		t.Fatalf("unexpected missing edge: %+v", st.MissingRequired)
	}
	assertFloat(t, st.Coverage, 0.5)
}
