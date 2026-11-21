package cryptoplan

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	mathrand "math/rand"
	"testing"
	"time"
)

// bruteForcePlan 用受限增长字符串枚举 n 个组的全部集合分区，独立计算
// 信封数最少、最弱等级最高、规范分区字典序最小的最优解。
// 仅用于小规模（n<=6）对照规划器，不依赖规划器内部实现。
func bruteForcePlan(groups []bruteGroup, rank map[string]int,
	forbidden map[[2]string]bool) (int, int, [][]int) {
	n := len(groups)
	bestCount, bestWorst := n+1, len(rank)+1
	var bestPartition [][]int

	blockAlg := func(members []int) (bool, int) {
		shared := uint32(0)
		principals := map[string]bool{}
		for _, idx := range members {
			if shared == 0 {
				shared = groups[idx].algMask
			} else {
				shared &= groups[idx].algMask
			}
			if shared == 0 {
				return false, 0
			}
			for _, principal := range groups[idx].principals {
				principals[principal] = true
			}
		}
		if shared == 0 {
			return false, 0
		}
		ps := make([]string, 0, len(principals))
		for p := range principals {
			ps = append(ps, p)
		}
		for i := 0; i < len(ps); i++ {
			for j := i + 1; j < len(ps); j++ {
				a, b := ps[i], ps[j]
				if a > b {
					a, b = b, a
				}
				if forbidden[[2]string{a, b}] {
					return false, 0
				}
			}
		}
		return true, trailingRank(shared)
	}

	var assign []int
	var walk func(idx, maxBlock int)
	walk = func(idx, maxBlock int) {
		if idx == n {
			blocks := map[int][]int{}
			for i, b := range assign {
				blocks[b] = append(blocks[b], i)
			}
			partition := make([][]int, 0, len(blocks))
			worst := -1
			for b := 0; b <= maxBlock; b++ {
				members, ok := blocks[b]
				if !ok {
					continue
				}
				feasible, r := blockAlg(members)
				if !feasible {
					return
				}
				if r > worst {
					worst = r
				}
				partition = append(partition, append([]int(nil), members...))
			}
			canonical := canonicalPartition(partition)
			better := len(canonical) < bestCount ||
				(len(canonical) == bestCount && worst < bestWorst) ||
				(len(canonical) == bestCount && worst == bestWorst &&
					(bestPartition == nil ||
						compareIntPartition(canonical, bestPartition) < 0))
			if better {
				bestCount, bestWorst, bestPartition = len(canonical), worst, canonical
			}
			return
		}
		for b := 0; b <= maxBlock+1; b++ {
			assign = append(assign, b)
			nextMax := maxBlock
			if b > maxBlock {
				nextMax = b
			}
			walk(idx+1, nextMax)
			assign = assign[:len(assign)-1]
		}
	}
	walk(0, -1)
	if bestCount > n {
		return -1, -1, nil
	}
	return bestCount, bestWorst, bestPartition
}

func trailingRank(mask uint32) int {
	return lowestSetBit(mask)
}

func canonicalPartition(partition [][]int) [][]int {
	out := append([][]int(nil), partition...)
	for i := range out {
		cp := append([]int(nil), out[i]...)
		sortInts(cp)
		out[i] = cp
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j][0] < out[j-1][0]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func sortInts(values []int) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func compareIntPartition(a, b [][]int) int {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	if len(b) == 0 {
		return 1
	}
	if len(a) == 0 {
		return -1
	}
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for i := 0; i < limit; i++ {
		jMax := len(a[i])
		if len(b[i]) < jMax {
			jMax = len(b[i])
		}
		for j := 0; j < jMax; j++ {
			if a[i][j] != b[i][j] {
				return a[i][j] - b[i][j]
			}
		}
		if len(a[i]) != len(b[i]) {
			return len(a[i]) - len(b[i])
		}
	}
	return len(a) - len(b)
}

type bruteGroup struct {
	algMask    uint32
	principals []string
}

func TestExhaustiveComparison(t *testing.T) {
	rng := mathrand.New(mathrand.NewSource(20261121))
	algList := []string{algMLKEM, algXWing, algRSA}
	rank := map[string]int{algMLKEM: 0, algXWing: 1, algRSA: 2}
	for trial := 0; trial < 200; trial++ {
		n := 2 + rng.Intn(5)
		groups := make([]bruteGroup, n)
		principalOf := make([]string, n)
		for i := range groups {
			mask := uint32(0)
			for bit := range algList {
				if rng.Intn(2) == 1 {
					mask |= uint32(1) << uint(bit)
				}
			}
			if mask == 0 {
				mask = uint32(1) << uint(rng.Intn(len(algList)))
			}
			principal := fmt.Sprintf("p-%d-%d", trial, i)
			principalOf[i] = principal
			groups[i] = bruteGroup{algMask: mask, principals: []string{principal}}
		}
		forbidden := map[[2]string]bool{}
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				if rng.Intn(3) == 0 {
					forbidden[[2]string{principalOf[i], principalOf[j]}] = true
				}
			}
		}
		bCount, bWorst, bPartition := bruteForcePlan(groups, rank, forbidden)

		host := newTestHost(t)
		release := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
		nb, na := minuteTime(0)
		certs := make([]DeviceCert, 0, n)
		edges := make([]AliasEdge, 0, n)
		reqGroups := make([]DeviceGroup, 0, n)
		var pairs [][2]string
		for pair := range forbidden {
			pairs = append(pairs, pair)
		}
		for i, group := range groups {
			key := deviceKey(t)
			var algs []string
			for bit, alg := range algList {
				if group.algMask&(uint32(1)<<uint(bit)) != 0 {
					algs = append(algs, alg)
				}
			}
			addr := fmt.Sprintf("u-%d-%d@example.test", trial, i)
			for _, alg := range algs {
				certID := fmt.Sprintf("c-%d-%d-%s", trial, i, alg)
				certs = append(certs, host.cert(t, certID, principalOf[i],
					fmt.Sprintf("d%d", i), 1, key, []string{alg}, nb, na))
			}
			edges = append(edges, host.edge(t, fmt.Sprintf("e-%d-%d", trial, i),
				addr, principalOf[i], "", nb, na))
			reqGroups = append(reqGroups, DeviceGroup{
				StableID:  fmt.Sprintf("g%02d", i),
				Recipient: addr,
				DeviceIDs: []string{fmt.Sprintf("d%d", i)},
			})
		}
		req := &DeliveryRequest{
			Groups:              reqGroups,
			ForbiddenPairs:      pairs,
			AlgorithmPreference: algList,
			ReleaseTime:         release,
		}
		plan, err := PlanDelivery(snapshot(host, certs, edges), req, nil)
		if bCount < 0 {
			if err == nil {
				t.Fatalf("trial %d: 穷举认为不可行，规划器却给出方案", trial)
			}
			continue
		}
		if err != nil {
			t.Fatalf("trial %d: 穷举认为可行，规划器报错: %v", trial, err)
		}
		if len(plan.Envelopes) != bCount {
			t.Fatalf("trial %d: 信封数 %d 与穷举 %d 不一致",
				trial, len(plan.Envelopes), bCount)
		}
		worst := -1
		gotPartition := make([][]int, 0, len(plan.Envelopes))
		idToIndex := map[string]int{}
		for i, group := range reqGroups {
			idToIndex[group.StableID] = i
		}
		for _, envelope := range plan.Envelopes {
			r := rankOf(t, algList, envelope.Algorithm)
			if r > worst {
				worst = r
			}
			members := make([]int, 0, len(envelope.GroupStableIDs))
			for _, id := range envelope.GroupStableIDs {
				members = append(members, idToIndex[id])
			}
			sortInts(members)
			gotPartition = append(gotPartition, members)
		}
		if worst != bWorst {
			t.Fatalf("trial %d: 最弱等级 %d 与穷举 %d 不一致", trial, worst, bWorst)
		}
		gotCanonical := canonicalPartition(gotPartition)
		if compareIntPartition(gotCanonical, bPartition) != 0 {
			t.Logf("planner canonical blocks: %v", gotCanonical)
			t.Logf("brute   canonical blocks: %v", bPartition)
			t.Fatalf("trial %d: 决胜分区 %v 与穷举 %v 不一致",
				trial, gotCanonical, bPartition)
		}
		assertPlanVerifies(t, plan, snapshot(host, certs, edges), req, nil)
	}
}

func BenchmarkPlan18Groups(b *testing.B) {
	issuer, pub, err := NewIssuer()
	if err != nil {
		b.Fatal(err)
	}
	host := &testHost{issuer: issuer, pub: pub}
	release := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	nb, na := minuteTime(0)
	certs := make([]DeviceCert, 0, MaxDeviceGroups)
	edges := make([]AliasEdge, 0, MaxDeviceGroups)
	groups := make([]DeviceGroup, 0, MaxDeviceGroups)
	for i := 0; i < MaxDeviceGroups; i++ {
		pubKey, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			b.Fatal(err)
		}
		key := pubKey
		principal := fmt.Sprintf("p%d", i)
		cert := DeviceCert{
			CertID:      fmt.Sprintf("c%d", i),
			PrincipalID: principal,
			DeviceID:    fmt.Sprintf("d%d", i),
			Generation:  1,
			PublicKey:   key,
			Algorithms:  []string{algMLKEM, algXWing},
			NotBefore:   nb,
			NotAfter:    na,
		}
		if err := host.issuer.SignDeviceCert(&cert); err != nil {
			b.Fatal(err)
		}
		certs = append(certs, cert)
		edge := AliasEdge{
			EdgeID:      fmt.Sprintf("e%d", i),
			Address:     fmt.Sprintf("u%d@example.test", i),
			PrincipalID: principal,
			NotBefore:   nb,
			NotAfter:    na,
		}
		if err := host.issuer.SignAliasEdge(&edge); err != nil {
			b.Fatal(err)
		}
		edges = append(edges, edge)
		groups = append(groups, DeviceGroup{
			StableID:  fmt.Sprintf("g%02d", i),
			Recipient: fmt.Sprintf("u%d@example.test", i),
			DeviceIDs: []string{fmt.Sprintf("d%d", i)},
		})
	}
	req := &DeliveryRequest{
		Groups:              groups,
		AlgorithmPreference: preference(),
		ReleaseTime:         release,
	}
	snap := snapshot(host, certs, edges)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := PlanDelivery(snap, req, nil); err != nil {
			b.Fatal(err)
		}
	}
}
