package cryptoplan

import (
	"reflect"
	"testing"
	"time"
)

// TestStableIDTieBreak 构造多个信封数与最弱等级完全相同的分区，验证规划器
// 按设备组稳定编号的规范字典序选首块编号最小的分区。
func TestStableIDTieBreak(t *testing.T) {
	host := newTestHost(t)
	release := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	nb, na := minuteTime(0)
	// 四个互相保密的身份，任意两组都不能共存，因此最优信封数固定为 4，
	// 且每个信封算法相同，存在大量同分分区。
	certs := make([]DeviceCert, 0, 4)
	edges := make([]AliasEdge, 0, 4)
	groups := make([]DeviceGroup, 0, 4)
	for i := 0; i < 4; i++ {
		principal := "p" + itoa(i)
		addr := "u" + itoa(i) + "@example.test"
		certs = append(certs, host.cert(t, "c"+itoa(i), principal,
			"d"+itoa(i), 1, deviceKey(t), []string{algMLKEM}, nb, na))
		edges = append(edges, host.edge(t, "e"+itoa(i), addr, principal, "", nb, na))
		groups = append(groups, DeviceGroup{
			StableID:  "g" + itoa(i),
			Recipient: addr,
			DeviceIDs: []string{"d" + itoa(i)},
		})
	}
	var pairs [][2]string
	for i := 0; i < 4; i++ {
		for j := i + 1; j < 4; j++ {
			pairs = append(pairs, [2]string{"p" + itoa(i), "p" + itoa(j)})
		}
	}
	req := &DeliveryRequest{
		Groups:              groups,
		ForbiddenPairs:      pairs,
		AlgorithmPreference: preference(),
		ReleaseTime:         release,
	}
	plan, err := PlanDelivery(snapshot(host, certs, edges), req, nil)
	if err != nil {
		t.Fatalf("PlanDelivery: %v", err)
	}
	if len(plan.Envelopes) != 4 {
		t.Fatalf("四组两两保密应得 4 个信封，实际 %d", len(plan.Envelopes))
	}
	wantIDs := [][]string{{"g0"}, {"g1"}, {"g2"}, {"g3"}}
	if !reflect.DeepEqual(envelopeGroups(plan), wantIDs) {
		t.Fatalf("决胜分区应为 %v，实际 %v", wantIDs, envelopeGroups(plan))
	}
	assertPlanVerifies(t, plan, snapshot(host, certs, edges), req, nil)
}

// TestLexicographicTieBreak 验证同一掩码存在多个信封数与等级完全相同的首块时，
// 规划器选编号序列字典序最小的分区（稳定编号决胜）。
func TestLexicographicTieBreak(t *testing.T) {
	host := newTestHost(t)
	release := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	nb, na := minuteTime(0)
	certs := make([]DeviceCert, 0, 3)
	edges := make([]AliasEdge, 0, 3)
	groups := make([]DeviceGroup, 0, 3)
	for i := 0; i < 3; i++ {
		principal := "q" + itoa(i)
		addr := "q" + itoa(i) + "@example.test"
		certs = append(certs, host.cert(t, "kc"+itoa(i), principal,
			"kd"+itoa(i), 1, deviceKey(t),
			[]string{algMLKEM, algXWing}, nb, na))
		edges = append(edges, host.edge(t, "ke"+itoa(i), addr, principal, "", nb, na))
		groups = append(groups, DeviceGroup{
			StableID:  "kg" + itoa(i),
			Recipient: addr,
			DeviceIDs: []string{"kd" + itoa(i)},
		})
	}
	req := &DeliveryRequest{
		Groups:              groups,
		AlgorithmPreference: preference(),
		ReleaseTime:         release,
	}
	plan, err := PlanDelivery(snapshot(host, certs, edges), req, nil)
	if err != nil {
		t.Fatalf("PlanDelivery: %v", err)
	}
	// 单信封即可覆盖全部三组，字典序最小的块是 {kg0 kg1 kg2}。
	want := [][]string{{"kg0", "kg1", "kg2"}}
	if !reflect.DeepEqual(envelopeGroups(plan), want) {
		t.Fatalf("决胜分区应为 %v，实际 %v", want, envelopeGroups(plan))
	}
	assertPlanVerifies(t, plan, snapshot(host, certs, edges), req, nil)
}

// TestLexSmallerAcrossEqualPartitions 构造两条信封数与最弱等级都相同的不同
// 双信封分区，强制 lexSmaller 比较首块编号并选字典序更小者。
func TestLexSmallerAcrossEqualPartitions(t *testing.T) {
	host := newTestHost(t)
	release := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	nb, na := minuteTime(0)
	// g0 仅 ML-KEM；g1 两者皆可；g2 仅 X-Wing；g3 两者皆可。
	spec := []struct {
		principal string
		algs      []string
	}{
		{"z0", []string{algMLKEM}},
		{"z1", []string{algMLKEM, algXWing}},
		{"z2", []string{algXWing}},
		{"z3", []string{algMLKEM, algXWing}},
	}
	certs := make([]DeviceCert, 0, len(spec))
	edges := make([]AliasEdge, 0, len(spec))
	groups := make([]DeviceGroup, 0, len(spec))
	for i, item := range spec {
		addr := "z" + itoa(i) + "@example.test"
		certs = append(certs, host.cert(t, "zc"+itoa(i), item.principal,
			"zd"+itoa(i), 1, deviceKey(t), item.algs, nb, na))
		edges = append(edges, host.edge(t, "ze"+itoa(i), addr, item.principal, "", nb, na))
		groups = append(groups, DeviceGroup{
			StableID:  "zg" + itoa(i),
			Recipient: addr,
			DeviceIDs: []string{"zd" + itoa(i)},
		})
	}
	req := &DeliveryRequest{
		Groups:              groups,
		AlgorithmPreference: preference(),
		ReleaseTime:         release,
	}
	plan, err := PlanDelivery(snapshot(host, certs, edges), req, nil)
	if err != nil {
		t.Fatalf("PlanDelivery: %v", err)
	}
	if len(plan.Envelopes) != 2 {
		t.Fatalf("最优信封数应为 2，实际 %d", len(plan.Envelopes))
	}
	assertPlanVerifies(t, plan, snapshot(host, certs, edges), req, nil)
}
