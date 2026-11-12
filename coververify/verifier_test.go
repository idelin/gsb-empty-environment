package coververify

import (
	"errors"
	"math"
	"testing"
)

type fixture struct {
	host     *testHost
	verifier *Verifier

	rootV1   Digest
	rootV2   Digest
	deposit  Digest // deposit 10
	overdraw Digest // withdraw 100（对 root 余额 10 构成透支）
	withdraw Digest // withdraw 15
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	host := newTestHost()
	rootV1 := host.register("bank/v1", testAccount{Account: "acct-1", Balance: 10})
	rootV2 := host.register("bank/v2", testAccount{Account: "acct-1", Balance: 10})

	f := &fixture{
		host:     host,
		verifier: New(host.oracle),
		rootV1:   rootV1,
		rootV2:   rootV2,
		deposit:  commandDigest("deposit", 10),
		overdraw: commandDigest("withdraw", 100),
		withdraw: commandDigest("withdraw", 15),
	}
	f.verifier.RegisterRoot("bank/v1", rootV1)
	f.verifier.RegisterRoot("bank/v2", rootV2)
	return f
}

func statusByUID(v *Verifier, uid string) (Status, bool) {
	for _, info := range v.Snapshot() {
		if info.UID == uid {
			return info.Status, true
		}
	}
	return "", false
}

func assertStatus(t *testing.T, v *Verifier, uid string, want Status) {
	t.Helper()
	got, ok := statusByUID(v, uid)
	if !ok {
		t.Fatalf("receipt %s not found in snapshot", uid)
	}
	if got != want {
		t.Fatalf("receipt %s status = %q, want %q", uid, got, want)
	}
}

func assertFloat(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("coverage = %v, want %v", got, want)
	}
}

// TestCoveredChainOnlyAfterReplay：合法链必须逐跳通过确定性重放才计入覆盖，
// 断链回执先保留待定，前驱补齐后自动转为已覆盖。
func TestCoveredChainOnlyAfterReplay(t *testing.T) {
	f := newFixture(t)

	// 先提交第二跳：前状态是“存款后”的状态，但该状态此刻不在已核验链上。
	afterDeposit := f.host.register("bank/v1", testAccount{Account: "acct-1", Balance: 20})
	second := legitReceipt(f.host, "bank/v1", afterDeposit, f.withdraw)
	res, err := f.verifier.Submit(second)
	if err != nil {
		t.Fatalf("submit second hop: %v", err)
	}
	assertStatus(t, f.verifier, res.UID, StatusPending)

	st := f.verifier.Stats()
	if st.CoveredEdges != 0 || st.Pending != 1 {
		t.Fatalf("broken chain must not count: got covered=%d pending=%d", st.CoveredEdges, st.Pending)
	}

	// 补齐第一跳：根状态可达且重放一致，第一跳覆盖后第二跳自动续接。
	first := legitReceipt(f.host, "bank/v1", f.rootV1, f.deposit)
	resFirst, err := f.verifier.Submit(first)
	if err != nil {
		t.Fatalf("submit first hop: %v", err)
	}
	assertStatus(t, f.verifier, resFirst.UID, StatusCovered)
	assertStatus(t, f.verifier, res.UID, StatusCovered)

	st = f.verifier.Stats()
	if st.CoveredEdges != 2 || st.DistinctEdges != 2 || st.Pending != 0 {
		t.Fatalf("full chain should be covered: %+v", st)
	}
	assertFloat(t, st.Coverage, 1.0)
}

// TestCrossModelSplicingCannotContinueChain：把 A 模型的后继状态拼到 B 模型的回执上，
// 因链按模型版本隔离，拼接回执只能是断链待定，绝不计入覆盖。
func TestCrossModelSplicingCannotContinueChain(t *testing.T) {
	f := newFixture(t)

	// bank/v1 存款后的状态，只存在于 v1 的已核验链与宿主注册表中。
	v1AfterDeposit := f.host.register("bank/v1", testAccount{Account: "acct-1", Balance: 20})
	firstV1, err := f.verifier.Submit(legitReceipt(f.host, "bank/v1", f.rootV1, f.deposit))
	if err != nil {
		t.Fatalf("submit v1 deposit: %v", err)
	}
	assertStatus(t, f.verifier, firstV1.UID, StatusCovered)

	// 伪造拼接：声称在 bank/v2 下，从 v1 的状态出发执行取款。
	spliced := Receipt{
		ModelVersion: "bank/v2",
		PrevState:    v1AfterDeposit,
		Input:        f.withdraw,
		Output:       SumDigest([]byte("fabricated-output")),
	}
	res, err := f.verifier.Submit(spliced)
	if err != nil {
		t.Fatalf("spliced receipt should be accepted for quarantine, got %v", err)
	}
	assertStatus(t, f.verifier, res.UID, StatusPending)

	st := f.verifier.Stats()
	if st.CoveredEdges != 1 || st.Pending != 1 {
		t.Fatalf("cross-model splice must stay pending: %+v", st)
	}

	// 即使输出摘要恰好等于 v2 合法重放输出，模型隔离仍使其保持断链。
	legitV2 := f.host.oracle("bank/v2", f.rootV2, f.deposit)
	if legitV2.Err != nil {
		t.Fatalf("oracle v2: %v", legitV2.Err)
	}
	spliced2 := Receipt{
		ModelVersion: "bank/v2",
		PrevState:    v1AfterDeposit,
		Input:        f.deposit,
		Output:       legitV2.Output,
	}
	res2, err := f.verifier.Submit(spliced2)
	if err != nil {
		t.Fatalf("spliced2: %v", err)
	}
	assertStatus(t, f.verifier, res2.UID, StatusPending)
	if st := f.verifier.Stats(); st.CoveredEdges != 1 {
		t.Fatalf("cross-model edge must never be covered: %+v", st)
	}
}

// TestMissingRejectTransitionKeepsRequiredCoverageIncomplete：拒绝转换是必须显式
// 覆盖的边；只覆盖普通转换时，必需的拒绝边持续缺失，覆盖率不得虚报为 100%。
func TestMissingRejectTransitionKeepsRequiredCoverageIncomplete(t *testing.T) {
	f := newFixture(t)
	rejectEdge := TransitionKey{ModelVersion: "bank/v1", PrevState: f.rootV1, Input: f.overdraw}
	f.verifier.RequireEdge(rejectEdge, true)

	// 只覆盖一条普通存款转换。
	res, err := f.verifier.Submit(legitReceipt(f.host, "bank/v1", f.rootV1, f.deposit))
	if err != nil {
		t.Fatalf("deposit: %v", err)
	}
	assertStatus(t, f.verifier, res.UID, StatusCovered)

	st := f.verifier.Stats()
	if st.RequiredTotal != 1 || st.RequiredCovered != 0 || len(st.MissingRequired) != 1 {
		t.Fatalf("missing reject edge must show as unmet: %+v", st)
	}
	assertFloat(t, st.Coverage, 0)

	// 显式上报拒绝转换后，必需边满足；拒绝边覆盖普通边时拒绝预期仍不匹配。
	rejectReceipt := legitReceipt(f.host, "bank/v1", f.rootV1, f.overdraw)
	resReject, err := f.verifier.Submit(rejectReceipt)
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	assertStatus(t, f.verifier, resReject.UID, StatusCovered)

	var rejected bool
	for _, snapshot := range f.verifier.Snapshot() {
		if snapshot.UID == resReject.UID {
			rejected = snapshot.Rejected
		}
	}
	if !rejected {
		t.Fatalf("overdraw receipt must be marked Rejected=true")
	}

	st = f.verifier.Stats()
	if st.RequiredCovered != 1 || len(st.MissingRequired) != 0 {
		t.Fatalf("reject edge now required-covered: %+v", st)
	}
	assertFloat(t, st.Coverage, 1.0)
}

// TestDuplicateReceiptsDoNotInflateCoverage：内容相同的重复回执只识别不重复计边。
func TestDuplicateReceiptsDoNotInflateCoverage(t *testing.T) {
	f := newFixture(t)
	rcpt := legitReceipt(f.host, "bank/v1", f.rootV1, f.deposit)

	first, err := f.verifier.Submit(rcpt)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	for i := 0; i < 5; i++ {
		dup, err := f.verifier.Submit(rcpt)
		if err != nil {
			t.Fatalf("duplicate %d: %v", i, err)
		}
		if !dup.Duplicate || dup.DuplicateOf != first.UID || dup.UID != first.UID {
			t.Fatalf("duplicate not recognized: %+v", dup)
		}
	}

	st := f.verifier.Stats()
	if st.Submissions != 6 || st.Duplicate != 5 {
		t.Fatalf("duplicate accounting wrong: %+v", st)
	}
	if st.DistinctEdges != 1 || st.CoveredEdges != 1 {
		t.Fatalf("duplicates must not inflate edges: %+v", st)
	}

	// 显式 ID 相同但四要素不同属于 ID 复用，回执无效并留痕。
	idReceipt := legitReceipt(f.host, "bank/v1", f.rootV1, f.withdraw)
	idReceipt.ID = "receipt-1"
	if _, err := f.verifier.Submit(idReceipt); err != nil {
		t.Fatalf("id receipt: %v", err)
	}
	tampered := idReceipt
	tampered.Output = SumDigest([]byte("tampered"))
	if _, err := f.verifier.Submit(tampered); !errors.Is(err, ErrReceiptIDReused) {
		t.Fatalf("reused id must fail, got %v", err)
	}
	st = f.verifier.Stats()
	if st.Invalid != 1 || st.DistinctEdges != 2 {
		t.Fatalf("id reuse must be invalid and distinct from earlier edge: %+v", st)
	}
}
