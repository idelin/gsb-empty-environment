package main

import (
	"fmt"
	"sort"
)

// verify 独立重算一份完整核验：
// 1) 学生冲突（同一学生两场考试同时段）；
// 2) 考场约束（同时段不重复使用、容量足够）；
// 3) 每个学生的分级惩罚、最大个人惩罚与总惩罚。
// 结果写入 rep，供输出与测试断言。
func verify(in *Input, rep *Report, stuNames []string, stuExams [][]int) {
	slots := rep.Assign.Slot
	sizes := in.enrollmentSize()

	// 1) 学生冲突核验。
	for si, exams := range stuExams {
		seen := map[int]int{}
		for _, e := range exams {
			if prev, ok := seen[slots[e]]; ok {
				rep.Conflicts = append(rep.Conflicts, fmt.Sprintf(
					"学生 %s 的考试 %s 与 %s 同在时段 %d",
					stuNames[si], in.Exams[prev].ID, in.Exams[e].ID, slots[e]))
			}
			seen[slots[e]] = e
		}
	}

	// 2) 考场核验：同时段不重复、容量足够。
	roomCap := map[string]int{}
	for _, r := range in.Rooms {
		roomCap[r.ID] = r.Capacity
	}
	type slotRoom struct {
		slot int
		room string
	}
	usedRoom := map[slotRoom]string{}
	for e := range in.Exams {
		cap, ok := roomCap[rep.Assign.Room[e]]
		if !ok {
			rep.RoomViolations = append(rep.RoomViolations, fmt.Sprintf(
				"考试 %s 分配到不存在的考场 %s", in.Exams[e].ID, rep.Assign.Room[e]))
			continue
		}
		if sizes[e] > cap {
			rep.RoomViolations = append(rep.RoomViolations, fmt.Sprintf(
				"考试 %s 人数 %d 超过考场 %s 容量 %d",
				in.Exams[e].ID, sizes[e], rep.Assign.Room[e], cap))
		}
		key := slotRoom{slots[e], rep.Assign.Room[e]}
		if prev, dup := usedRoom[key]; dup {
			rep.RoomViolations = append(rep.RoomViolations, fmt.Sprintf(
				"考场 %s 在时段 %d 被 %s 和 %s 重复使用",
				key.room, key.slot, prev, in.Exams[e].ID))
		}
		usedRoom[key] = in.Exams[e].ID
	}

	// 3) 惩罚核验：逐学生重算分级惩罚。
	rep.MaxPenalty, rep.TotalPenalty = 0, 0
	for si, exams := range stuExams {
		sp := StudentPen{Student: stuNames[si]}
		for a := 0; a < len(exams); a++ {
			for b := a + 1; b < len(exams); b++ {
				gap := slots[exams[a]] - slots[exams[b]]
				if gap < 0 {
					gap = -gap
				}
				p := in.Penalty(gap)
				if p > 0 {
					sp.Details = append(sp.Details, fmt.Sprintf(
						"%s(时段%d) 与 %s(时段%d) 间隔 %d → 罚 %d",
						in.Exams[exams[a]].ID, slots[exams[a]],
						in.Exams[exams[b]].ID, slots[exams[b]], gap, p))
				}
				sp.Penalty += p
			}
		}
		rep.TotalPenalty += sp.Penalty
		if sp.Penalty > rep.MaxPenalty {
			rep.MaxPenalty = sp.Penalty
		}
		rep.StudentPenalty = append(rep.StudentPenalty, sp)
	}
	sort.Slice(rep.StudentPenalty, func(a, b int) bool {
		if rep.StudentPenalty[a].Penalty != rep.StudentPenalty[b].Penalty {
			return rep.StudentPenalty[a].Penalty > rep.StudentPenalty[b].Penalty
		}
		return rep.StudentPenalty[a].Student < rep.StudentPenalty[b].Student
	})
}
