package sim

import (
	"fmt"
	"hash/fnv"
	"sort"
)

// Digest 是公开的固定摘要函数（FNV-1a 64），节点放置与键定位都从这里派生。
func Digest(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

// Perm 返回节点占据槽位的排列：offset = digest % M，skip = 1 + digest % (M-1)。
// M 为素数且 skip ∈ [1, M-1]，故排列覆盖全部 M 个槽。
func Perm(nodeID string, M int) []int {
	d := Digest(nodeID)
	offset := int(d % uint64(M))
	skip := 1 + int(d%uint64(M-1))
	perm := make([]int, 0, M)
	seen := make([]bool, M)
	for x := offset; len(perm) < M; x = (x + skip) % M {
		if !seen[x] {
			seen[x] = true
			perm = append(perm, x)
		}
	}
	return perm
}

// SlotOf 把键映射到槽位。
func SlotOf(key string, M int) int {
	return int(Digest("key:"+key) % uint64(M))
}

// Assign 按节点 ID 排序后逐轮让每个节点占据其排列中首个空槽，直到 M 个槽都有主。
func Assign(nodes []Node, M int) ([]string, error) {
	if M < 2 {
		return nil, fmt.Errorf("M=%d 必须为 >=2 的素数", M)
	}
	sorted := append([]Node(nil), nodes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	owner := make([]string, M)
	assigned := 0
	for assigned < M {
		progress := false
		for _, n := range sorted {
			for _, s := range Perm(n.ID, M) {
				if owner[s] == "" {
					owner[s] = n.ID
					assigned++
					progress = true
					break
				}
			}
		}
		if !progress {
			return nil, fmt.Errorf("放置无法收敛：已占 %d/%d 槽", assigned, M)
		}
	}
	return owner, nil
}

// PickReplica 为槽位选取与主节点不同故障域的备节点；缺少合法备节点时显式报错。
func PickReplica(nodes []Node, M int, primary []string) ([]string, error) {
	domain := map[string]string{}
	for _, n := range nodes {
		domain[n.ID] = n.Domain
	}
	sorted := append([]Node(nil), nodes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	replica := make([]string, M)
	var errs []string
	for s := 0; s < M; s++ {
		for _, n := range sorted {
			if n.ID != primary[s] && n.Domain != domain[primary[s]] {
				replica[s] = n.ID
				break
			}
		}
		if replica[s] == "" {
			errs = append(errs, fmt.Sprintf("槽 %d: 主=%s(域 %s) 无合法备节点（缺少不同故障域节点）",
				s, primary[s], domain[primary[s]]))
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("备节点分配失败: %v", errs)
	}
	return replica, nil
}
