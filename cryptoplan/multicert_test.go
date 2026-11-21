package cryptoplan

import (
	"errors"
	"testing"
	"time"
)

// TestMultiCertDeviceEnvelope 验证同一设备的多算法证书在信封中只携带授权该
// 信封算法的那张，独立投递端仍能完整验证设备覆盖。
func TestMultiCertDeviceEnvelope(t *testing.T) {
	host := newTestHost(t)
	release := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	nb, na := minuteTime(0)
	key := deviceKey(t)
	certML := host.cert(t, "c-ml", "p1", "d1", 1, key,
		[]string{algMLKEM}, nb, na)
	certXW := host.cert(t, "c-xw", "p1", "d1", 2, key,
		[]string{algXWing}, nb, na)
	edge := host.edge(t, "e1", "a1@example.test", "p1", "", nb, na)
	snap := snapshot(host, []DeviceCert{certML, certXW}, []AliasEdge{edge})
	req := &DeliveryRequest{
		Groups: []DeviceGroup{
			{StableID: "g1", Recipient: "a1@example.test", DeviceIDs: []string{"d1"}},
		},
		AlgorithmPreference: preference(),
		ReleaseTime:         release,
	}
	plan, err := PlanDelivery(snap, req, nil)
	if err != nil {
		t.Fatalf("PlanDelivery: %v", err)
	}
	if len(plan.Envelopes) != 1 || plan.Envelopes[0].Algorithm != algMLKEM {
		t.Fatalf("应选最高等级 ML-KEM 单信封: %+v", plan.Envelopes)
	}
	envelope := plan.Envelopes[0]
	if len(envelope.Certs) != 1 || envelope.Certs[0].CertID != "c-ml" {
		t.Fatalf("信封应只携带 ML-KEM 授权证书，实际 %v", envelope.Certs)
	}
	assertPlanVerifies(t, plan, snap, req, nil)

	// 吊销 ML-KEM 证书后，规划必须改用 X-Wing，不能静默沿用旧方案。
	revoked := func(certID string, _ time.Time) (bool, error) {
		return certID == "c-ml", nil
	}
	replanned, err := PlanDelivery(snap, req, revoked)
	if err != nil {
		t.Fatalf("吊销后应能用 X-Wing 重新规划: %v", err)
	}
	if replanned.Envelopes[0].Algorithm != algXWing {
		t.Fatalf("吊销 ML-KEM 后应降级为 X-Wing 重新规划，实际 %s",
			replanned.Envelopes[0].Algorithm)
	}
	if err := Recheck(plan, snap, req, revoked); !errors.Is(err, ErrRevoked) {
		t.Fatalf("旧方案复检应报 ErrRevoked，得到 %v", err)
	}
}
