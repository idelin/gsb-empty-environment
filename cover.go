package netsec

import "fmt"

// MaxDangerousFaults 是精确求解最小测试集所允许的危险故障上限。
// 超过该上限时 MinTestSet 报告 Unknown。
const MaxDangerousFaults = 20

// CoverReport 是 MinTestSet 的结论。Vectors 是按升序排列的最小
// 新增测试向量集；与公开测试集合并后可发现全部危险故障。
type CoverReport struct {
	Status          Status
	Reason          string // Status 为 Unknown 时的原因
	Vectors         []uint32
	DangerousFaults int // 危险故障总数
	AlreadyCovered  int // 其中已被公开测试集发现的数量
}

// MinTestSet 精确求解覆盖全部危险故障的最小新增测试向量集。
//
// 危险故障指存在输入使安全输出 0→1 的故障（因而必然可检测）。
// 公开测试集已发现的危险故障无需重复覆盖；返回的新增向量集与
// 公开测试集合并后可发现所有危险故障，且向量数量精确最少。
// 数量并列时取向量序列（升序排列后按数值字典序）最小者。
//
// 前提：输入空间不超过 4096（由 NewCircuit 保证）且危险故障数
// 不超过 MaxDangerousFaults；前提破坏或预算耗尽时报告 Unknown。
func MinTestSet(c *Circuit, tests []uint32, opts Options) (*CoverReport, error) {
	if err := c.validateTests(tests); err != nil {
		return nil, err
	}
	normal := c.simulate(nil)
	normalOut := normal[c.outputIdx]
	faults := c.AllFaults()
	rep := &CoverReport{}
	// remaining：危险且未被公开测试发现的故障及其故障输出真值表。
	var remaining [][]uint64
	for i, f := range faults {
		if opts.Budget > 0 && int64(i) >= opts.Budget {
			rep.Status = StatusUnknown
			rep.Reason = fmt.Sprintf("预算耗尽：已分析 %d/%d 个故障", i, len(faults))
			return rep, nil
		}
		faultyOut := c.simulate(&f)[c.outputIdx]
		dangerous := false
		for w := 0; w < c.words; w++ {
			if faultyOut[w]&^normalOut[w] != 0 {
				dangerous = true
				break
			}
		}
		if !dangerous {
			continue
		}
		rep.DangerousFaults++
		if detectedByTests(normalOut, faultyOut, tests) {
			rep.AlreadyCovered++
			continue
		}
		remaining = append(remaining, faultyOut)
	}
	if rep.DangerousFaults > MaxDangerousFaults {
		rep.Status = StatusUnknown
		rep.Reason = fmt.Sprintf("危险故障数 %d 超过上限 %d，无法保证精确求解", rep.DangerousFaults, MaxDangerousFaults)
		return rep, nil
	}
	if len(remaining) == 0 {
		rep.Status = StatusNone
		return rep, nil
	}
	// 每个候选输入向量对剩余故障的覆盖掩码：向量 j 能发现故障 k
	// 当且仅当输入 j 下故障输出与正常输出不同。
	nf := len(remaining)
	space := c.InputSpace()
	coverAll := make([]uint32, space)
	for j := 0; j < space; j++ {
		nb := bitOf(normalOut, uint32(j))
		var m uint32
		for k := 0; k < nf; k++ {
			if bitOf(remaining[k], uint32(j)) != nb {
				m |= 1 << k
			}
		}
		coverAll[j] = m
	}
	// 去重并删除被真包含的掩码：被包含者不可能减少所需向量数。
	masks := reduceMasks(coverAll)
	// dist[mask] = 覆盖 mask 中全部故障所需的最少向量数。
	// 故障数不超过 20，状态空间不超过 2^20。
	full := (1 << nf) - 1
	const unreachable = 0xff
	dist := make([]uint8, full+1)
	for i := range dist {
		dist[i] = unreachable
	}
	dist[0] = 0
	for mask := 1; mask <= full; mask++ {
		best := unreachable
		for _, cm := range masks {
			m := int(cm)
			if m&mask == 0 {
				continue
			}
			if d := int(dist[mask&^m]) + 1; d < best {
				best = d
			}
		}
		dist[mask] = uint8(best)
	}
	if dist[full] == unreachable {
		// 不可达：每个危险故障都有逃逸输入，必然可被某向量发现。
		return nil, fmt.Errorf("netsec: 内部不一致：危险故障不可覆盖")
	}
	// 贪心重构字典序最小的升序向量序列：每一位都取仍允许
	// 最优完成的最小向量。可行性不变式保证该贪心精确。
	vecs := make([]uint32, 0, dist[full])
	cur := full
	lo := 0
	for cur != 0 {
		d := dist[cur]
		chosen := -1
		for j := lo; j < space; j++ {
			m := int(coverAll[j])
			if m == 0 {
				continue
			}
			if dist[cur&^m] == d-1 {
				chosen = j
				break
			}
		}
		if chosen < 0 {
			return nil, fmt.Errorf("netsec: 内部不一致：无法重构最小覆盖")
		}
		vecs = append(vecs, uint32(chosen))
		cur &^= int(coverAll[chosen])
		lo = chosen + 1
	}
	rep.Vectors = vecs
	rep.Status = StatusFound
	return rep, nil
}

// reduceMasks 返回非空覆盖掩码的集合：去重，并删除被其他掩码
// 真包含的掩码。
func reduceMasks(coverAll []uint32) []uint32 {
	seen := make(map[uint32]bool)
	var masks []uint32
	for _, m := range coverAll {
		if m == 0 || seen[m] {
			continue
		}
		seen[m] = true
		masks = append(masks, m)
	}
	kept := masks[:0]
	for i, a := range masks {
		dominated := false
		for j, b := range masks {
			if i != j && a&b == a { // a ⊆ b 且 a ≠ b（已去重）
				dominated = true
				break
			}
		}
		if !dominated {
			kept = append(kept, a)
		}
	}
	return kept
}
