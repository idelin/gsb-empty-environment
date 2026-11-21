package cryptoplan

import (
	"reflect"
	"testing"
	"time"
)

type groupSpec struct {
	id         string
	algMask    uint32
	principals []string
}

// greedyStrongest 模拟题目描述的锁定式贪心：当前信封一旦以最高等级算法建立，
// 就只接受能保持该等级的组，拒绝任何降级；没有组能加入时新开信封。
// 它是独立于规划器的朴素参考实现，用于构造“贪心多一份信封”的反例。
func greedyStrongest(groups []groupSpec, forbidden map[[2]string]bool) int {
	remaining := map[int]bool{}
	for i := range groups {
		remaining[i] = true
	}
	envelopes := 0
	for len(remaining) > 0 {
		envelopes++
		var lockedRank int
		var currentMask uint32
		seeded := false
		currentPrincipals := map[string]bool{}
		for {
			picked, bestRank := -1, 64
			for idx := range remaining {
				if conflicts(currentPrincipals, groups[idx].principals, forbidden) {
					continue
				}
				trial := groups[idx].algMask
				if seeded {
					trial = currentMask & groups[idx].algMask
				}
				if trial == 0 {
					continue
				}
				rank := lowestSetBit(trial)
				if seeded && rank != lockedRank {
					continue
				}
				if rank < bestRank {
					bestRank, picked = rank, idx
				}
			}
			if picked < 0 {
				break
			}
			if !seeded {
				currentMask = groups[picked].algMask
				lockedRank = lowestSetBit(currentMask)
				seeded = true
			} else {
				currentMask &= groups[picked].algMask
			}
			delete(remaining, picked)
			for _, principal := range groups[picked].principals {
				currentPrincipals[principal] = true
			}
		}
		if !seeded {
			return len(groups) + 1
		}
	}
	return envelopes
}

func conflicts(current map[string]bool, principals []string,
	forbidden map[[2]string]bool) bool {
	for left := range current {
		for _, right := range principals {
			a, b := left, right
			if a > b {
				a, b = b, a
			}
			if forbidden[[2]string{a, b}] {
				return true
			}
		}
	}
	return false
}

func TestGreedyStrongestWastesEnvelope(t *testing.T) {
	rank := map[string]int{algMLKEM: 0, algXWing: 1, algRSA: 2}
	high := uint32(1) << rank[algMLKEM]
	mid := uint32(1) << rank[algXWing]
	// g1 仅支持 ML-KEM；g2 两者皆可；g3 仅 X-Wing。
	// 锁定贪心：信封1 锁定 ML-KEM，装入 g1,g2 后拒绝 g3 → 再开信封2，共 2 份。
	// 最优：整体降级为 X-Wing（g1 也支持 X-Wing 的证书见下），单信封即可。
	specs := []groupSpec{
		{id: "g1", algMask: high | mid, principals: []string{"p1"}},
		{id: "g2", algMask: high | mid, principals: []string{"p2"}},
		{id: "g3", algMask: mid, principals: []string{"p3"}},
	}
	// 为让贪心先锁定 ML-KEM，把 g1 视为“最高可用算法为 ML-KEM”的锚点：
	// 锁定规则下 g1/g2 保持 rank0，g3 无法并入。
	specs[0].algMask = high
	if got := greedyStrongest(specs, nil); got != 2 {
		t.Fatalf("锁定贪心对照基线应为 2 个信封，实际 %d", got)
	}

	host := newTestHost(t)
	release := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	notBefore, notAfter := minuteTime(0)
	// 规划器视图里 g1 同时持有 ML-KEM 与 X-Wing 授权，因此单信封 X-Wing 可行。
	certs := []DeviceCert{
		host.cert(t, "c1", "p1", "d1", 1, deviceKey(t),
			[]string{algMLKEM, algXWing}, notBefore, notAfter),
		host.cert(t, "c2", "p2", "d2", 1, deviceKey(t),
			[]string{algMLKEM, algXWing}, notBefore, notAfter),
		host.cert(t, "c3", "p3", "d3", 1, deviceKey(t),
			[]string{algXWing}, notBefore, notAfter),
	}
	edges := []AliasEdge{
		host.edge(t, "e1", "g1@example.test", "p1", "", notBefore, notAfter),
		host.edge(t, "e2", "g2@example.test", "p2", "", notBefore, notAfter),
		host.edge(t, "e3", "g3@example.test", "p3", "", notBefore, notAfter),
	}
	req := &DeliveryRequest{
		Groups: []DeviceGroup{
			{StableID: "g1", Recipient: "g1@example.test", DeviceIDs: []string{"d1"}},
			{StableID: "g2", Recipient: "g2@example.test", DeviceIDs: []string{"d2"}},
			{StableID: "g3", Recipient: "g3@example.test", DeviceIDs: []string{"d3"}},
		},
		AlgorithmPreference: preference(),
		ReleaseTime:         release,
	}
	plan, err := PlanDelivery(snapshot(host, certs, edges), req, nil)
	if err != nil {
		t.Fatalf("PlanDelivery: %v", err)
	}
	if len(plan.Envelopes) != 1 {
		t.Fatalf("最优方案应只有 1 个信封（整体降级为 X-Wing），实际 %d",
			len(plan.Envelopes))
	}
	if plan.Envelopes[0].Algorithm != algXWing {
		t.Fatalf("单信封最弱算法应为 X-Wing，实际 %s", plan.Envelopes[0].Algorithm)
	}
	wantGroups := []string{"g1", "g2", "g3"}
	if !reflect.DeepEqual(plan.Envelopes[0].GroupStableIDs, wantGroups) {
		t.Fatalf("信封应覆盖全部三组: %v", plan.Envelopes[0].GroupStableIDs)
	}
	assertPlanVerifies(t, plan, snapshot(host, certs, edges), req, nil)
}

func TestWeakestEnvelopeLevelBreaksTie(t *testing.T) {
	host := newTestHost(t)
	release := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	notBefore, notAfter := minuteTime(0)
	certs := []DeviceCert{
		host.cert(t, "c1", "p1", "d1", 1, deviceKey(t),
			[]string{algMLKEM, algXWing}, notBefore, notAfter),
		host.cert(t, "c2", "p2", "d2", 1, deviceKey(t),
			[]string{algXWing}, notBefore, notAfter),
		host.cert(t, "c3", "p3", "d3", 1, deviceKey(t),
			[]string{algMLKEM, algXWing}, notBefore, notAfter),
	}
	edges := []AliasEdge{
		host.edge(t, "e1", "a1@example.test", "p1", "", notBefore, notAfter),
		host.edge(t, "e2", "a2@example.test", "p2", "", notBefore, notAfter),
		host.edge(t, "e3", "a3@example.test", "p3", "", notBefore, notAfter),
	}
	req := &DeliveryRequest{
		Groups: []DeviceGroup{
			{StableID: "g1", Recipient: "a1@example.test", DeviceIDs: []string{"d1"}},
			{StableID: "g2", Recipient: "a2@example.test", DeviceIDs: []string{"d2"}},
			{StableID: "g3", Recipient: "a3@example.test", DeviceIDs: []string{"d3"}},
		},
		ForbiddenPairs:      [][2]string{{"p1", "p3"}},
		AlgorithmPreference: preference(),
		ReleaseTime:         release,
	}
	plan, err := PlanDelivery(snapshot(host, certs, edges), req, nil)
	if err != nil {
		t.Fatalf("PlanDelivery: %v", err)
	}
	if len(plan.Envelopes) != 2 {
		t.Fatalf("p1/p3 保密对应得 2 个信封，实际 %d", len(plan.Envelopes))
	}
	worst := -1
	for _, envelope := range plan.Envelopes {
		worst = maxInt(worst, rankOf(t, preference(), envelope.Algorithm))
	}
	if worst != rankOf(t, preference(), algXWing) {
		t.Fatalf("最弱信封等级应恰好为 X-Wing，实际 rank=%d", worst)
	}
	assertPlanVerifies(t, plan, snapshot(host, certs, edges), req, nil)
}

func preference() []string {
	return []string{algMLKEM, algXWing, algRSA}
}

func rankOf(t *testing.T, prefs []string, algorithm string) int {
	t.Helper()
	for i, alg := range prefs {
		if alg == algorithm {
			return i
		}
	}
	t.Fatalf("未知算法 %s", algorithm)
	return -1
}
