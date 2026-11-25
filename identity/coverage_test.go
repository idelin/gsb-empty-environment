package identity

import "testing"

// 交叉覆盖证明拒绝：读取 A 的有效证书不能用于读取 B，篡改状态集合或模型
// 指纹同样必须被拒绝；证书不是“一个通过的调度”。
func TestCrossCoverageRejection(t *testing.T) {
	in := Input{Events: []Event{
		{ID: 1, Chain: 0, Thread: 1, Kind: EnterRequest, Token: tok("a", 1)},
		{ID: 2, Chain: 0, Thread: 1, Kind: Spawn, Slot: 0, HandleGeneration: 1},

		{ID: 10, Chain: 1, Thread: 2, Kind: ResumeHandle, Slot: 0, HandleGeneration: 1, After: []int{2}},
		{ID: 11, Chain: 1, Thread: 2, Kind: ProtectedRead, Policy: pol("a", 1), After: []int{10}},

		{ID: 20, Chain: 2, Thread: 3, Kind: ResumeHandle, Slot: 0, HandleGeneration: 1, After: []int{2}},
		{ID: 21, Chain: 2, Thread: 3, Kind: ProtectedRead, Policy: pol("a", 1), After: []int{20}},
	}}
	res, err := Analyze(in, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	certA := res.Coverage[11]
	certB := res.Coverage[21]
	if certA == nil || certB == nil {
		t.Fatal("expected coverage certificates for uniform reads")
	}
	if err := res.VerifyCoverage(11, certA); err != nil {
		t.Fatalf("certificate for its own read rejected: %v", err)
	}
	if err := res.VerifyCoverage(21, certA); err == nil {
		t.Fatal("certificate bound to read 11 must be rejected for target read 21")
	}
	if err := res.VerifyCoverage(11, certB); err == nil {
		t.Fatal("certificate bound to read 21 must be rejected for target read 11")
	}

	tampered := *certA
	tampered.StateFingerprints = append(append([]string(nil), certA.StateFingerprints...), "forged-state")
	if err := res.VerifyCoverage(11, &tampered); err == nil {
		t.Fatal("forged state set must be rejected")
	}
	tampered2 := *certA
	tampered2.ModelFingerprint = "other-model"
	if err := res.VerifyCoverage(11, &tampered2); err == nil {
		t.Fatal("foreign-model certificate must be rejected")
	}
	tampered3 := *certA
	tampered3.Outcome = Violation
	if err := res.VerifyCoverage(11, &tampered3); err == nil {
		t.Fatal("outcome-altered certificate must be rejected")
	}
}

// 所有合法历史均违规：线程读取时的有效身份固定为 c/1，而策略要求 a/1。
func TestAllViolate(t *testing.T) {
	in := Input{Events: []Event{
		{ID: 1, Chain: 0, Thread: 1, Kind: EnterRequest, Token: tok("a", 1)},
		{ID: 2, Chain: 0, Thread: 1, Kind: Borrow, Token: tok("c", 1), BorrowID: 1},
		{ID: 3, Chain: 0, Thread: 1, Kind: ProtectedRead, Policy: pol("a", 1)},
		{ID: 4, Chain: 0, Thread: 1, Kind: Restore, BorrowID: 1},
	}}
	res, err := Analyze(in, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	rr := res.Reads[3]
	if rr.Class != AllViolate {
		t.Fatalf("expected AllViolate, got %v", rr.Class)
	}
	if !rr.ReachableViolate || rr.ReachableConform {
		t.Fatal("only violation facts should be reachable")
	}
	cert := res.Coverage[3]
	if cert == nil || cert.Outcome != Violation {
		t.Fatal("AllViolate read needs a violation coverage certificate")
	}
	if err := res.VerifyCoverage(3, cert); err != nil {
		t.Fatalf("violation certificate rejected: %v", err)
	}
	if err := res.VerifyWitness(3, Violation, rr.ViolatePrefix); err != nil {
		t.Fatalf("violation witness rejected: %v", err)
	}
	assertOracleAgrees(t, in, res)
}

// 双重消费句柄是非法非退出事件：偏序在该事件处成为死事件，存在停在它之前
// 的完整合法历史；依赖它的读取不可达，而不是被当作违规证据。
func TestConsumedHandleKillsHistory(t *testing.T) {
	in := Input{Events: []Event{
		{ID: 1, Chain: 0, Thread: 1, Kind: EnterRequest, Token: tok("a", 1)},
		{ID: 2, Chain: 0, Thread: 1, Kind: Spawn, Slot: 0, HandleGeneration: 1},
		{ID: 3, Chain: 0, Thread: 1, Kind: ResumeHandle, Slot: 0, HandleGeneration: 1},
		{ID: 4, Chain: 0, Thread: 1, Kind: ResumeHandle, Slot: 0, HandleGeneration: 1},
		{ID: 5, Chain: 0, Thread: 1, Kind: ProtectedRead, Policy: pol("a", 1), After: []int{4}},
	}}
	res, err := Analyze(in, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.HasLegalHistory {
		t.Fatal("maximal legal history should stop before the dead consumed-handle event")
	}
	if !hasIllegal(res, 4, ReasonConsumedHandle) {
		t.Fatalf("expected consumed handle rejection, got %+v", res.IllegalAttempts)
	}
	if res.Reads[5].Class != Unreachable {
		t.Fatalf("dependent read must be Unreachable, got %v", res.Reads[5].Class)
	}
}
