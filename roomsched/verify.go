package main

import (
	"fmt"
	"math/bits"
	"sort"
)

// Verify 独立校验求解结果，不依赖求解器内部状态。任何一项不满足即返回错误。
func Verify(p *Problem, sol *Solution) error {
	byID := map[string]Meeting{}
	for _, m := range p.Meetings {
		byID[m.ID] = m
	}
	seen := map[string]bool{}

	// 1. 接受的会议：真实存在、块合法、容量足够
	for _, a := range sol.Accepted {
		m, ok := byID[a.Meeting.ID]
		if !ok || m != a.Meeting {
			return fmt.Errorf("接受的会议 %q 与输入不一致", a.Meeting.ID)
		}
		if seen[m.ID] {
			return fmt.Errorf("会议 %s 被重复安排", m.ID)
		}
		seen[m.ID] = true
		if a.Lo < 0 || a.Hi >= len(p.Rooms) || a.Lo > a.Hi {
			return fmt.Errorf("会议 %s 的房间块 [%d,%d] 非法", m.ID, a.Lo, a.Hi)
		}
		capSum := 0
		for r := a.Lo; r <= a.Hi; r++ {
			capSum += p.Rooms[r].Capacity
		}
		if capSum < m.Size {
			return fmt.Errorf("会议 %s 需要容量 %d，块容量仅 %d", m.ID, m.Size, capSum)
		}
	}

	// 2. 拒绝的会议：真实存在、权重账目一致
	rejectedWeight := 0
	for _, m := range sol.Rejected {
		if _, ok := byID[m.ID]; !ok {
			return fmt.Errorf("拒绝的会议 %q 不存在于输入", m.ID)
		}
		if seen[m.ID] {
			return fmt.Errorf("会议 %s 被重复安排", m.ID)
		}
		seen[m.ID] = true
		rejectedWeight += m.Weight
	}
	if rejectedWeight != sol.RejectedWeight {
		return fmt.Errorf("拒绝权重账目不符: 报告 %d，实际 %d", sol.RejectedWeight, rejectedWeight)
	}
	for id := range byID {
		if !seen[id] {
			return fmt.Errorf("会议 %s 既未被接受也未被拒绝", id)
		}
	}

	// 3. 重放分区：从全独立状态出发，逐场会议核对所需拆合与操作列表一致
	part := uint32(0)
	R := len(p.Rooms)
	opUsed := make([]bool, len(sol.Ops))
	accepted := append([]Assignment(nil), sol.Accepted...)
	sort.Slice(accepted, func(i, j int) bool {
		if accepted[i].Meeting.Start != accepted[j].Meeting.Start {
			return accepted[i].Meeting.Start < accepted[j].Meeting.Start
		}
		return accepted[i].Meeting.ID < accepted[j].Meeting.ID
	})
	for _, a := range accepted {
		newPart := blockPart(part, a.Lo, a.Hi, R)
		changed := part ^ newPart
		matched := 0
		for k, op := range sol.Ops {
			if op.MeetingID != a.Meeting.ID {
				continue
			}
			opUsed[k] = true
			matched++
			if op.Boundary < 0 || op.Boundary >= R-1 {
				return fmt.Errorf("会议 %s 的操作边界 %d 非法", a.Meeting.ID, op.Boundary)
			}
			if changed&(1<<uint(op.Boundary)) == 0 {
				return fmt.Errorf("会议 %s 在边界 %d 上有多余的拆合操作", a.Meeting.ID, op.Boundary)
			}
			if op.Merge != (newPart&(1<<uint(op.Boundary)) != 0) {
				return fmt.Errorf("会议 %s 在边界 %d 上的拆合方向错误", a.Meeting.ID, op.Boundary)
			}
			if op.End != a.Meeting.Start || op.End-op.Start != p.Prep {
				return fmt.Errorf("会议 %s 的准备时段 [%d,%d) 未在会议开始前恰好完成", a.Meeting.ID, op.Start, op.End)
			}
		}
		if matched != bits.OnesCount32(changed) {
			return fmt.Errorf("会议 %s 缺少拆合操作: 需要 %d 次，记录 %d 次", a.Meeting.ID, bits.OnesCount32(changed), matched)
		}
		part = newPart
	}
	for k, used := range opUsed {
		if !used {
			return fmt.Errorf("操作列表中存在无法归属的多余操作: %+v", sol.Ops[k])
		}
	}
	if sol.TotalOps != len(sol.Ops) {
		return fmt.Errorf("操作次数账目不符: 报告 %d，实际 %d", sol.TotalOps, len(sol.Ops))
	}

	// 4. 合并排他性：合并块存续期间，其任何房间不得被其他会议单独（或以其他块形式）占用
	for _, a1 := range accepted {
		if a1.Hi == a1.Lo {
			continue
		}
		for _, a2 := range accepted {
			if a2.Meeting.ID == a1.Meeting.ID {
				continue
			}
			overlapTime := a1.Meeting.Start < a2.Meeting.End && a2.Meeting.Start < a1.Meeting.End
			overlapRoom := a2.Lo <= a1.Hi && a1.Lo <= a2.Hi
			if overlapTime && overlapRoom {
				return fmt.Errorf("合并排他性违反: 会议 %s 在合并会议 %s（房间 %d-%d）存续期间占用了其房间",
					a2.Meeting.ID, a1.Meeting.ID, a1.Lo, a1.Hi)
			}
		}
	}

	// 5. 每房间时间线：会议互不重叠；准备操作不得与会议重叠
	//    （不同边界上的准备是不同隔墙的施工，允许并行）
	meetings := make([][][2]int, R)
	preps := make([][][2]int, R)
	for _, a := range accepted {
		for r := a.Lo; r <= a.Hi; r++ {
			meetings[r] = append(meetings[r], [2]int{a.Meeting.Start, a.Meeting.End})
		}
	}
	for _, op := range sol.Ops {
		if op.End > op.Start {
			preps[op.Boundary] = append(preps[op.Boundary], [2]int{op.Start, op.End})
			preps[op.Boundary+1] = append(preps[op.Boundary+1], [2]int{op.Start, op.End})
		}
	}
	for r := 0; r < R; r++ {
		mv := meetings[r]
		sort.Slice(mv, func(i, j int) bool { return mv[i][0] < mv[j][0] })
		for k := 1; k < len(mv); k++ {
			if mv[k][0] < mv[k-1][1] {
				return fmt.Errorf("房间 %s 的会议时间重叠: [%d,%d) 与 [%d,%d)",
					p.Rooms[r].Name, mv[k-1][0], mv[k-1][1], mv[k][0], mv[k][1])
			}
		}
		for _, pp := range preps[r] {
			for _, mm := range mv {
				if pp[0] < mm[1] && mm[0] < pp[1] {
					return fmt.Errorf("房间 %s 的准备时段 [%d,%d) 与会议 [%d,%d) 冲突",
						p.Rooms[r].Name, pp[0], pp[1], mm[0], mm[1])
				}
			}
		}
	}
	return nil
}
