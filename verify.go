package main

import (
	"fmt"
	"sort"
)

// verify independently re-checks a solution against every constraint and
// recomputes the penalties from scratch. It returns a list of problems;
// an empty list means the solution is fully valid.
func verify(in *Input, sol *Solution) []string {
	var problems []string

	roomCap := map[string]int{}
	for _, r := range in.Rooms {
		roomCap[r.Name] = r.Capacity
	}
	examStudents := map[string][]string{}
	for _, ex := range in.Exams {
		examStudents[ex.ID] = ex.Students
	}

	// 1. Every exam placed exactly once, with valid slot and room.
	placed := map[string]int{}
	type slotRoom struct{ slot, room string }
	usedRoom := map[[2]string]string{} // (slot, room) -> exam
	studentSlotExam := map[string]map[int]string{}
	for _, p := range sol.Placements {
		if _, ok := examStudents[p.Exam]; !ok {
			problems = append(problems, fmt.Sprintf("安排了未知考试 %s", p.Exam))
			continue
		}
		placed[p.Exam]++
		if p.Slot < 0 || p.Slot >= sol.Slots {
			problems = append(problems, fmt.Sprintf("考试 %s 的时段 %d 超出范围 [0,%d)", p.Exam, p.Slot, sol.Slots))
		}
		cap_, ok := roomCap[p.Room]
		if !ok {
			problems = append(problems, fmt.Sprintf("考试 %s 使用了不存在的考场 %s", p.Exam, p.Room))
		} else if cap_ < p.Enrollment {
			problems = append(problems, fmt.Sprintf("考试 %s 人数 %d 超过考场 %s 容量 %d", p.Exam, p.Enrollment, p.Room, cap_))
		}
		key := [2]string{fmt.Sprint(p.Slot), p.Room}
		if other, dup := usedRoom[key]; dup {
			problems = append(problems, fmt.Sprintf("时段 %d 考场 %s 被 %s 和 %s 同时占用", p.Slot, p.Room, other, p.Exam))
		} else {
			usedRoom[key] = p.Exam
		}
		// 2. No student has two exams in the same slot.
		for _, st := range examStudents[p.Exam] {
			m := studentSlotExam[st]
			if m == nil {
				m = map[int]string{}
				studentSlotExam[st] = m
			}
			if other, dup := m[p.Slot]; dup {
				problems = append(problems, fmt.Sprintf("学生 %s 在时段 %d 同时有考试 %s 和 %s（冲突）", st, p.Slot, other, p.Exam))
			} else {
				m[p.Slot] = p.Exam
			}
		}
	}
	for _, ex := range in.Exams {
		if placed[ex.ID] == 0 {
			problems = append(problems, fmt.Sprintf("考试 %s 未被安排", ex.ID))
		} else if placed[ex.ID] > 1 {
			problems = append(problems, fmt.Sprintf("考试 %s 被安排了 %d 次", ex.ID, placed[ex.ID]))
		}
	}

	// 3. Recompute penalties independently and compare with the claim.
	table := penaltyTable(in, len(in.Exams))
	slotOf := map[string]int{}
	for _, p := range sol.Placements {
		slotOf[p.Exam] = p.Slot
	}
	studentSlots := map[string][]int{}
	for _, ex := range in.Exams {
		for _, st := range ex.Students {
			studentSlots[st] = append(studentSlots[st], slotOf[ex.ID])
		}
	}
	maxPen, total := 0, 0
	for _, slots := range studentSlots {
		p := studentPenalty(slots, in.SlotsPerDay, table)
		if p > maxPen {
			maxPen = p
		}
		total += p
	}
	if maxPen != sol.MaxPenalty {
		problems = append(problems, fmt.Sprintf("最大个人惩罚核验不符：声明 %d，重算 %d", sol.MaxPenalty, maxPen))
	}
	if total != sol.TotalPenalty {
		problems = append(problems, fmt.Sprintf("总惩罚核验不符：声明 %d，重算 %d", sol.TotalPenalty, total))
	}
	return problems
}

// penaltyHistogram summarizes how many students carry each penalty level.
func penaltyHistogram(sol *Solution) [][2]int {
	counts := map[int]int{}
	for _, sp := range sol.PerStudent {
		counts[sp.Penalty]++
	}
	levels := make([]int, 0, len(counts))
	for p := range counts {
		levels = append(levels, p)
	}
	sort.Ints(levels)
	out := make([][2]int, len(levels))
	for i, p := range levels {
		out[i] = [2]int{p, counts[p]}
	}
	return out
}
