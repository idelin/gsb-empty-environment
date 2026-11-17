package netsec

import (
	"fmt"
	"math/bits"
	"sort"
)

// CoverResult 是最小新增测试向量集合的结果。
type CoverResult struct {
	Status Status
	// Vectors 为升序的最小新增测试向量集合；在所有最小规模解中
	// 按向量序列字典序最小决胜。Status 为 StatusFound 时精确有效。
	Vectors []uint32
	// Dangerous 为全部危险故障（存在输入使安全位 0->1），供审计。
	Dangerous []Fault
	Err       error
}

// MinimalAdditionalTests 求覆盖所有可检测危险故障的最小新增测试向量集合。
//
// 危险故障：存在输入使安全输出位由 0 变为 1 的固定故障（此类故障必然可检测）。
// 已被 public 中向量观测到的危险故障视为已覆盖，新增向量只需覆盖其余危险故障；
// 传入空 public 即得到独立覆盖全部危险故障的最小集合。候选向量限定为
// [0, min(2^NumInputs, 4096)) 且不与 public 重复。
//
// 求解为精确最小集合覆盖：先求最小规模 k，再取字典序最小的升序向量组合。
// budget 为评估预算（故障仿真与搜索节点各计 1）；budget < 0 不限，
// 耗尽时返回 StatusUnknown。
func MinimalAdditionalTests(c *Circuit, public []uint32, budget int64) CoverResult {
	if err := c.Validate(); err != nil {
		return CoverResult{Status: StatusError, Err: err}
	}
	space := c.space()
	if space > MaxSpace {
		space = MaxSpace
	}
	inPublic := make([]bool, space)
	for _, t := range public {
		if t >= uint32(space) {
			return CoverResult{Status: StatusError, Err: fmt.Errorf("netsec: test vector %d out of range [0,%d)", t, space)}
		}
		inPublic[t] = true
	}

	words := wordsOf(space)
	valid := validMask(space)
	pats := inputPatterns(c.NumInputs, space)

	normal := newBitsets(len(c.Nodes), words)
	simulate(c, pats, valid, normal, 0)
	outputs := c.outputNodes()

	testMask := make(bitset, words)
	for _, t := range public {
		testMask[t/64] |= 1 << (uint(t) % 64)
	}

	// 枚举全部故障，收集危险故障及其可检测向量集合。
	var dangerous []Fault
	var detects []bitset // 与 dangerous 对齐：第 v 位置位表示向量 v 可观测该故障
	tmp := make(bitset, words)
	diff := make(bitset, words)
	faultNodes := c.faultableNodes()
	done := int64(0)
	for _, n := range faultNodes {
		for stuck := 0; stuck <= 1; stuck++ {
			if budget >= 0 && done >= budget {
				return CoverResult{Status: StatusUnknown, Dangerous: dangerous}
			}
			done++
			vals := cloneBitsets(normal)
			setConst(vals[n], valid, stuck)
			simulate(c, pats, valid, vals, n+1)

			andNotInto(tmp, vals[c.Secure], normal[c.Secure])
			if _, ok := lowestBit(tmp); !ok {
				continue // 非危险故障
			}
			detect := make(bitset, words)
			for _, o := range outputs {
				diffInto(diff, vals[o], normal[o])
				orInto(detect, diff)
			}
			dangerous = append(dangerous, Fault{Node: n, Stuck: stuck})
			detects = append(detects, detect)
		}
	}
	if len(dangerous) > 64 {
		return CoverResult{Status: StatusError, Err: fmt.Errorf("netsec: %d dangerous faults exceeds limit 64", len(dangerous))}
	}

	// 待覆盖集合：未被 public 观测到的危险故障。
	var universe uint64
	for i, d := range detects {
		if !anyAnd(d, testMask) {
			universe |= 1 << uint(i)
		}
	}
	res := CoverResult{Status: StatusFound, Dangerous: dangerous}
	if universe == 0 {
		return res
	}

	// 每个候选向量覆盖的故障集合（排除 public 向量）。
	cover := make([]uint64, space)
	for i := 0; i < len(dangerous); i++ {
		if universe>>uint(i)&1 == 0 {
			continue
		}
		d := detects[i]
		for w, x := range d {
			for x != 0 {
				tz := bits.TrailingZeros64(x)
				v := w*64 + tz
				if !inPublic[v] {
					cover[v] |= 1 << uint(i)
				}
				x &= x - 1
			}
		}
	}

	var budgetPtr *int64
	if budget >= 0 {
		remaining := budget - done
		budgetPtr = &remaining
	}
	vecs, feasible, aborted := minSetCover(cover, universe, budgetPtr)
	switch {
	case aborted:
		return CoverResult{Status: StatusUnknown, Dangerous: dangerous}
	case !feasible:
		return CoverResult{Status: StatusError, Err: fmt.Errorf("netsec: dangerous fault not coverable by any candidate vector")}
	}
	res.Vectors = make([]uint32, len(vecs))
	for i, v := range vecs {
		res.Vectors[i] = uint32(v)
	}
	return res
}

// minSetCover 精确求解最小集合覆盖：universe 为故障位掩码，
// cover[v] 为向量 v 覆盖的故障位掩码。返回升序向量下标，
// 规模为精确最小，且在所有最小规模解中字典序最小。
// feasible 为 false 表示某故障无任何候选向量可覆盖；
// budget 非 nil 时限制搜索节点数，耗尽时 aborted 为 true。
func minSetCover(cover []uint64, universe uint64, budget *int64) (vecs []int, feasible, aborted bool) {
	if universe == 0 {
		return nil, true, false
	}
	var cands []int
	for v, m := range cover {
		if m != 0 {
			cands = append(cands, v)
		}
	}
	sort.Ints(cands)

	nf := bits.OnesCount64(universe)
	// 每个故障可覆盖它的最大候选下标（用于死路剪枝），按故障位下标索引。
	lastCand := make([]int, 64)
	// 候选覆盖大小的后缀最大值（用于容量剪枝）。
	suffixMax := make([]int, len(cands)+1)
	for i := len(cands) - 1; i >= 0; i-- {
		suffixMax[i] = suffixMax[i+1]
		if pc := bits.OnesCount64(cover[cands[i]]); pc > suffixMax[i] {
			suffixMax[i] = pc
		}
	}
	for f := range lastCand {
		lastCand[f] = -1
	}
	for i, v := range cands {
		m := cover[v] & universe
		for m != 0 {
			f := bits.TrailingZeros64(m)
			lastCand[f] = i
			m &= m - 1
		}
	}
	for m := universe; m != 0; m &= m - 1 {
		if lastCand[bits.TrailingZeros64(m)] < 0 {
			return nil, false, false
		}
	}

	maxCover := suffixMax[0]
	lower := (nf + maxCover - 1) / maxCover

	chosen := make([]int, 0, nf)
	var dfs func(covered uint64, last, k int) bool
	dfs = func(covered uint64, last, k int) bool {
		if covered == universe {
			return true
		}
		if len(chosen) == k {
			return false
		}
		if budget != nil {
			if *budget <= 0 {
				aborted = true
				return false
			}
			*budget--
		}
		rem := universe &^ covered
		slots := k - len(chosen)
		// 容量剪枝：剩余槽位按最大覆盖也无法盖完。
		if bits.OnesCount64(rem) > slots*suffixMax[last+1] {
			return false
		}
		// 死路剪枝：某未覆盖故障的所有可覆盖向量均不大于 last。
		m := rem
		for m != 0 {
			f := bits.TrailingZeros64(m)
			if lastCand[f] <= last {
				return false
			}
			m &= m - 1
		}
		for i := last + 1; i < len(cands); i++ {
			cv := cover[cands[i]]
			if cv&rem == 0 {
				continue // 最小解中每个向量必覆盖至少一个未覆盖故障
			}
			chosen = append(chosen, cands[i])
			if dfs(covered|cv, i, k) {
				return true
			}
			chosen = chosen[:len(chosen)-1]
			if aborted {
				return false
			}
		}
		return false
	}

	for k := lower; k <= nf; k++ {
		chosen = chosen[:0]
		aborted = false
		if dfs(0, -1, k) {
			out := make([]int, len(chosen))
			copy(out, chosen)
			return out, true, false
		}
		if aborted {
			return nil, false, true
		}
	}
	// 不可达：可行性预检保证 k <= nf 时必有解。
	return nil, false, false
}
