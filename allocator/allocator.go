// Package allocator 实现加权分片分配器：
//
//   - 按节点权重把分片摊派为整数配额（最大余数法 / Hamilton 分摊），
//     配额之和恒等于分片总数，且每个节点的配额与其理想值 w_i/Σw * N 的
//     偏差严格小于 1，不是近似均衡；
//   - 在满足配额的所有分配方案中，选取搬迁数最小的方案（保留旧归属最多）；
//   - 一切并列（余数相同、选择保留哪些分片、空缺填充顺序）均按
//     节点编号 / 分片编号升序决胜，结果完全确定；
//   - 输出搬迁下界及证明：任何满足配额的分配方案，搬迁数都不小于
//     LowerBound，而本方案的搬迁数恰好等于 LowerBound，故最优。
package allocator

import (
	"fmt"
	"sort"
	"strings"
)

// NodeID 节点编号，决胜时按字典序升序（编号小者优先）。
type NodeID string

// ShardID 分片编号。
type ShardID string

// Node 参与分配的节点及其权重。权重为 0 的节点配额为 0（其上的
// 分片会全部迁出），可用于平滑下线。
type Node struct {
	ID     NodeID
	Weight int64
}

// NodeReport 单个节点的分配明细。
type NodeReport struct {
	ID      NodeID
	Weight  int64
	Quota   int // 目标整数配额
	OldHeld int // 旧归属中仍存在的分片数（节点已下线则为 0）
	Kept    int // 保留在本节点的分片数 = min(OldHeld, Quota)
	Arrived int // 迁入（含全新分片）
	Left    int // 迁出
}

// Plan 一次分配的结果及其最优性证据。
type Plan struct {
	Assignment map[ShardID]NodeID // 最终归属
	Quotas     map[NodeID]int     // 每个节点的整数配额
	Nodes      []NodeReport       // 按节点编号升序
	Total      int                // 分片总数
	Stays      int                // 归属未变的分片数
	Migration  int                // 搬迁数：旧归属存在且发生变化的分片数
	LowerBound int                // 搬迁下界：任何满足配额的方案都至少需要这么多次搬迁
	NewShards  int                // 旧归属中不存在的新分片数（计入 Arrived，不计入 Migration）
}

// Allocate 计算满足整数配额的最小搬迁分配。
//
// nodes 为当前在线节点（可含权重 0 节点）；shards 为全量分片；
// old 为旧归属，允许引用已下线的节点或已删除的分片，也允许缺少
// 新增分片的条目。返回的 Plan 满足 Verify 的全部校验。
func Allocate(nodes []Node, shards []ShardID, old map[ShardID]NodeID) (*Plan, error) {
	if err := validate(nodes, shards); err != nil {
		return nil, err
	}
	quotas, err := computeQuotas(nodes, len(shards))
	if err != nil {
		return nil, err
	}

	sortedNodes := sortedNodeIDs(nodes)
	nodeSet := make(map[NodeID]bool, len(nodes))
	for _, id := range sortedNodes {
		nodeSet[id] = true
	}

	// 按旧归属把仍存在的分片归到各节点名下。
	heldBy := make(map[NodeID][]ShardID)
	newShards := 0
	for _, s := range shards {
		owner, ok := old[s]
		if !ok {
			newShards++
			continue
		}
		heldBy[owner] = append(heldBy[owner], s)
	}
	for id := range heldBy {
		sort.Slice(heldBy[id], func(i, j int) bool { return heldBy[id][i] < heldBy[id][j] })
	}

	// 第一步：每个在线节点保留 min(旧持有, 配额) 个分片（编号小者优先保留），
	// 其余进入待分配池。这是保留旧归属的上界，我们直接达到它。
	assignment := make(map[ShardID]NodeID, len(shards))
	var pool []ShardID
	stays := 0
	for _, id := range sortedNodes {
		held := heldBy[id]
		keep := quotas[id]
		if len(held) < keep {
			keep = len(held)
		}
		for _, s := range held[:keep] {
			assignment[s] = id
			stays++
		}
		pool = append(pool, held[keep:]...)
	}
	// 已下线节点上的分片与全新分片一并进入待分配池。
	for _, s := range shards {
		owner, ok := old[s]
		if !ok || !nodeSet[owner] {
			pool = append(pool, s)
		}
	}
	sort.Slice(pool, func(i, j int) bool { return pool[i] < pool[j] })

	// 第二步：按节点编号升序，依次用编号最小的待分配分片补齐配额缺口。
	idx := 0
	for _, id := range sortedNodes {
		need := quotas[id]
		for _, s := range heldBy[id] {
			if assignment[s] == id {
				need--
			}
		}
		for ; need > 0; need-- {
			assignment[pool[idx]] = id
			idx++
		}
	}
	if idx != len(pool) {
		return nil, fmt.Errorf("internal error: %d shards left unassigned", len(pool)-idx)
	}

	plan := &Plan{
		Assignment: assignment,
		Quotas:     quotas,
		Total:      len(shards),
		Stays:      stays,
		NewShards:  newShards,
	}
	for _, s := range shards {
		oldOwner, ok := old[s]
		if ok && oldOwner != assignment[s] {
			plan.Migration++
		}
	}
	// 下界 = 有旧归属的分片数 - 保留数上界。本方案保留数恰为上界，故 Migration == LowerBound。
	plan.LowerBound = (len(shards) - newShards) - stays
	plan.Nodes = buildReports(nodes, quotas, heldBy, assignment, old)
	return plan, nil
}

// Verify 独立复核 Plan：配额精确满足、每个分片恰好归属一个在线节点、
// 搬迁数与下界均与重新计算的结果一致。全部通过则该 Plan 是满足整数
// 配额的最小搬迁分配，返回 nil；否则返回首个不符项的错误。
func Verify(p *Plan, nodes []Node, shards []ShardID, old map[ShardID]NodeID) error {
	if err := validate(nodes, shards); err != nil {
		return err
	}
	quotas, err := computeQuotas(nodes, len(shards))
	if err != nil {
		return err
	}
	nodeSet := make(map[NodeID]bool, len(nodes))
	for _, n := range nodes {
		nodeSet[n.ID] = true
	}

	count := make(map[NodeID]int)
	for _, s := range shards {
		owner, ok := p.Assignment[s]
		if !ok {
			return fmt.Errorf("shard %s is unassigned", s)
		}
		if !nodeSet[owner] {
			return fmt.Errorf("shard %s assigned to unknown node %s", s, owner)
		}
		count[owner]++
	}
	if len(p.Assignment) != len(shards) {
		return fmt.Errorf("assignment covers %d shards, want %d", len(p.Assignment), len(shards))
	}
	for _, n := range nodes {
		if count[n.ID] != quotas[n.ID] {
			return fmt.Errorf("node %s holds %d shards, quota is %d", n.ID, count[n.ID], quotas[n.ID])
		}
		if p.Quotas[n.ID] != quotas[n.ID] {
			return fmt.Errorf("node %s reported quota %d, recomputed %d", n.ID, p.Quotas[n.ID], quotas[n.ID])
		}
	}

	migration := 0
	newShards := 0
	oldHeld := make(map[NodeID]int)
	for _, s := range shards {
		oldOwner, ok := old[s]
		if !ok {
			newShards++
			continue
		}
		oldHeld[oldOwner]++
		if oldOwner != p.Assignment[s] {
			migration++
		}
	}
	if migration != p.Migration {
		return fmt.Errorf("reported migration %d, recomputed %d", p.Migration, migration)
	}

	// 下界重算：节点 i 上归属不变的分片至多 min(旧持有_i, 配额_i)，
	// 已下线节点上的分片必须迁出，故任何方案的搬迁数 >=
	// (有旧归属的分片数) - Σ_i min(旧持有_i, 配额_i)。
	maxStays := 0
	for _, n := range nodes {
		keep := oldHeld[n.ID]
		if quotas[n.ID] < keep {
			keep = quotas[n.ID]
		}
		maxStays += keep
	}
	lowerBound := (len(shards) - newShards) - maxStays
	if p.LowerBound != lowerBound {
		return fmt.Errorf("reported lower bound %d, recomputed %d", p.LowerBound, lowerBound)
	}
	if p.Migration != lowerBound {
		return fmt.Errorf("migration %d exceeds proven lower bound %d", p.Migration, lowerBound)
	}
	return nil
}

// Proof 输出人类可读的搬迁下界证明。
func (p *Plan) Proof() string {
	var b strings.Builder
	fmt.Fprintf(&b, "分片总数 %d，搬迁 %d 次，下界 %d 次（已达最优）。\n", p.Total, p.Migration, p.LowerBound)
	b.WriteString("证明：任何满足整数配额的分配方案中，节点 i 上归属不变的分片数\n")
	b.WriteString("至多为 min(旧持有_i, 配额_i)；旧节点已下线的分片必然迁出。因此\n")
	b.WriteString("搬迁数 >= (有旧归属的分片数) - Σ_i min(旧持有_i, 配额_i) = 下界。\n")
	b.WriteString("本方案在每个节点恰好保留 min(旧持有_i, 配额_i) 个分片，逐项达到上界，\n")
	b.WriteString("故搬迁数等于下界，不存在搬迁更少的合法方案。逐项明细：\n")
	for _, r := range p.Nodes {
		fmt.Fprintf(&b, "  节点 %-8s 权重 %-4d 配额 %-4d 旧持有 %-4d 保留 %-4d 迁入 %-4d 迁出 %d\n",
			string(r.ID), r.Weight, r.Quota, r.OldHeld, r.Kept, r.Arrived, r.Left)
	}
	return b.String()
}

// computeQuotas 用最大余数法（Hamilton 分摊）把 total 个分片按权重摊派为
// 整数配额：先取 floor(total*w_i/Σw)，剩余席位按小数余数从大到小分配，
// 余数并列时节点编号小者优先。配额之和恒等于 total，且 |配额_i -
// total*w_i/Σw| < 1。全部用整数运算，无浮点误差。
func computeQuotas(nodes []Node, total int) (map[NodeID]int, error) {
	quotas := make(map[NodeID]int, len(nodes))
	if total == 0 {
		for _, n := range nodes {
			quotas[n.ID] = 0
		}
		return quotas, nil
	}
	var totalWeight int64
	for _, n := range nodes {
		totalWeight += n.Weight
	}
	if totalWeight <= 0 {
		return nil, fmt.Errorf("total weight is zero but %d shards need assignment", total)
	}
	type remainder struct {
		id NodeID
		r  int64
	}
	rems := make([]remainder, 0, len(nodes))
	assigned := 0
	for _, n := range nodes {
		exact := int64(total) * n.Weight
		q := exact / totalWeight
		quotas[n.ID] = int(q)
		assigned += int(q)
		rems = append(rems, remainder{id: n.ID, r: exact % totalWeight})
	}
	sort.Slice(rems, func(i, j int) bool {
		if rems[i].r != rems[j].r {
			return rems[i].r > rems[j].r // 余数大者优先
		}
		return rems[i].id < rems[j].id // 并列按节点编号升序决胜
	})
	for i := 0; i < total-assigned; i++ {
		quotas[rems[i].id]++
	}
	return quotas, nil
}

func validate(nodes []Node, shards []ShardID) error {
	seenNodes := make(map[NodeID]bool, len(nodes))
	for _, n := range nodes {
		if n.Weight < 0 {
			return fmt.Errorf("node %s has negative weight %d", n.ID, n.Weight)
		}
		if seenNodes[n.ID] {
			return fmt.Errorf("duplicate node %s", n.ID)
		}
		seenNodes[n.ID] = true
	}
	seenShards := make(map[ShardID]bool, len(shards))
	for _, s := range shards {
		if seenShards[s] {
			return fmt.Errorf("duplicate shard %s", s)
		}
		seenShards[s] = true
	}
	return nil
}

func sortedNodeIDs(nodes []Node) []NodeID {
	ids := make([]NodeID, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func buildReports(nodes []Node, quotas map[NodeID]int, heldBy map[NodeID][]ShardID,
	assignment map[ShardID]NodeID, old map[ShardID]NodeID) []NodeReport {
	arrived := make(map[NodeID]int)
	for s, owner := range assignment {
		if oldOwner, ok := old[s]; !ok || oldOwner != owner {
			arrived[owner]++
		}
	}
	reports := make([]NodeReport, 0, len(nodes))
	for _, n := range nodes {
		quota := quotas[n.ID]
		oldHeld := len(heldBy[n.ID])
		kept := oldHeld
		if quota < kept {
			kept = quota
		}
		reports = append(reports, NodeReport{
			ID:      n.ID,
			Weight:  n.Weight,
			Quota:   quota,
			OldHeld: oldHeld,
			Kept:    kept,
			Arrived: arrived[n.ID],
			Left:    oldHeld - kept,
		})
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].ID < reports[j].ID })
	return reports
}
