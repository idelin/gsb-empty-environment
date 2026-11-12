package padplan

import (
	"fmt"
	"math/big"
	"sort"
)

// enumerationResult 是全分配枚举核对的内部结果。
type enumerationResult struct {
	bestDistinct int
	bestPadding  int64
	leaves       int64
	nodes        int64
	space        *big.Int
}

// enumerator 在全部 len(buckets)^len(lengths) 个分配上做深度优先枚举，
// 独立记录自身观察到的字典序最优值，用于交叉核对规划器结论。
type enumerator struct {
	lengths []int64
	buckets []int64
	budget  int64

	minIdx []int

	plannerDistinct int
	plannerPadding  int64

	bestDistinct int
	bestPadding  int64

	used map[int64]int

	leaves int64
	nodes  int64
	cap    int64
}

// enumerateAll 执行全分配枚举。nodeCap <= 0 表示不限制；plannerDistinct 与
// plannerPadding 是规划器给出的最优值，仅用于安全剪枝（剪掉不可能严格更优的
// 分支），枚举器仍独立维护自身最优值。
func enumerateAll(lengths, buckets []int64, budget int64,
	plannerDistinct int, plannerPadding, nodeCap int64) (*enumerationResult, error) {

	n := len(lengths)
	k := len(buckets)
	minIdx := make([]int, n)
	for i, length := range lengths {
		idx := sort.Search(k, func(j int) bool { return buckets[j] >= length })
		if idx == k {
			return nil, fmt.Errorf("%w: object at index %d length %d exceeds largest bucket %d",
				ErrNoFeasibleBucket, i, length, buckets[k-1])
		}
		minIdx[i] = idx
	}

	e := &enumerator{
		lengths:         lengths,
		buckets:         buckets,
		budget:          budget,
		minIdx:          minIdx,
		plannerDistinct: plannerDistinct,
		plannerPadding:  plannerPadding,
		bestDistinct:    n + 1,
		bestPadding:     1<<63 - 1,
		used:            make(map[int64]int, k),
		cap:             nodeCap,
	}
	if err := e.dfs(0, 0); err != nil {
		return nil, err
	}
	if e.bestDistinct > n {
		return nil, fmt.Errorf("%w: enumerator found no feasible assignment", ErrVerificationMismatch)
	}

	space := new(big.Int).Exp(big.NewInt(int64(k)), big.NewInt(int64(n)), nil)
	return &enumerationResult{
		bestDistinct: e.bestDistinct,
		bestPadding:  e.bestPadding,
		leaves:       e.leaves,
		nodes:        e.nodes,
		space:        space,
	}, nil
}

// dfs 为第 depth 个对象枚举桶选择，padding 是已确定对象的累计填充。
func (e *enumerator) dfs(depth int, padding int64) error {
	e.nodes++
	if e.cap > 0 && e.nodes > e.cap {
		return fmt.Errorf("%w: visited %d nodes (cap %d)", ErrEnumerationLimit, e.nodes-1, e.cap)
	}

	n := len(e.lengths)
	if depth == n {
		// 防御性复核：到达叶子的分配必须满足全部硬约束。
		if padding > e.budget {
			return fmt.Errorf("%w: leaf padding %d exceeds budget %d", ErrVerificationMismatch, padding, e.budget)
		}
		e.leaves++
		distinct := len(e.used)
		if distinct < e.bestDistinct || (distinct == e.bestDistinct && padding < e.bestPadding) {
			e.bestDistinct = distinct
			e.bestPadding = padding
		}
		return nil
	}

	distinctUsed := len(e.used)
	// 剪枝 1：已使用的不同桶数已超过规划器最优值，后续只增不减；
	// 与规划器持平的分支保留，确保枚举器能独立复现最优值。
	if distinctUsed > e.plannerDistinct {
		return nil
	}
	minimumAdditional, ok := e.minimumAdditionalPadding(depth, padding)
	if !ok {
		return nil
	}
	// 剪枝 2：剩余对象即使各自取最小可行桶，也会突破预算。
	if padding+minimumAdditional > e.budget {
		return nil
	}

	for bi := e.minIdx[depth]; bi < len(e.buckets); bi++ {
		bucket := e.buckets[bi]
		added := bucket - e.lengths[depth]
		nextPadding, addOK := safeAdd(padding, added)
		if !addOK || nextPadding > e.budget {
			continue
		}
		e.used[bucket]++
		err := e.dfs(depth+1, nextPadding)
		e.used[bucket]--
		if e.used[bucket] == 0 {
			delete(e.used, bucket)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// minimumAdditionalPadding 计算从 start 起的剩余对象在全局最小可行桶下的
// 填充下界；返回 ok=false 表示整数溢出。
func (e *enumerator) minimumAdditionalPadding(start int, padding int64) (int64, bool) {
	var additional int64
	for i := start; i < len(e.lengths); i++ {
		bucket := e.buckets[e.minIdx[i]]
		sum, ok := safeAdd(additional, bucket-e.lengths[i])
		if !ok {
			return 0, false
		}
		additional = sum
		if padding+additional > e.budget {
			return additional, true
		}
	}
	return additional, true
}
