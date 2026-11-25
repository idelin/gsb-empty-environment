package identity

import "testing"

// 槽号代次重用：同一槽位先代次 1 后被代次 2 覆盖；旧句柄恢复失效，重复
// 消费也失效。只有最新代次句柄可恢复。
func TestSlotGenerationReuse(t *testing.T) {
	in := Input{Events: []Event{
		{ID: 1, Chain: 0, Thread: 1, Kind: EnterRequest, Token: tok("a", 1)},
		{ID: 2, Chain: 0, Thread: 1, Kind: Spawn, Slot: 5, HandleGeneration: 1},
		{ID: 3, Chain: 0, Thread: 1, Kind: EnterRequest, Token: tok("a", 2)},
		{ID: 4, Chain: 0, Thread: 1, Kind: Spawn, Slot: 5, HandleGeneration: 2},

		// 旧代次恢复必失败（仅审计），放在独立链且后接一个合法读取，避免非法
		// 事件成为链尾而让整条历史无法完成。
		{ID: 10, Chain: 1, Thread: 2, Kind: ResumeHandle, Slot: 5, HandleGeneration: 1, After: []int{4}},
		{ID: 14, Chain: 1, Thread: 2, Kind: ProtectedRead, Policy: pol("a", 2), After: []int{10}},
		{ID: 11, Chain: 2, Thread: 2, Kind: ResumeHandle, Slot: 5, HandleGeneration: 2, After: []int{4}},
		{ID: 12, Chain: 3, Thread: 2, Kind: ResumeHandle, Slot: 5, HandleGeneration: 2, After: []int{11}},
		{ID: 13, Chain: 2, Thread: 2, Kind: ProtectedRead, Policy: pol("a", 2), After: []int{11}},
	}}
	res, err := Analyze(in, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Exhaustive || !res.HasLegalHistory {
		t.Fatalf("expected legal history, got legal=%v", res.HasLegalHistory)
	}
	if !hasIllegal(res, 10, ReasonStaleHandle) {
		t.Fatalf("expected stale generation rejection, got %+v", res.IllegalAttempts)
	}
	if !hasIllegal(res, 12, ReasonConsumedHandle) {
		t.Fatalf("expected consumed handle rejection, got %+v", res.IllegalAttempts)
	}
	rr := res.Reads[13]
	if rr.Class != AllConform {
		t.Fatalf("generation-2 snapshot must dominate legal reads, got %v", rr.Class)
	}
	if res.Reads[14].Class != Unreachable {
		t.Fatalf("read behind the stale handle must be unreachable, got %v", res.Reads[14].Class)
	}
	assertOracleAgrees(t, in, res)
}

// 部分顺序中相同已执行集合却有不同身份栈：B1、B2 两个借用后，归还 B2 与
// 读取 R30 互不排序。执行完同一集合 {进入,B1,B2,归还B2,R30} 时：
// R30 先于归还则栈顶仍是 B2(d/1)，归还先于 R30 则栈顶变成 B1(b/1)。
func TestSameExecutedSetDifferentIdentityStacks(t *testing.T) {
	in := Input{Events: []Event{
		{ID: 1, Chain: 0, Thread: 1, Kind: EnterRequest, Token: tok("a", 1)},
		{ID: 2, Chain: 0, Thread: 1, Kind: Borrow, Token: tok("b", 1), BorrowID: 1},
		{ID: 3, Chain: 0, Thread: 1, Kind: Borrow, Token: tok("d", 1), BorrowID: 2},
		{ID: 20, Chain: 1, Thread: 1, Kind: Restore, BorrowID: 2, After: []int{3}},
		{ID: 30, Chain: 2, Thread: 1, Kind: ProtectedRead, Policy: pol("d", 1), After: []int{3}},
		{ID: 40, Chain: 3, Thread: 1, Kind: Restore, BorrowID: 1, After: []int{20, 30}},
	}}
	res, err := Analyze(in, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Exhaustive || !res.HasLegalHistory {
		t.Fatalf("expected legal histories, got legal=%v", res.HasLegalHistory)
	}
	r3 := res.Reads[30]
	if r3.Class != Mixed {
		t.Fatalf("R30 interleaves with restore and must be Mixed, got %v", r3.Class)
	}
	if err := res.VerifyWitness(30, Conform, r3.ConformPrefix); err != nil {
		t.Fatalf("R30 conform witness rejected: %v", err)
	}
	if err := res.VerifyWitness(30, Violation, r3.ViolatePrefix); err != nil {
		t.Fatalf("R30 violation witness rejected: %v", err)
	}
	// 同一读取在两条合法交错中观测到不同身份栈指纹（已执行集合也不同，
	// 但读取身份由偏序交错决定而非日志顺序）。
	obs := res.ReadStates[30]
	distinct := map[string]struct{}{}
	for _, o := range obs {
		distinct[o.Fingerprint] = struct{}{}
	}
	if len(distinct) < 2 {
		t.Fatalf("expected distinct identity-stack observations for read 30, got %d", len(distinct))
	}
	assertOracleAgrees(t, in, res)
}

// 预算未知：极小预算截断探索时必须显式标记 Unknown/BudgetCutoff，且不得在
// 未穷尽时签发覆盖证明。
func TestBudgetUnknown(t *testing.T) {
	in := Input{Events: []Event{
		{ID: 1, Chain: 0, Thread: 1, Kind: EnterRequest, Token: tok("a", 1)},
		{ID: 2, Chain: 0, Thread: 1, Kind: Spawn, Slot: 0, HandleGeneration: 1},
		{ID: 10, Chain: 1, Thread: 2, Kind: ResumeHandle, Slot: 0, HandleGeneration: 1, After: []int{2}},
		{ID: 11, Chain: 1, Thread: 2, Kind: ProtectedRead, Policy: pol("a", 1), After: []int{10}},
	}}
	res, err := Analyze(in, Budget{MaxStates: 2, MaxTransitions: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !res.BudgetCutoff || res.Exhaustive {
		t.Fatalf("expected explicit cutoff, got cutoff=%v exhaustive=%v", res.BudgetCutoff, res.Exhaustive)
	}
	if res.Coverage[11] != nil {
		t.Fatal("no coverage certificate may be issued under a cutoff")
	}
	full, err := Analyze(in, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if full.Reads[11].Class != AllConform {
		t.Fatalf("full analysis should classify read as AllConform, got %v", full.Reads[11].Class)
	}
}
