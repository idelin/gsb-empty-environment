package identity

import "testing"

// 同线程嵌套异常：B1、B2 后先归还 B1 违反 LIFO，是非法迁移，不能把该交错
// 当作读取违规证据；合法交错只有先 B2 再 B1。
func TestSameThreadNestingViolation(t *testing.T) {
	in := Input{Events: []Event{
		{ID: 1, Chain: 0, Thread: 1, Kind: EnterRequest, Token: tok("a", 1)},
		{ID: 2, Chain: 0, Thread: 1, Kind: Borrow, Token: tok("b", 1), BorrowID: 1},
		{ID: 3, Chain: 0, Thread: 1, Kind: Borrow, Token: tok("c", 1), BorrowID: 2},
		// 两条归还链只有屏障前驱、互不排序：先归还 B1 违反 LIFO（非法），
		// 先归还 B2 合法。偏序允许这两种交错。
		{ID: 4, Chain: 1, Thread: 1, Kind: Restore, BorrowID: 1, After: []int{3}},
		{ID: 5, Chain: 2, Thread: 1, Kind: Restore, BorrowID: 2, After: []int{3}},
		{ID: 6, Chain: 3, Thread: 1, Kind: ProtectedRead, Policy: pol("a", 1), After: []int{4, 5}},
	}}
	res, err := Analyze(in, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Exhaustive || !res.HasLegalHistory {
		t.Fatalf("expected exhaustive legal analysis, got exhaustive=%v legal=%v", res.Exhaustive, res.HasLegalHistory)
	}
	if !hasIllegal(res, 4, ReasonLIFO) {
		t.Fatalf("expected LIFO rejection for event 4, got %+v", res.IllegalAttempts)
	}
	rr := res.Reads[6]
	if rr.Class != AllConform {
		t.Fatalf("read must be AllConform across legal interleavings, got %v (%s)", rr.Class, rr.Reason)
	}
	if rr.ReachableViolate {
		t.Fatal("illegal nesting interleaving must not count as violation evidence")
	}
	if err := res.VerifyWitness(6, Conform, rr.ConformPrefix); err != nil {
		t.Fatalf("conform witness rejected: %v", err)
	}
	if err := res.VerifyWitness(6, Violation, rr.ConformPrefix); err == nil {
		t.Fatal("mismatched outcome witness must be rejected")
	}
	assertOracleAgrees(t, in, res)
}

// 跨线程归还：借用 B 属于线程 1。在线程 2 上恢复它在某些交错下表现为跨线程
// 归还（栈顶恰好是同 ID 借用帧的跨线程副本时拒绝），在另一些交错下表现为
// LIFO 异常（栈顶是句柄快照帧）。非法交错只进审计，句柄快照让读取在全部
// 合法历史中符合。
func TestCrossThreadReturn(t *testing.T) {
	in := Input{Events: []Event{
		{ID: 1, Chain: 0, Thread: 1, Kind: EnterRequest, Token: tok("a", 1)},
		{ID: 2, Chain: 0, Thread: 1, Kind: Borrow, Token: tok("b", 1), BorrowID: 7},
		{ID: 3, Chain: 0, Thread: 1, Kind: Spawn, Slot: 0, HandleGeneration: 1},

		{ID: 10, Chain: 1, Thread: 2, Kind: ResumeHandle, Slot: 0, HandleGeneration: 1, After: []int{3}},
		// 读取链与非法归还链分离：读取不依赖事件 11，完整历史可以在合法交错中
		// 完成；事件 11 在线程 2 上始终是跨线程/LIFO 异常，只进审计。
		{ID: 12, Chain: 1, Thread: 2, Kind: ProtectedRead, Policy: pol("b", 1), After: []int{10}},

		{ID: 14, Chain: 3, Thread: 2, Kind: ProtectedRead, Policy: pol("b", 1), After: []int{10}},
		{ID: 11, Chain: 3, Thread: 2, Kind: Restore, BorrowID: 7, After: []int{14}},
		{ID: 4, Chain: 2, Thread: 1, Kind: Restore, BorrowID: 7, After: []int{3}},
	}}
	res, err := Analyze(in, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Exhaustive || !res.HasLegalHistory {
		t.Fatalf("expected legal history via async handle, got legal=%v", res.HasLegalHistory)
	}
	if !hasIllegal(res, 11, ReasonLIFO) && !hasIllegal(res, 11, ReasonEmptyStack) {
		t.Fatalf("expected cross-thread/LIFO rejection, got %+v", res.IllegalAttempts)
	}
	rr := res.Reads[12]
	if rr.Class != AllConform {
		t.Fatalf("snapshot identity b/1 must make read conform in all legal histories, got %v", rr.Class)
	}
	if res.Reads[14].Class != AllConform {
		t.Fatalf("second-chain read must be AllConform, got %v", res.Reads[14].Class)
	}
	if err := res.VerifyWitness(12, Conform, rr.ConformPrefix); err != nil {
		t.Fatalf("witness rejected: %v", err)
	}
	assertOracleAgrees(t, in, res)
}

// 无合法历史：显式退出时普通借用栈非空，退出事件在所有交错中非法，因此不
// 存在完整合法历史（HasLegalHistory=false），读取分类降级为 Unknown。
func TestNoLegalHistory(t *testing.T) {
	in := Input{Events: []Event{
		{ID: 1, Chain: 0, Thread: 1, Kind: EnterRequest, Token: tok("a", 1)},
		{ID: 2, Chain: 0, Thread: 1, Kind: Borrow, Token: tok("b", 1), BorrowID: 1},
		{ID: 3, Chain: 0, Thread: 1, Kind: ProtectedRead, Policy: pol("b", 1)},
		{ID: 4, Chain: 0, Thread: 1, Kind: Exit},
	}}
	res, err := Analyze(in, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if res.HasLegalHistory {
		t.Fatal("borrow left on stack must eliminate every legal history")
	}
	if !hasIllegal(res, 4, ReasonBorrowsAtExit) {
		t.Fatalf("expected exit rejection, got %+v", res.IllegalAttempts)
	}
	if res.Reads[3].Class != Unknown {
		t.Fatalf("reads must be Unknown without a legal history, got %v", res.Reads[3].Class)
	}
	oracle, err := EnumeratePermutations(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(oracle.MaximalLegalPrefixes) == 0 || containsID(oracle.MaximalLegalPrefixes, 4) {
		t.Fatal("oracle must keep maximal prefixes that stop before the rejected exit")
	}
	for _, h := range oracle.LegalHistories {
		if containsID([][]int{h}, 4) {
			t.Fatal("no legal history may pass through the rejected exit")
		}
	}
}

func containsID(histories [][]int, id int) bool {
	for _, h := range histories {
		for _, x := range h {
			if x == id {
				return true
			}
		}
	}
	return false
}
