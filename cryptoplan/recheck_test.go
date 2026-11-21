package cryptoplan

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func itoa(i int) string {
	return fmt.Sprintf("%d", i)
}

func fmtAddr(i int) string {
	return fmt.Sprintf("user-%d@example.test", i)
}

func basicPlanFixture(t *testing.T) (*testHost, *Snapshot, *DeliveryRequest, *Plan) {
	t.Helper()
	host := newTestHost(t)
	release := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	nb, na := minuteTime(0)
	certs := []DeviceCert{
		host.cert(t, "c1", "p1", "d1", 1, deviceKey(t),
			[]string{algMLKEM, algXWing}, nb, na),
		host.cert(t, "c2", "p2", "d2", 1, deviceKey(t),
			[]string{algMLKEM}, nb, na),
	}
	edges := []AliasEdge{
		host.edge(t, "e1", "a1@example.test", "p1", "", nb, na),
		host.edge(t, "e2", "a2@example.test", "p2", "", nb, na),
	}
	req := &DeliveryRequest{
		Groups: []DeviceGroup{
			{StableID: "g1", Recipient: "a1@example.test", DeviceIDs: []string{"d1"}},
			{StableID: "g2", Recipient: "a2@example.test", DeviceIDs: []string{"d2"}},
		},
		AlgorithmPreference: preference(),
		ReleaseTime:         release,
	}
	snap := snapshot(host, certs, edges)
	plan, err := PlanDelivery(snap, req, nil)
	if err != nil {
		t.Fatalf("PlanDelivery: %v", err)
	}
	return host, snap, req, plan
}

func TestRecheckRejectsRevocation(t *testing.T) {
	_, snap, req, plan := basicPlanFixture(t)
	if err := Recheck(plan, snap, req, nil); err != nil {
		t.Fatalf("未吊销时复检应通过: %v", err)
	}
	revoked := func(certID string, _ time.Time) (bool, error) {
		return certID == "c2", nil
	}
	err := Recheck(plan, snap, req, revoked)
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("证书吊销后应返回 ErrRevoked，得到 %v", err)
	}
	// 规划阶段遇到吊销也不能私自兜底：被吊销证书是唯一授权时直接无可行方案。
	if _, err := PlanDelivery(snap, req, revoked); !errors.Is(err, ErrNoFeasiblePlan) {
		t.Fatalf("吊销导致无授权时应 ErrNoFeasiblePlan，得到 %v", err)
	}
}

func TestRecheckRejectsGroupChange(t *testing.T) {
	_, snap, req, plan := basicPlanFixture(t)
	changed := *req
	changed.Groups = append([]DeviceGroup(nil), req.Groups...)
	changed.Groups[0].DeviceIDs = append(changed.Groups[0].DeviceIDs, "d-extra")
	err := Recheck(plan, snap, &changed, nil)
	if !errors.Is(err, ErrInputChanged) {
		t.Fatalf("收件组变更应返回 ErrInputChanged，得到 %v", err)
	}

	removed := *req
	removed.Groups = removed.Groups[:1]
	if err := Recheck(plan, snap, &removed, nil); !errors.Is(err, ErrInputChanged) {
		t.Fatalf("收件组减少应返回 ErrInputChanged，得到 %v", err)
	}
}

func TestRecheckRejectsCertTamper(t *testing.T) {
	host, snap, req, plan := basicPlanFixture(t)
	tampered := *snap
	tampered.DeviceCerts = append([]DeviceCert(nil), snap.DeviceCerts...)
	tampered.DeviceCerts[0].Algorithms = []string{algRSA}
	err := Recheck(plan, &tampered, req, nil)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("篡改证书载荷应签名失败，得到 %v", err)
	}

	// 用另一把宿主密钥重签后的证书也无法通过原宿主公钥验证。
	otherHost := newTestHost(t)
	rogue := otherHost.cert(t, "c1", "p1", "d1", 1,
		snap.DeviceCerts[0].PublicKey, []string{algMLKEM},
		snap.DeviceCerts[0].NotBefore, snap.DeviceCerts[0].NotAfter)
	rogueSnap := snapshot(host,
		append([]DeviceCert{rogue}, snap.DeviceCerts[1:]...), snap.AliasEdges)
	if err := Recheck(plan, rogueSnap, req, nil); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("非宿主签发的证书应被拒绝，得到 %v", err)
	}
}

func TestRecheckRejectsEdgeTamper(t *testing.T) {
	_, snap, req, plan := basicPlanFixture(t)
	tampered := *snap
	tampered.AliasEdges = append([]AliasEdge(nil), snap.AliasEdges...)
	tampered.AliasEdges[0].PrincipalID = "p-attacker"
	err := Recheck(plan, &tampered, req, nil)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("篡改别名边应签名失败，得到 %v", err)
	}
}

func TestCrossIdentityKeyIDRejected(t *testing.T) {
	host := newTestHost(t)
	release := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	nb, na := minuteTime(0)
	shared := deviceKey(t)
	certs := []DeviceCert{
		host.cert(t, "c1", "p1", "d1", 1, shared, []string{algMLKEM}, nb, na),
		host.cert(t, "c2", "p2", "d2", 1, shared, []string{algMLKEM}, nb, na),
	}
	edges := []AliasEdge{
		host.edge(t, "e1", "a1@example.test", "p1", "", nb, na),
		host.edge(t, "e2", "a2@example.test", "p2", "", nb, na),
	}
	req := &DeliveryRequest{
		Groups: []DeviceGroup{
			{StableID: "g1", Recipient: "a1@example.test", DeviceIDs: []string{"d1"}},
			{StableID: "g2", Recipient: "a2@example.test", DeviceIDs: []string{"d2"}},
		},
		AlgorithmPreference: preference(),
		ReleaseTime:         release,
	}
	_, err := PlanDelivery(snapshot(host, certs, edges), req, nil)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("跨身份共用同一公钥应被拒绝，得到 %v", err)
	}
}

func TestSameIdentitySharedKeyAcrossGroups(t *testing.T) {
	host := newTestHost(t)
	release := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	nb, na := minuteTime(0)
	shared := deviceKey(t)
	certs := []DeviceCert{
		host.cert(t, "c1", "p1", "d1", 1, shared,
			[]string{algMLKEM, algXWing}, nb, na),
	}
	edges := []AliasEdge{
		host.edge(t, "e1", "alias1@example.test", "p1", "", nb, na),
		host.edge(t, "e2", "alias2@example.test", "p1", "", nb, na),
	}
	req := &DeliveryRequest{
		Groups: []DeviceGroup{
			{StableID: "g1", Recipient: "alias1@example.test", DeviceIDs: []string{"d1"}},
			{StableID: "g2", Recipient: "alias2@example.test", DeviceIDs: []string{"d1"}},
		},
		AlgorithmPreference: preference(),
		ReleaseTime:         release,
	}
	plan, err := PlanDelivery(snapshot(host, certs, edges), req, nil)
	if err != nil {
		t.Fatalf("同一主体跨组共用密钥应允许: %v", err)
	}
	if len(plan.Envelopes) != 1 {
		t.Fatalf("同主体两组应能合并为单信封，实际 %d", len(plan.Envelopes))
	}
	assertPlanVerifies(t, plan, snapshot(host, certs, edges), req, nil)
}

func TestTooManyGroupsRejected(t *testing.T) {
	host := newTestHost(t)
	release := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	nb, na := minuteTime(0)
	groups := make([]DeviceGroup, MaxDeviceGroups+1)
	certs := make([]DeviceCert, 0, MaxDeviceGroups+1)
	edges := make([]AliasEdge, 0, MaxDeviceGroups+1)
	for i := range groups {
		addr := fmtAddr(i)
		key := deviceKey(t)
		certs = append(certs, host.cert(t,
			"c"+itoa(i), "p"+itoa(i), "d"+itoa(i), 1, key,
			[]string{algMLKEM}, nb, na))
		edges = append(edges, host.edge(t, "e"+itoa(i), addr, "p"+itoa(i), "", nb, na))
		groups[i] = DeviceGroup{
			StableID:  "g" + itoa(i),
			Recipient: addr,
			DeviceIDs: []string{"d" + itoa(i)},
		}
	}
	req := &DeliveryRequest{
		Groups:              groups,
		AlgorithmPreference: preference(),
		ReleaseTime:         release,
	}
	_, err := PlanDelivery(snapshot(host, certs, edges), req, nil)
	if !errors.Is(err, ErrTooManyGroups) {
		t.Fatalf("超过 %d 组应返回 ErrTooManyGroups，得到 %v", MaxDeviceGroups, err)
	}
}
