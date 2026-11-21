package compaction

import (
	"errors"
	"math/bits"
)

// Solution 是一次成功压缩选择的结果。
type Solution struct {
	// SelectedIDs 按页编号升序给出入选候选页。
	SelectedIDs []uint32
	// TotalBytes 为入选页独立编码字节加上被引用字典段字节之和；
	// 同一字典段无论被多少页引用只计费一次。
	TotalBytes int
	// PageCount 为入选页数。
	PageCount int
}

// NoSolutionError 表示不存在任何候选页子集能保持全部观测。
//
// Observations 是一个包含关系极小的观测集：删去其中任一观测后，其余
// 观测可被某个合法候选页子集同时满足。
type NoSolutionError struct {
	Observations []Observation
}

func (e *NoSolutionError) Error() string {
	return "compaction: no candidate subset preserves all observations"
}

// Is 支持 errors.Is(err, ErrNoSolution)。
func (e *NoSolutionError) Is(target error) bool { return target == ErrNoSolution }

// ErrNoSolution 用于 errors.Is 判断无解错误。
var ErrNoSolution = errors.New("compaction: no solution")

// Solve 选择总物理字节最少的候选页子集，使全部合法快照的完整范围扫描
// 与替换前逐项一致。候选页数不超过 22，因此直接枚举 2^N 个页子集；
// 既不按序号跨度枚举，也不按字节串键空间枚举。
//
// 并列次序：物理字节更少优先；再按页数更少；再按页编号向量字典序最小。
func Solve(m *Manifest) (*Solution, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	cands := m.sortedCandidates()
	store := baselineStore(m)
	snaps := buildSnapshots(m)
	obs := observations(m, store, snaps)

	clauses, zeroObs, err := buildClauses(m, cands, store, obs)
	if err != nil {
		return nil, err
	}
	if zeroObs != nil {
		// 正单调子句系统不可满足当且仅当存在空子句；单个空子句本身
		// 就是包含关系极小的不可满足核：删掉它后，全集候选页可满足
		// 其余所有非空子句。
		return nil, &NoSolutionError{Observations: []Observation{*zeroObs}}
	}

	n := len(cands)
	size := 1 << n
	// bad[S]!=0 表示选择 S 违反至少一条覆盖子句。对每条子句掩码 C，
	// 所有与 C 不相交的选择都违反该子句，即 C 补集的全部子集：先在
	// 补集上打点，再用 SOS DP 把标记从子集传播到其置位超集。
	bad := make([]uint8, size)
	full := size - 1
	for _, cm := range clauses {
		bad[full^cm] = 1
	}
	for b := 0; b < n; b++ {
		bit := 1 << b
		for mask := 0; mask < size; mask++ {
			if mask&bit == 0 && bad[mask|bit] != 0 {
				bad[mask] = 1
			}
		}
	}

	dictIndex := map[uint32]int{}
	for i, d := range m.Dictionaries {
		dictIndex[d.ID] = i
	}
	candDictIdx := make([][]int, n)
	for i, c := range cands {
		for _, did := range c.DictIDs {
			candDictIdx[i] = append(candDictIdx[i], dictIndex[did])
		}
	}
	dictUse := make([]int, len(m.Dictionaries))

	bestMask := -1
	bestCost := 0
	bestCount := 0
	curCost := 0
	curCount := 0
	// 格雷码遍历：相邻子集只差一个候选页，字典引用计数可增量维护。
	for t := 0; t < size; t++ {
		if t > 0 {
			bit := bits.TrailingZeros(uint(t))
			mask := t ^ (t >> 1)
			added := mask&(1<<bit) != 0
			if added {
				curCost += len(cands[bit].Encoding)
				for _, di := range candDictIdx[bit] {
					if dictUse[di] == 0 {
						curCost += len(m.Dictionaries[di].Data)
					}
					dictUse[di]++
				}
				curCount++
			} else {
				curCost -= len(cands[bit].Encoding)
				for _, di := range candDictIdx[bit] {
					dictUse[di]--
					if dictUse[di] == 0 {
						curCost -= len(m.Dictionaries[di].Data)
					}
				}
				curCount--
			}
		}
		mask := t ^ (t >> 1)
		if bad[mask] != 0 {
			continue
		}
		if betterSubset(mask, curCost, curCount, bestMask, bestCost, bestCount) {
			bestMask, bestCost, bestCount = mask, curCost, curCount
		}
	}
	if bestMask == -1 {
		// 理论上不可达：没有空子句时全集候选页必然可行。
		return nil, &NoSolutionError{Observations: obs}
	}

	sol := &Solution{TotalBytes: bestCost, PageCount: bestCount}
	for i := range cands {
		if bestMask&(1<<i) != 0 {
			sol.SelectedIDs = append(sol.SelectedIDs, cands[i].ID)
		}
	}
	return sol, nil
}

// betterSubset 实现并列规则：字节 → 页数 → 页编号向量字典序。
// 向量按候选页编号升序构造；两个等长升序向量的字典序由最低不同位
// 决定，该位上含页编号的一方向量更大。
func betterSubset(mask, cost, count, bestMask, bestCost, bestCount int) bool {
	if bestMask == -1 {
		return true
	}
	if cost != bestCost {
		return cost < bestCost
	}
	if count != bestCount {
		return count < bestCount
	}
	diff := mask ^ bestMask
	low := diff & -diff
	// 候选按页编号升序排列，最低不同位对应最小分歧编号；不含该位
	// 的一方在该编号处结束，其升序向量字典序更小。
	return mask&low != 0
}

// buildClauses 为每个由被替换页决定结果的观测构造一条正覆盖子句。
//
// 判定依据是“读结果是否仍正确”而非获胜记录身份：一个更高序号的
// 删除可能被候选里同效墓碑（点删除/区间删除都读出 null）遮蔽，此时
// 缺少原序号记录并不造成可观测差异。其余层决定结果或本来就缺失的
// 观测对选择没有约束。子句掩码为空意味着任何合法页集都无法修复该
// 观测，对应不可满足观测。
func buildClauses(m *Manifest, cands []CandidatePage, store []taggedRecord, obs []Observation) ([]int, *Observation, error) {
	clauses := make([]int, 0, len(obs))
	base := make([]taggedRecord, 0, len(m.OtherLayers))
	for _, r := range m.OtherLayers {
		base = append(base, taggedRecord{Record: r.Clone()})
	}
	for oi := range obs {
		ob := &obs[oi]
		win := winner(store, ob.Key, ob.Snapshot)
		if win == nil || !win.fromReplaced {
			continue
		}
		var mask int
		for ci := range cands {
			recs := append(append([]taggedRecord(nil), base...),
				taggedRecords(cands[ci].Records)...)
			if readAt(recs, ob.Key, ob.Snapshot).equal(ob.Correct) {
				mask |= 1 << ci
			}
		}
		if mask == 0 {
			return nil, ob, nil
		}
		clauses = append(clauses, mask)
	}
	return clauses, nil, nil
}

func taggedRecords(rs []Record) []taggedRecord {
	out := make([]taggedRecord, 0, len(rs))
	for _, r := range rs {
		out = append(out, taggedRecord{Record: r.Clone(), fromReplaced: true})
	}
	return out
}

// winner 返回快照下决定读结果的最大序号覆盖记录，无覆盖时为 nil。
func winner(recs []taggedRecord, key []byte, snap uint64) *taggedRecord {
	best := -1
	for i := range recs {
		r := &recs[i]
		if r.Seq > snap || !r.Covers(key) {
			continue
		}
		if best == -1 || r.Seq > recs[best].Seq {
			best = i
		}
	}
	if best == -1 {
		return nil
	}
	return &recs[best]
}

// simulateSubset 是供测试对照的参考实现：逐候选页子集完整重建存储，
// 对每个观测直接求值，不使用子句或 SOS DP 的任何结论。
func simulateSubset(m *Manifest, cands []CandidatePage, mask uint32, store []taggedRecord, obs []Observation) bool {
	var recs []taggedRecord
	for _, r := range m.OtherLayers {
		recs = append(recs, taggedRecord{Record: r.Clone()})
	}
	for ci := range cands {
		if mask&(1<<ci) == 0 {
			continue
		}
		for _, r := range cands[ci].Records {
			recs = append(recs, taggedRecord{Record: r.Clone(), fromReplaced: true})
		}
	}
	for _, ob := range obs {
		got := readAt(recs, ob.Key, ob.Snapshot)
		if !got.equal(ob.Correct) {
			return false
		}
	}
	return true
}
