// Package testselect 在给定至多 20 个测试（各自覆盖一组故障，
// 并带有互斥与前置执行关系）的情况下，选出总成本最小、
// 能覆盖全部目标故障的测试集。前置测试会被一并选入并计入成本。
// 若无法覆盖全部故障，则以 UncoverableError 报告缺失的故障。
package testselect

import (
	"fmt"
	"sort"
	"strings"
)

// MaxTests 是 Select 支持的测试数量上限（子集穷举的规模限制）。
const MaxTests = 20

// Test 描述一个可执行的测试。
type Test struct {
	ID       string   // 唯一标识
	Cost     int64    // 执行成本（被作为前置拉入时同样计费）
	Faults   []string // 该测试覆盖的故障
	Requires []string // 前置测试的 ID：选中本测试则必须同时选中它们
	Excludes []string // 互斥测试的 ID：不能与本测试同时被选中
}

// Selection 是一次求解结果。
type Selection struct {
	Tests     []string // 选中的测试 ID，按字典序排列
	TotalCost int64    // 全部选中测试（含前置）的成本之和
}

// UncoverableError 表示无法覆盖全部故障。
type UncoverableError struct {
	// Missing 是任何合法（满足前置闭包与互斥）选择都无法覆盖的故障，按字典序排列。
	Missing []string
}

func (e *UncoverableError) Error() string {
	if len(e.Missing) == 0 {
		return "testselect: no valid selection covers all faults (conflicting constraints)"
	}
	return "testselect: uncoverable faults: " + strings.Join(e.Missing, ", ")
}

// Select 枚举全部测试子集，返回覆盖 faults 的最小成本合法选择。
// 成本相同时，按排序后的测试 ID 列表的字典序决胜（较小者胜）。
func Select(tests []Test, faults []string) (Selection, error) {
	if len(tests) > MaxTests {
		return Selection{}, fmt.Errorf("testselect: %d tests exceed limit %d", len(tests), MaxTests)
	}

	// 按 ID 排序，使位序号与字典序一致，便于并列决胜。
	sorted := make([]Test, len(tests))
	copy(sorted, tests)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	index := make(map[string]int, len(sorted))
	for i, t := range sorted {
		if t.ID == "" {
			return Selection{}, fmt.Errorf("testselect: test at position %d has empty ID", i)
		}
		if _, dup := index[t.ID]; dup {
			return Selection{}, fmt.Errorf("testselect: duplicate test ID %q", t.ID)
		}
		if t.Cost < 0 {
			return Selection{}, fmt.Errorf("testselect: test %q has negative cost", t.ID)
		}
		index[t.ID] = i
	}

	n := len(sorted)
	requireMask := make([]uint32, n)
	excludeMask := make([]uint32, n)
	for i, t := range sorted {
		for _, r := range t.Requires {
			j, ok := index[r]
			if !ok {
				return Selection{}, fmt.Errorf("testselect: test %q requires unknown test %q", t.ID, r)
			}
			if j == i {
				return Selection{}, fmt.Errorf("testselect: test %q requires itself", t.ID)
			}
			requireMask[i] |= 1 << uint(j)
		}
		for _, e := range t.Excludes {
			j, ok := index[e]
			if !ok {
				return Selection{}, fmt.Errorf("testselect: test %q excludes unknown test %q", t.ID, e)
			}
			if j == i {
				return Selection{}, fmt.Errorf("testselect: test %q excludes itself", t.ID)
			}
			// 互斥是对称关系：任一方声明即双向生效。
			excludeMask[i] |= 1 << uint(j)
			excludeMask[j] |= 1 << uint(i)
		}
	}

	// 目标故障去重并编号，用位图表示覆盖关系。
	faultIndex := make(map[string]int, len(faults))
	var want []string
	for _, f := range faults {
		if _, ok := faultIndex[f]; !ok {
			faultIndex[f] = len(want)
			want = append(want, f)
		}
	}
	words := (len(want) + 63) / 64
	coverBits := make([][]uint64, n)
	for i, t := range sorted {
		bits := make([]uint64, words)
		for _, f := range t.Faults {
			k, ok := faultIndex[f]
			if !ok {
				continue // 覆盖目标之外的故障，忽略
			}
			bits[k/64] |= 1 << uint(k%64)
		}
		coverBits[i] = bits
	}

	coversAll := func(bits []uint64) bool {
		for w := 0; w < words; w++ {
			full := ^uint64(0)
			if rem := len(want) % 64; w == words-1 && rem != 0 {
				full = 1<<uint(rem) - 1
			}
			if bits[w]&full != full {
				return false
			}
		}
		return true
	}
	// 并列决胜：对称差中最低位（即字典序最小的不同 ID）所在的一方更小。
	less := func(a, b uint32) bool {
		d := a ^ b
		if d == 0 {
			return false
		}
		low := d & (^(d - 1)) // d 的最低位；避免依赖 bits 包之外的技巧
		return a&low != 0
	}

	// 子集 DP 预计算：req/excl 为子集内全部测试的前置、互斥并集，
	// cost 与覆盖位图同样按“去掉最低位的前驱子集”递推，
	// 使主循环每个子集只需 O(1) 次查询。
	total := uint32(1) << uint(n)
	req := make([]uint32, total)
	excl := make([]uint32, total)
	cost := make([]int64, total)
	cov := make([]uint64, int(total)*words)
	for mask := uint32(1); mask < total; mask++ {
		low := mask & (^(mask - 1)) // 最低位
		i := lowestBit(low)
		prev := mask ^ low
		req[mask] = req[prev] | requireMask[i]
		excl[mask] = excl[prev] | excludeMask[i]
		cost[mask] = cost[prev] + sorted[i].Cost
		base, pbase := int(mask)*words, int(prev)*words
		for w := 0; w < words; w++ {
			cov[base+w] = cov[pbase+w] | coverBits[i][w]
		}
	}

	var (
		bestMask  uint32
		bestCost  int64
		found     bool
		coverable = make([]uint64, words) // 所有合法子集覆盖的并集
	)

	for mask := uint32(0); mask < total; mask++ {
		// 前置全部落在子集内（闭包）且子集内无互斥对。
		if req[mask]&^mask != 0 || excl[mask]&mask != 0 {
			continue
		}
		bits := cov[int(mask)*words : int(mask)*words+words]
		for w := 0; w < words; w++ {
			coverable[w] |= bits[w]
		}
		if !coversAll(bits) {
			continue
		}
		if !found || cost[mask] < bestCost || (cost[mask] == bestCost && less(mask, bestMask)) {
			found, bestMask, bestCost = true, mask, cost[mask]
		}
	}

	if !found {
		var missing []string
		for k, f := range want {
			if coverable[k/64]&(1<<uint(k%64)) == 0 {
				missing = append(missing, f)
			}
		}
		sort.Strings(missing)
		return Selection{}, &UncoverableError{Missing: missing}
	}

	sel := Selection{TotalCost: bestCost}
	for i := 0; i < n; i++ {
		if bestMask&(1<<uint(i)) != 0 {
			sel.Tests = append(sel.Tests, sorted[i].ID)
		}
	}
	return sel, nil
}

func lowestBit(m uint32) int {
	i := 0
	for m&1 == 0 {
		m >>= 1
		i++
	}
	return i
}
