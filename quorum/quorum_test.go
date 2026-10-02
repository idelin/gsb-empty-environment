package quorum

import (
	"errors"
	"strings"
	"testing"
)

var (
	regionKeys = map[string][]byte{
		"IDC-A": []byte("key-idc-a"),
		"IDC-B": []byte("key-idc-b"),
		"IDC-C": []byte("key-idc-c"),
	}
	evidenceKey = []byte("arbiter-evidence-key")
)

// 验收拓扑：IDC-A 权重 2(主中心)，IDC-B/C 权重各 1。
func newArbiter(t *testing.T) *Arbiter {
	t.Helper()
	a := NewArbiter(evidenceKey)
	regions := []Region{
		{ID: "IDC-A", Weight: 2, LocalThreshold: 2, Primary: true},
		{ID: "IDC-B", Weight: 1, LocalThreshold: 1},
		{ID: "IDC-C", Weight: 1, LocalThreshold: 1},
	}
	for _, r := range regions {
		if err := a.AddRegion(r, regionKeys[r.ID]); err != nil {
			t.Fatalf("AddRegion %s: %v", r.ID, err)
		}
	}
	// 上报本地多数派确认(分层仲裁下层)
	for id, acks := range map[string]int{"IDC-A": 2, "IDC-B": 1, "IDC-C": 1} {
		if err := a.ReportLocalAcks(id, acks); err != nil {
			t.Fatalf("ReportLocalAcks %s: %v", id, err)
		}
	}
	return a
}

func verify(t *testing.T, a *Arbiter, d *Decision) {
	t.Helper()
	if err := Verify(d, a.Snapshot(), regionKeys, evidenceKey); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestNormalTopologyQuorum(t *testing.T) {
	a := newArbiter(t)

	// A(2)+B(1)=3 >= 门限 3，达成全局多数派
	d, err := a.Propose(1, "v1", []string{"IDC-A", "IDC-B"})
	if err != nil {
		t.Fatalf("A+B should reach quorum: %v", err)
	}
	if d.Mode != ModeNormal {
		t.Fatalf("mode = %v, want normal", d.Mode)
	}
	verify(t, a, d)

	// B(1)+C(1)=2 < 门限 3，正常拓扑下不构成多数派
	if _, err := a.Propose(2, "v2", []string{"IDC-B", "IDC-C"}); !errors.Is(err, ErrNoQuorum) {
		t.Fatalf("B+C in normal mode: err = %v, want ErrNoQuorum", err)
	}
}

func TestDegradedTopologyOnPrimaryLoss(t *testing.T) {
	a := newArbiter(t)
	if _, err := a.Propose(1, "v1", []string{"IDC-A", "IDC-B"}); err != nil {
		t.Fatal(err)
	}

	// 主中心 A 失联 → 自动降级
	if err := a.SetReachable("IDC-A", false); err != nil {
		t.Fatal(err)
	}
	if a.Mode() != ModeDegraded {
		t.Fatalf("mode = %v, want degraded", a.Mode())
	}

	// B+C 在降级拓扑中达成临时多数派(2 >= 门限 2)
	d, err := a.Propose(2, "v2-degraded", []string{"IDC-B", "IDC-C"})
	if err != nil {
		t.Fatalf("B+C should reach temporary majority in degraded mode: %v", err)
	}
	if d.Mode != ModeDegraded {
		t.Fatalf("mode = %v, want degraded", d.Mode)
	}
	verify(t, a, d)

	// 降级拓扑中被隔离的主中心不得投票
	if err := a.SetReachable("IDC-A", true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Propose(3, "v3", []string{"IDC-A", "IDC-B"}); !errors.Is(err, ErrPrimaryDegraded) {
		t.Fatalf("primary voting while degraded: err = %v, want ErrPrimaryDegraded", err)
	}
}

func TestPrimaryRecoveryTakeoverNoConflict(t *testing.T) {
	a := newArbiter(t)
	if _, err := a.Propose(1, "v1", []string{"IDC-A", "IDC-B"}); err != nil {
		t.Fatal(err)
	}
	if err := a.SetReachable("IDC-A", false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Propose(2, "v2-degraded", []string{"IDC-B", "IDC-C"}); err != nil {
		t.Fatal(err)
	}

	// A 恢复可达，但出示不高于当前纪元的编号 → 拒绝接管
	if err := a.SetReachable("IDC-A", true); err != nil {
		t.Fatal(err)
	}
	if err := a.Takeover(2); !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("Takeover(2): err = %v, want ErrStaleEpoch", err)
	}
	if a.Mode() != ModeDegraded {
		t.Fatalf("mode = %v, want still degraded", a.Mode())
	}

	// 出示更高纪元 → 重新接管，恢复正常拓扑
	if err := a.Takeover(3); err != nil {
		t.Fatalf("Takeover(3): %v", err)
	}
	if a.Mode() != ModeNormal {
		t.Fatalf("mode = %v, want normal", a.Mode())
	}

	// 接管后 A+B 重新达成全局多数派，无状态冲突
	d, err := a.Propose(4, "v4-after-recovery", []string{"IDC-A", "IDC-C"})
	if err != nil {
		t.Fatalf("post-recovery proposal: %v", err)
	}
	verify(t, a, d)

	// 旧纪元的提案被围栏(fencing)拒绝
	if _, err := a.Propose(2, "stale", []string{"IDC-A", "IDC-B"}); !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("stale epoch proposal: err = %v, want ErrStaleEpoch", err)
	}
}

func TestSameEpochConflictingValueRejected(t *testing.T) {
	a := newArbiter(t)
	if _, err := a.Propose(1, "v1", []string{"IDC-A", "IDC-B"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Propose(1, "v1-other", []string{"IDC-A", "IDC-C"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	// 同纪元同值幂等重放允许
	if _, err := a.Propose(1, "v1", []string{"IDC-A", "IDC-B"}); err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
}

func TestLocalMajorityGate(t *testing.T) {
	a := newArbiter(t)
	// IDC-A 本地确认不足门限 2 → 投票无效
	if err := a.ReportLocalAcks("IDC-A", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Propose(1, "v1", []string{"IDC-A", "IDC-B"}); !errors.Is(err, ErrNoLocalQuorum) {
		t.Fatalf("err = %v, want ErrNoLocalQuorum", err)
	}
}

func TestUnreachableVoterRejected(t *testing.T) {
	a := newArbiter(t)
	if err := a.SetReachable("IDC-C", false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Propose(1, "v1", []string{"IDC-A", "IDC-C"}); !errors.Is(err, ErrRegionDown) {
		t.Fatalf("err = %v, want ErrRegionDown", err)
	}
}

func TestEvidenceTamperingDetected(t *testing.T) {
	a := newArbiter(t)
	d, err := a.Propose(1, "v1", []string{"IDC-A", "IDC-B"})
	if err != nil {
		t.Fatal(err)
	}
	verify(t, a, d)

	// 篡改决议值
	bad := *d
	bad.Value = "forged"
	if err := Verify(&bad, a.Snapshot(), regionKeys, evidenceKey); !errors.Is(err, ErrBadEvidence) {
		t.Fatalf("tampered value: err = %v, want ErrBadEvidence", err)
	}

	// 篡改投票签名
	bad2 := *d
	bad2.Votes = append([]Vote(nil), d.Votes...)
	bad2.Votes[0].Signature = append([]byte(nil), d.Votes[0].Signature...)
	bad2.Votes[0].Signature[0] ^= 0xff
	if err := Verify(&bad2, a.Snapshot(), regionKeys, evidenceKey); !errors.Is(err, ErrBadEvidence) {
		t.Fatalf("tampered vote signature: err = %v, want ErrBadEvidence", err)
	}

	// 错误的证据密钥
	if err := Verify(d, a.Snapshot(), regionKeys, []byte("wrong-key")); !errors.Is(err, ErrBadEvidence) {
		t.Fatalf("wrong evidence key: err = %v, want ErrBadEvidence", err)
	}

	// 权重不足(去掉一票) → 证据无效
	bad3 := *d
	bad3.Votes = bad3.Votes[:1]
	if err := Verify(&bad3, a.Snapshot(), regionKeys, evidenceKey); err == nil ||
		!strings.Contains(err.Error(), "quorum") {
		t.Fatalf("dropped vote: err = %v, want quorum/evidence failure", err)
	}
}

func TestTakeoverRequiresReachablePrimary(t *testing.T) {
	a := newArbiter(t)
	if err := a.SetReachable("IDC-A", false); err != nil {
		t.Fatal(err)
	}
	if err := a.Takeover(5); !errors.Is(err, ErrPrimaryDown) {
		t.Fatalf("err = %v, want ErrPrimaryDown", err)
	}
}
