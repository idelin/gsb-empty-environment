package main

import (
	"testing"
)

// bruteForce 独立枚举所有时段分配（不管考场），返回字典序最优的
// (最大个人惩罚, 总惩罚)，作为求解器的对照真值。
func bruteForce(in *Input) (int, int) {
	n := len(in.Exams)
	_, stuExams := in.StudentExams()
	slot := make([]int, n)
	bestMax, bestTotal := -1, 0
	var rec func(e int)
	rec = func(e int) {
		if e == n {
			// 冲突检查
			for _, exams := range stuExams {
				seen := map[int]bool{}
				for _, x := range exams {
					if seen[slot[x]] {
						return
					}
					seen[slot[x]] = true
				}
			}
			maxP, total := 0, 0
			for _, exams := range stuExams {
				p := 0
				for a := 0; a < len(exams); a++ {
					for b := a + 1; b < len(exams); b++ {
						g := slot[exams[a]] - slot[exams[b]]
						if g < 0 {
							g = -g
						}
						p += in.Penalty(g)
					}
				}
				total += p
				if p > maxP {
					maxP = p
				}
			}
			if bestMax < 0 || maxP < bestMax || (maxP == bestMax && total < bestTotal) {
				bestMax, bestTotal = maxP, total
			}
			return
		}
		for sl := 0; sl < in.NumSlots; sl++ {
			slot[e] = sl
			rec(e + 1)
		}
	}
	rec(0)
	return bestMax, bestTotal
}

// TestSolveMatchesBruteForce 在多个小规模用例上对照暴力枚举。
func TestSolveMatchesBruteForce(t *testing.T) {
	cases := []Input{
		{
			NumSlots: 3, GapPenalties: []int{10, 4},
			Exams: []Exam{{ID: "A"}, {ID: "B"}, {ID: "C"}, {ID: "D"}},
			Rooms: []Room{{ID: "R1", Capacity: 10}, {ID: "R2", Capacity: 10}},
			Enrollments: map[string][]string{
				"x": {"A", "B", "C"}, "y": {"B", "D"}, "z": {"C", "D"},
			},
		},
		{
			NumSlots: 4, GapPenalties: []int{5, 2, 1},
			Exams: []Exam{{ID: "A"}, {ID: "B"}, {ID: "C"}, {ID: "D"}, {ID: "E"}},
			Rooms: []Room{{ID: "R1", Capacity: 3}, {ID: "R2", Capacity: 3}},
			Enrollments: map[string][]string{
				"p": {"A", "B"}, "q": {"A", "C", "E"}, "r": {"B", "D"}, "s": {"D", "E"},
			},
		},
		defaultInput(),
	}
	for i := range cases {
		in := cases[i]
		if err := in.Validate(); err != nil {
			t.Fatalf("用例 %d 输入非法: %v", i, err)
		}
		rep, err := Solve(&in)
		if err != nil {
			t.Fatalf("用例 %d 求解失败: %v", i, err)
		}
		wantMax, wantTotal := bruteForce(&in)
		if rep.MaxPenalty != wantMax || rep.TotalPenalty != wantTotal {
			t.Errorf("用例 %d: 求解器 (%d,%d)，暴力真值 (%d,%d)",
				i, rep.MaxPenalty, rep.TotalPenalty, wantMax, wantTotal)
		}
		if len(rep.Conflicts) != 0 || len(rep.RoomViolations) != 0 {
			t.Errorf("用例 %d: 最优解不应有冲突或考场违规: %v %v",
				i, rep.Conflicts, rep.RoomViolations)
		}
	}
}

// TestMaxPenaltyFirst 构造一个“牺牲少数学生可压低总惩罚”的场景，
// 验证求解器优先最小化最大个人惩罚，而不是只优化总和。
func TestMaxPenaltyFirst(t *testing.T) {
	in := Input{
		NumSlots: 3, GapPenalties: []int{10, 1},
		Exams: []Exam{{ID: "A"}, {ID: "B"}, {ID: "C"}, {ID: "D"}},
		Rooms: []Room{{ID: "R1", Capacity: 10}, {ID: "R2", Capacity: 10}},
		Enrollments: map[string][]string{
			// 学生 x 修 A,B,C；其余学生各修一门与 D 的组合。
			"x": {"A", "B", "C"},
			"y": {"A", "D"},
			"z": {"B", "D"},
		},
	}
	rep, err := Solve(&in)
	if err != nil {
		t.Fatal(err)
	}
	// 字典序最优：最大个人惩罚必须最小。
	wantMax, _ := bruteForce(&in)
	if rep.MaxPenalty != wantMax {
		t.Fatalf("最大个人惩罚 %d，期望 %d", rep.MaxPenalty, wantMax)
	}
	// 确认不存在“总惩罚更低但最大惩罚更高”的安排被误选：
	// 直接检查最优解中没有任何学生惩罚超过 rep.MaxPenalty。
	for _, sp := range rep.StudentPenalty {
		if sp.Penalty > rep.MaxPenalty {
			t.Errorf("学生 %s 惩罚 %d 超过报告的最大值 %d", sp.Student, sp.Penalty, rep.MaxPenalty)
		}
	}
}

// TestVerifyDetectsConflict 验证核验器能发现人为制造的冲突与考场违规。
func TestVerifyDetectsConflict(t *testing.T) {
	in := Input{
		NumSlots: 2, GapPenalties: []int{10},
		Exams: []Exam{{ID: "A"}, {ID: "B"}},
		Rooms: []Room{{ID: "R1", Capacity: 1}},
		Enrollments: map[string][]string{
			"x": {"A", "B"},
		},
	}
	// 故意把两场冲突考试排在同一时段、同一考场。
	rep := &Report{Assign: Assignment{Slot: []int{0, 0}, Room: []string{"R1", "R1"}}}
	names, lists := in.StudentExams()
	verify(&in, rep, names, lists)
	if len(rep.Conflicts) == 0 {
		t.Error("应检测到学生冲突")
	}
	if len(rep.RoomViolations) == 0 {
		t.Error("应检测到考场重复使用")
	}
}

// TestTooManyExams 验证超过 12 场考试被拒绝。
func TestTooManyExams(t *testing.T) {
	in := Input{NumSlots: 2, GapPenalties: []int{1}, Rooms: []Room{{ID: "R", Capacity: 1}}}
	for i := 0; i < MaxExams+1; i++ {
		in.Exams = append(in.Exams, Exam{ID: string(rune('A' + i))})
	}
	if err := in.Validate(); err == nil {
		t.Error("超过 12 场考试应被拒绝")
	}
}

// TestInfeasible 验证资源不足时返回错误而非错误答案。
func TestInfeasible(t *testing.T) {
	in := Input{
		NumSlots: 1, GapPenalties: []int{1},
		Exams: []Exam{{ID: "A"}, {ID: "B"}},
		Rooms: []Room{{ID: "R1", Capacity: 10}, {ID: "R2", Capacity: 10}},
		Enrollments: map[string][]string{
			"x": {"A", "B"}, // 冲突考试，但只有一个时段
		},
	}
	if _, err := Solve(&in); err == nil {
		t.Error("无可行安排时应返回错误")
	}
}
