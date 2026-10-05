package main

import (
	"fmt"
	"sort"
)

// Report 是求解与核验结果。
type Report struct {
	Assign         Assignment   `json:"assignment"`
	StudentPenalty []StudentPen `json:"student_penalties"`
	MaxPenalty     int          `json:"max_penalty"`
	TotalPenalty   int          `json:"total_penalty"`
	Conflicts      []string     `json:"conflicts"`       // 应为空
	RoomViolations []string     `json:"room_violations"` // 应为空
	Nodes          int          `json:"search_nodes"`
	Optimal        bool         `json:"optimal"`
}

// StudentPen 记录单个学生的惩罚明细。
type StudentPen struct {
	Student string   `json:"student"`
	Penalty int      `json:"penalty"`
	Details []string `json:"details,omitempty"`
}

// Solve 用分支限界搜索字典序最优目标：
// 第一目标最小化最大个人惩罚，第二目标最小化总惩罚。
// 这样不会为了压低总和而让少数学生承担全部连续考试。
func Solve(in *Input) (*Report, error) {
	n := len(in.Exams)
	stuNames, stuExams := in.StudentExams()
	sizes := in.enrollmentSize()

	// 冲突图（邻接位掩码）：共享学生的考试不能同时段。
	conflict := make([]uint16, n)
	for _, exams := range stuExams {
		for _, a := range exams {
			for _, b := range exams {
				if a != b {
					conflict[a] |= 1 << uint(b)
				}
			}
		}
	}

	// 回溯顺序：度大的考试先排。
	degree := make([]int, n)
	for i := range conflict {
		degree[i] = bitsOn(conflict[i])
	}
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		if degree[order[a]] != degree[order[b]] {
			return degree[order[a]] > degree[order[b]]
		}
		return order[a] < order[b]
	})

	// 每个考试的学生列表（学生下标），用于增量计算惩罚。
	examStudents := make([][]int, n)
	for si, exams := range stuExams {
		for _, e := range exams {
			examStudents[e] = append(examStudents[e], si)
		}
	}

	// 考场容量降序，用于逐时段可行性检查（Hall 条件的排序形式）。
	roomCaps := make([]int, len(in.Rooms))
	for i, r := range in.Rooms {
		roomCaps[i] = r.Capacity
	}
	sort.Sort(sort.Reverse(sort.IntSlice(roomCaps)))

	s := &search{
		in:           in,
		n:            n,
		order:        order,
		conflict:     conflict,
		stuExams:     stuExams,
		examStudents: examStudents,
		sizes:        sizes,
		roomCaps:     roomCaps,
		slot:         make([]int, n),
		slotSizes:    make([][]int, in.NumSlots),
		stuPen:       make([]int, len(stuNames)),
		bestMax:      int(^uint(0) >> 1),
		bestTotal:    int(^uint(0) >> 1),
	}
	for i := range s.slot {
		s.slot[i] = -1
	}

	s.place(0)
	if s.best == nil {
		return nil, fmt.Errorf("无可行安排：时段或考场资源不足（%d 个时段、%d 个考场）", in.NumSlots, len(in.Rooms))
	}

	assign := Assignment{Slot: append([]int(nil), s.best...), Room: make([]string, n)}
	if err := assignRooms(in, assign.Slot, sizes, assign.Room); err != nil {
		return nil, err
	}

	rep := &Report{Assign: assign, Nodes: s.nodes, Optimal: true}
	verify(in, rep, stuNames, stuExams)
	return rep, nil
}

type search struct {
	in           *Input
	n            int
	order        []int
	conflict     []uint16
	stuExams     [][]int
	examStudents [][]int
	sizes        []int
	roomCaps     []int

	slot      []int   // 当前部分解
	slotSizes [][]int // 每个时段已排考试的规模
	stuPen    []int   // 当前每个学生的累计惩罚
	curMax    int
	curTotal  int

	best      []int
	bestMax   int
	bestTotal int
	nodes     int
}

func (s *search) place(depth int) {
	s.nodes++
	if depth == s.n {
		// 平移对称破缺：整体平移不改变间隔与惩罚，
		// 只保留最小时段为 0 的代表解。
		for _, sl := range s.slot {
			if sl == 0 {
				goto canonical
			}
		}
		return
	canonical:
		if s.curMax < s.bestMax || (s.curMax == s.bestMax && s.curTotal < s.bestTotal) {
			s.bestMax, s.bestTotal = s.curMax, s.curTotal
			s.best = append([]int(nil), s.slot...)
		}
		return
	}
	// 剪枝：部分解的惩罚只会增长。
	if s.curMax > s.bestMax || (s.curMax == s.bestMax && s.curTotal >= s.bestTotal) {
		return
	}
	e := s.order[depth]

	// 已被冲突考试占用的时段。
	used := uint16(0)
	for _, f := range s.order[:depth] {
		if s.slot[f] >= 0 && s.conflict[e]&(1<<uint(f)) != 0 {
			used |= 1 << uint(s.slot[f])
		}
	}

	for slot := 0; slot < s.in.NumSlots; slot++ {
		if used&(1<<uint(slot)) != 0 {
			continue
		}
		if !s.slotRoomOK(slot, s.sizes[e]) {
			continue
		}
		// 增量惩罚：与该考试每位学生已排的考试配对。
		delta := 0
		type undo struct{ stu, add int }
		var undos []undo
		for _, si := range s.examStudents[e] {
			add := 0
			for _, f := range s.stuExams[si] {
				if s.slot[f] >= 0 {
					gap := slot - s.slot[f]
					if gap < 0 {
						gap = -gap
					}
					add += s.in.Penalty(gap)
				}
			}
			if add > 0 {
				s.stuPen[si] += add
				undos = append(undos, undo{si, add})
				delta += add
			}
		}
		oldMax, oldTotal := s.curMax, s.curTotal
		s.curTotal += delta
		s.curMax = 0
		for _, p := range s.stuPen {
			if p > s.curMax {
				s.curMax = p
			}
		}

		s.slot[e] = slot
		s.slotSizes[slot] = append(s.slotSizes[slot], s.sizes[e])
		s.place(depth + 1)
		s.slot[e] = -1
		s.slotSizes[slot] = s.slotSizes[slot][:len(s.slotSizes[slot])-1]

		for _, u := range undos {
			s.stuPen[u.stu] -= u.add
		}
		s.curMax, s.curTotal = oldMax, oldTotal
	}
}

// slotRoomOK 检查把规模为 size 的考试放进 slot 后，
// 该时段的考试能否一一装入考场（排序形式的匹配条件）。
func (s *search) slotRoomOK(slot, size int) bool {
	exams := append(append([]int(nil), s.slotSizes[slot]...), size)
	if len(exams) > len(s.roomCaps) {
		return false
	}
	sort.Sort(sort.Reverse(sort.IntSlice(exams)))
	for i, sz := range exams {
		if sz > s.roomCaps[i] {
			return false
		}
	}
	return true
}

// assignRooms 为每个时段把考试贪心分配到考场：
// 规模大的考试优先，分配能容纳它的最小考场。
func assignRooms(in *Input, slots, sizes []int, out []string) error {
	type sizedRoom struct {
		id  string
		cap int
	}
	bySlot := map[int][]int{}
	for e, sl := range slots {
		bySlot[sl] = append(bySlot[sl], e)
	}
	for sl, exams := range bySlot {
		sort.Slice(exams, func(a, b int) bool { return sizes[exams[a]] > sizes[exams[b]] })
		rooms := make([]sizedRoom, len(in.Rooms))
		for i, r := range in.Rooms {
			rooms[i] = sizedRoom{r.ID, r.Capacity}
		}
		sort.Slice(rooms, func(a, b int) bool { return rooms[a].cap < rooms[b].cap })
		used := make([]bool, len(rooms))
		for _, e := range exams {
			placed := false
			for ri, rm := range rooms {
				if !used[ri] && rm.cap >= sizes[e] {
					used[ri] = true
					out[e] = rm.id
					placed = true
					break
				}
			}
			if !placed {
				return fmt.Errorf("时段 %d 无法为考试 %s 分配考场", sl, in.Exams[e].ID)
			}
		}
	}
	return nil
}

func bitsOn(x uint16) int {
	c := 0
	for x != 0 {
		x &= x - 1
		c++
	}
	return c
}
