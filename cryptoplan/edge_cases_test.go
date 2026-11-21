package cryptoplan

import (
	"errors"
	"testing"
	"time"
)

// TestHalfOpenIntervalEndpoints 验证有效期是半开区间 [NotBefore, NotAfter)：
// 起点可用、终点失效；两张仅在端点相接的证书不能在同一发布时间同时授权。
func TestHalfOpenIntervalEndpoints(t *testing.T) {
	host := newTestHost(t)
	base := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	key := deviceKey(t)
	first := host.cert(t, "c-first", "p1", "d1", 1, key,
		[]string{algMLKEM}, base.Add(-time.Hour), base)
	second := host.cert(t, "c-second", "p1", "d1", 2, key,
		[]string{algMLKEM}, base, base.Add(time.Hour))
	edge := host.edge(t, "e1", "a1@example.test", "p1", "",
		base.Add(-time.Hour), base.Add(time.Hour))

	snap := snapshot(host, []DeviceCert{first, second}, []AliasEdge{edge})
	reqAt := func(release time.Time) *DeliveryRequest {
		return &DeliveryRequest{
			Groups: []DeviceGroup{
				{StableID: "g1", Recipient: "a1@example.test", DeviceIDs: []string{"d1"}},
			},
			AlgorithmPreference: preference(),
			ReleaseTime:         release,
		}
	}

	if _, err := PlanDelivery(snap, reqAt(base.Add(-time.Minute)), nil); err != nil {
		t.Fatalf("起点之后、终点之前应可用: %v", err)
	}
	if _, err := PlanDelivery(snap, reqAt(base), nil); err != nil {
		t.Fatalf("恰在第二张证书起点应可用: %v", err)
	}
	_, err := PlanDelivery(snap, reqAt(base.Add(time.Hour)), nil)
	if !errors.Is(err, ErrNoFeasiblePlan) {
		t.Fatalf("恰在证书终点应失效，得到 %v", err)
	}

	// 别名边同样是半开：终点时刻归并证据消失。
	_, err = PlanDelivery(snap, reqAt(base.Add(time.Hour)), nil)
	if !errors.Is(err, ErrNoFeasiblePlan) {
		t.Fatalf("别名边终点应无有效证据，得到 %v", err)
	}
}

// TestSameDisplayNameDifferentIdentity 验证显示名相同但签名边指向不同身份时，
// 两个组必须被当作两个独立主体，显示名不能充当归并证据。
func TestSameDisplayNameDifferentIdentity(t *testing.T) {
	host := newTestHost(t)
	release := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	nb, na := minuteTime(0)
	certs := []DeviceCert{
		host.cert(t, "c1", "id-alice-a", "d1", 1, deviceKey(t),
			[]string{algMLKEM}, nb, na),
		host.cert(t, "c2", "id-alice-b", "d2", 1, deviceKey(t),
			[]string{algMLKEM}, nb, na),
	}
	edges := []AliasEdge{
		host.edge(t, "e1", "alice@a.test", "id-alice-a", "Same Display Name", nb, na),
		host.edge(t, "e2", "alice@b.test", "id-alice-b", "Same Display Name", nb, na),
	}
	req := &DeliveryRequest{
		Groups: []DeviceGroup{
			{StableID: "g1", Recipient: "alice@a.test",
				DisplayName: "Same Display Name", DeviceIDs: []string{"d1"}},
			{StableID: "g2", Recipient: "alice@b.test",
				DisplayName: "Same Display Name", DeviceIDs: []string{"d2"}},
		},
		ForbiddenPairs:      [][2]string{{"id-alice-a", "id-alice-b"}},
		AlgorithmPreference: preference(),
		ReleaseTime:         release,
	}
	plan, err := PlanDelivery(snapshot(host, certs, edges), req, nil)
	if err != nil {
		t.Fatalf("PlanDelivery: %v", err)
	}
	if len(plan.Envelopes) != 2 {
		t.Fatalf("同名异身份且互为保密对应拆为 2 个信封，实际 %d", len(plan.Envelopes))
	}
	joined := map[string]bool{}
	for _, envelope := range plan.Envelopes {
		for _, principal := range envelope.Principals {
			if joined[principal] {
				t.Fatalf("身份 %s 出现在多个信封的合并披露中", principal)
			}
			joined[principal] = true
		}
	}
	assertPlanVerifies(t, plan, snapshot(host, certs, edges), req, nil)
}

// TestBCCIdentityNotDisclosed 验证盲抄送身份与显式收件人禁止同信封披露。
func TestBCCIdentityNotDisclosed(t *testing.T) {
	host := newTestHost(t)
	release := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	nb, na := minuteTime(0)
	certs := []DeviceCert{
		host.cert(t, "c-to", "id-to", "d1", 1, deviceKey(t),
			[]string{algMLKEM}, nb, na),
		host.cert(t, "c-bcc", "id-bcc", "d2", 1, deviceKey(t),
			[]string{algMLKEM}, nb, na),
	}
	edges := []AliasEdge{
		host.edge(t, "e-to", "to@example.test", "id-to", "", nb, na),
		host.edge(t, "e-bcc", "bcc@example.test", "id-bcc", "", nb, na),
	}
	req := &DeliveryRequest{
		Groups: []DeviceGroup{
			{StableID: "g-to", Recipient: "to@example.test", DeviceIDs: []string{"d1"}},
			{StableID: "g-bcc", Recipient: "bcc@example.test", DeviceIDs: []string{"d2"}},
		},
		ForbiddenPairs:      [][2]string{{"id-bcc", "id-to"}},
		AlgorithmPreference: preference(),
		ReleaseTime:         release,
	}
	plan, err := PlanDelivery(snapshot(host, certs, edges), req, nil)
	if err != nil {
		t.Fatalf("PlanDelivery: %v", err)
	}
	for _, envelope := range plan.Envelopes {
		if len(envelope.Principals) != 1 {
			t.Fatalf("BCC 身份不得与显式收件人共同披露: %v", envelope.Principals)
		}
	}
	assertPlanVerifies(t, plan, snapshot(host, certs, edges), req, nil)
}
