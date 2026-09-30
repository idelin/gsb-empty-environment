package main

import "fmt"

// Overlap 表示一对规则的部分重叠（后规则未被前规则完全覆盖）。
type Overlap struct {
	Earlier  int    // 前规则下标（优先级更高）
	Later    int    // 后规则下标
	Conflict bool   // 两规则动作不同即为冲突
	Example  Packet // 落在交集中的合成反例包
}

// RuleReport 是单条规则的分析结论。
type RuleReport struct {
	RuleIndex int
	Reachable bool
	CoveredBy []int   // 参与覆盖本规则的前规则下标
	Witness   *Packet // 可达时：能命中本规则的合成包
	Captured  *Packet // 不可达时：本规则想匹配、但被前规则截获的合成包
}

// Report 是完整分析结果。
type Report struct {
	Rules    []RuleReport
	Overlaps []Overlap
}

// Analyze 对规则集做遮蔽与重叠分析。
// 对每条规则 j：用 j 的匹配区域依次减去所有同族前规则的区域，
// 剩余为空则 j 被完全遮蔽（不可达）；与前规则相交但未被覆盖则记为部分重叠。
// IPv4 与 IPv6 规则互不影响。
func Analyze(rules []Rule) Report {
	regions := make([]region, len(rules))
	for i := range rules {
		regions[i] = regionOf(rules[i])
	}
	rep := Report{}
	for j := range rules {
		rr := RuleReport{RuleIndex: j, Reachable: true}
		remaining := []region{regions[j]}
		for i := 0; i < j; i++ {
			if rules[i].Family() != rules[j].Family() {
				continue // 跨族永不遮蔽
			}
			inter, ok := intersectRegion(regions[j], regions[i])
			if !ok {
				continue
			}
			// 有交集：若 i 单独不能完全覆盖 j，则为部分重叠。
			if rest := subtractRegion(regions[j], regions[i]); len(rest) > 0 {
				rep.Overlaps = append(rep.Overlaps, Overlap{
					Earlier:  i,
					Later:    j,
					Conflict: rules[i].Action != rules[j].Action,
					Example:  samplePacket(inter),
				})
			}
			// 从 j 的剩余空间中扣除 i。
			var next []region
			for _, r := range remaining {
				next = append(next, subtractRegion(r, regions[i])...)
			}
			remaining = next
			rr.CoveredBy = append(rr.CoveredBy, i)
			if len(remaining) == 0 {
				break
			}
		}
		if len(remaining) == 0 {
			rr.Reachable = false
			inter, _ := intersectRegion(regions[j], regions[rr.CoveredBy[0]])
			pkt := samplePacket(inter)
			rr.Captured = &pkt
		} else {
			pkt := samplePacket(remaining[0])
			rr.Witness = &pkt
		}
		rep.Rules = append(rep.Rules, rr)
	}
	return rep
}

func (rep Report) String(rules []Rule) string {
	var out string
	out += "== 规则可达性 ==\n"
	for _, rr := range rep.Rules {
		r := rules[rr.RuleIndex]
		if rr.Reachable {
			out += fmt.Sprintf("[可达]   #%d %-12s %s\n        见证包: %s\n", rr.RuleIndex, r.Name, r, rr.Witness)
		} else {
			out += fmt.Sprintf("[被遮蔽] #%d %-12s %s\n        覆盖者: %v，被截获示例包: %s\n", rr.RuleIndex, r.Name, r, rr.CoveredBy, rr.Captured)
		}
	}
	if len(rep.Overlaps) > 0 {
		out += "== 部分重叠 ==\n"
		for _, ov := range rep.Overlaps {
			kind := "重叠"
			if ov.Conflict {
				kind = "冲突(动作不同)"
			}
			out += fmt.Sprintf("[%s] #%d(%s) 与 #%d(%s)，交集示例包: %s\n",
				kind, ov.Earlier, rules[ov.Earlier].Name, ov.Later, rules[ov.Later].Name, ov.Example)
		}
	} else {
		out += "== 部分重叠 ==\n无\n"
	}
	return out
}
