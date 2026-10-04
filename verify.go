package main

import "fmt"

// Report 是三条不变量的独立验证结果：总量、保底、组内先来先服务。
type Report struct {
	TotalCapacityOK bool     `json:"totalCapacityOK"`
	MinGuaranteeOK  bool     `json:"minGuaranteeOK"`
	GroupFIFOOK     bool     `json:"groupFIFOOK"`
	Violations      []string `json:"violations,omitempty"`
}

// OK 当且仅当三条不变量全部成立。
func (r Report) OK() bool {
	return r.TotalCapacityOK && r.MinGuaranteeOK && r.GroupFIFOOK
}

// Verify 不依赖模拟器内部状态，仅根据输入场景与输出许可序列重放并校验：
//  1. 总量：每轮发出的许可数不超过总容量；
//  2. 保底：每轮各组获批数不少于 min(本轮需求, 保底额度)；
//  3. 组内先来先服务：同组许可的批准顺序与请求到达顺序一致；
//     另校验每张许可在发放时确实处于待批准状态（未被取消、未重复发放）。
func Verify(sc Scenario, out []RoundResult) Report {
	rep := Report{TotalCapacityOK: true, MinGuaranteeOK: true, GroupFIFOOK: true}
	fail := func(format string, args ...any) {
		rep.Violations = append(rep.Violations, fmt.Sprintf(format, args...))
	}

	if len(out) != len(sc.Rounds) {
		rep.TotalCapacityOK = false
		rep.MinGuaranteeOK = false
		rep.GroupFIFOOK = false
		fail("output has %d rounds, scenario has %d", len(out), len(sc.Rounds))
		return rep
	}

	type pendReq struct {
		group string
		seq   int
	}
	pending := map[string]pendReq{}
	lastGrantedSeq := map[string]int{} // 各组最近一次获批请求的到达序号
	seq := 0

	for i, rr := range out {
		round := i + 1
		// 重放本轮事件（与模拟器约定一致：先事件后分配）。
		for _, e := range sc.Rounds[i].Events {
			switch e.Type {
			case "request":
				seq++
				pending[e.ID] = pendReq{group: e.Group, seq: seq}
			case "cancel":
				delete(pending, e.ID)
			}
		}

		// 不变量 1：总量。
		if len(rr.Grants) > sc.Capacity {
			rep.TotalCapacityOK = false
			fail("round %d: granted %d permits exceeds capacity %d", round, len(rr.Grants), sc.Capacity)
		}

		// 分配时刻各组需求快照，用于保底校验。
		demand := map[string]int{}
		for _, p := range pending {
			demand[p.group]++
		}

		got := map[string]int{}
		for _, g := range rr.Grants {
			p, ok := pending[g.ID]
			if !ok {
				rep.GroupFIFOOK = false
				fail("round %d: grant %q was not pending (unknown, cancelled, or already granted)", round, g.ID)
				continue
			}
			if p.group != g.Group {
				rep.GroupFIFOOK = false
				fail("round %d: grant %q belongs to group %q, recorded as %q", round, g.ID, p.group, g.Group)
			}
			delete(pending, g.ID)
			// 不变量 3：组内 FIFO（到达序号必须严格递增）。
			if p.seq <= lastGrantedSeq[p.group] {
				rep.GroupFIFOOK = false
				fail("round %d: group %q granted %q (seq %d) after a later request (seq %d)",
					round, p.group, g.ID, p.seq, lastGrantedSeq[p.group])
			}
			lastGrantedSeq[p.group] = p.seq
			got[p.group]++
		}

		// 不变量 2：保底。
		for group, m := range sc.Groups {
			need := min(demand[group], m)
			if got[group] < need {
				rep.MinGuaranteeOK = false
				fail("round %d: group %q got %d permits, below guaranteed %d (demand %d, min %d)",
					round, group, got[group], need, demand[group], m)
			}
		}
	}
	return rep
}
