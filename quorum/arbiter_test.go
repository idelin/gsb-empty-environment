package quorum

import "testing"

// testConfig 验收拓扑:IDC-A 权重 2(主中心),IDC-B/IDC-C 权重各 1。
func testConfig() Config {
	return Config{
		Primary: "IDC-A",
		Regions: []Region{
			{ID: "IDC-A", Weight: 2, LocalNodes: 3, LocalThreshold: 2, Key: []byte("key-idc-a")},
			{ID: "IDC-B", Weight: 1, LocalNodes: 3, LocalThreshold: 2, Key: []byte("key-idc-b")},
			{ID: "IDC-C", Weight: 1, LocalNodes: 3, LocalThreshold: 2, Key: []byte("key-idc-c")},
		},
	}
}

func mustArbiter(t *testing.T) *Arbiter {
	t.Helper()
	a, err := NewArbiter(testConfig())
	if err != nil {
		t.Fatalf("NewArbiter: %v", err)
	}
	return a
}

// 正常拓扑:全局权重 4,多数派门限 3,决议携带可验证的签名证据。
func TestNormalTopologyQuorum(t *testing.T) {
	a := mustArbiter(t)
	d, err := a.Propose("set x=1")
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if d.Mode != ModeNormal {
		t.Fatalf("期望 NORMAL 拓扑,得到 %v", d.Mode)
	}
	if d.Quorum != 3 || d.Weight != 4 {
		t.Fatalf("门限/权重不符: quorum=%d weight=%d", d.Quorum, d.Weight)
	}
	if len(d.Votes) != 3 {
		t.Fatalf("正常拓扑应有 3 个区域投票,得到 %d", len(d.Votes))
	}
	if err := VerifyDecision(testConfig(), d); err != nil {
		t.Fatalf("签名证据验证失败: %v", err)
	}
}

// 验收边界 1:A 失联后,B+C(权重 2)在降级拓扑下达成临时多数派。
func TestDegradedQuorumWhenPrimaryLost(t *testing.T) {
	a := mustArbiter(t)
	if _, err := a.Propose("set x=1"); err != nil {
		t.Fatalf("正常决议失败: %v", err)
	}
	if err := a.SetReachable("IDC-A", false); err != nil {
		t.Fatal(err)
	}
	if a.Mode() != ModeDegraded {
		t.Fatal("A 失联后应进入降级拓扑")
	}
	d, err := a.Propose("set y=2")
	if err != nil {
		t.Fatalf("降级仲裁失败: %v", err)
	}
	if d.Mode != ModeDegraded {
		t.Fatalf("期望 DEGRADED,得到 %v", d.Mode)
	}
	// B+C 权重 2,临时多数派门限 2。
	if d.Weight != 2 || d.Quorum != 2 {
		t.Fatalf("降级多数派不符: weight=%d quorum=%d", d.Weight, d.Quorum)
	}
	for _, v := range d.Votes {
		if v.RegionID == "IDC-A" {
			t.Fatal("降级决议不应包含失联主中心的投票")
		}
	}
	// 围栏:降级决议抬升纪元。
	if d.Epoch <= 1 {
		t.Fatalf("降级决议应抬升纪元,得到 %d", d.Epoch)
	}
	if err := VerifyDecision(testConfig(), d); err != nil {
		t.Fatalf("降级决议签名证据验证失败: %v", err)
	}
}

// 验收边界 2:A 恢复后出示更高纪元重新接管,无状态冲突。
func TestPrimaryRecoveryTakeover(t *testing.T) {
	a := mustArbiter(t)
	if _, err := a.Propose("set x=1"); err != nil {
		t.Fatal(err)
	}
	_ = a.SetReachable("IDC-A", false)
	dDegraded, err := a.Propose("set y=2")
	if err != nil {
		t.Fatal(err)
	}

	// A 恢复,但出示的纪元不高于当前纪元 -> 拒绝接管(防止过期状态回灌)。
	_ = a.SetReachable("IDC-A", true)
	if err := a.RestorePrimary(dDegraded.Epoch); err == nil {
		t.Fatal("相同纪元接管应被拒绝")
	}
	// 出示更高纪元 -> 接管成功,回到正常拓扑。
	if err := a.RestorePrimary(dDegraded.Epoch + 1); err != nil {
		t.Fatalf("恢复接管失败: %v", err)
	}
	if a.Mode() != ModeNormal {
		t.Fatal("接管后应回到正常拓扑")
	}

	// 接管后新决议纪元更高;降级期间的决议仍然有效且日志有序。
	dAfter, err := a.Propose("set z=3")
	if err != nil {
		t.Fatal(err)
	}
	if dAfter.Epoch <= dDegraded.Epoch {
		t.Fatalf("接管后决议纪元 %d 应高于降级纪元 %d", dAfter.Epoch, dDegraded.Epoch)
	}
	log := a.Committed()
	if len(log) != 3 {
		t.Fatalf("committed log len = %d, want 3", len(log))
	}
	for i := 1; i < len(log); i++ {
		if log[i].Epoch < log[i-1].Epoch {
			t.Fatal("提交日志纪元应单调不减")
		}
	}
	for _, d := range log {
		if err := VerifyDecision(testConfig(), d); err != nil {
			t.Fatalf("历史决议证据验证失败: %v", err)
		}
	}
}

// 网络闪断防脑裂:仅剩单一区域时无法凑齐降级多数派,拒绝决议。
func TestNoQuorumOnPartition(t *testing.T) {
	a := mustArbiter(t)
	_ = a.SetReachable("IDC-A", false)
	_ = a.SetReachable("IDC-C", false)
	// 仅剩 IDC-B(权重1),降级门限基于 B+C 静态权重 = majority(2) = 2。
	if _, err := a.Propose("set x=9"); err == nil {
		t.Fatal("仅剩 IDC-B(权重1 < 门限2)不应达成多数派")
	}
	if len(a.Committed()) != 0 {
		t.Fatal("未达多数派时不应产生任何提交")
	}
}

// 本地多数派门限:区域本地赞成票不足门限时,证据验证失败。
func TestLocalThresholdEnforced(t *testing.T) {
	cfg := testConfig()
	a := mustArbiter(t)
	d, err := a.Propose("set x=1")
	if err != nil {
		t.Fatal(err)
	}
	for i := range d.Votes {
		if d.Votes[i].RegionID == "IDC-B" {
			d.Votes[i].LocalVotes = 1 // 低于门限 2,需重新签名? 不——验证应先因签名失败拒绝
			d.Votes[i].Signature = cfg.Regions[1].sign(d.Votes[i])
		}
	}
	if err := VerifyDecision(cfg, d); err == nil {
		t.Fatal("本地门限不足的投票应导致证据验证失败")
	}
}

// 签名证据防篡改:篡改投票内容或伪造签名均被拒绝。
func TestEvidenceTamperDetection(t *testing.T) {
	a := mustArbiter(t)
	d, err := a.Propose("set x=1")
	if err != nil {
		t.Fatal(err)
	}

	tampered := d
	tampered.Votes = append([]Vote(nil), d.Votes...)
	tampered.Votes[0].Proposal = "set x=evil"
	if err := VerifyDecision(testConfig(), tampered); err == nil {
		t.Fatal("篡改提案内容应被签名验证拒绝")
	}

	forged := d
	forged.Votes = append([]Vote(nil), d.Votes...)
	forged.Votes[0].Signature = "deadbeef"
	if err := VerifyDecision(testConfig(), forged); err == nil {
		t.Fatal("伪造签名应被拒绝")
	}
}

// 同纪元重复提交同一提案 -> 状态冲突保护。
func TestConflictDetection(t *testing.T) {
	a := mustArbiter(t)
	if _, err := a.Propose("set x=1"); err != nil {
		t.Fatal(err)
	}
	d := a.Committed()[0]
	if err := a.commit(d); err == nil {
		t.Fatal("同纪元重复提交应触发冲突错误")
	}
}
