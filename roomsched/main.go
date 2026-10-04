package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

func main() {
	var p *Problem
	if len(os.Args) > 1 {
		data, err := os.ReadFile(os.Args[1])
		if err != nil {
			fatal("读取输入失败", err)
		}
		p = &Problem{}
		if err := json.Unmarshal(data, p); err != nil {
			fatal("解析 JSON 失败", err)
		}
	} else {
		p = demoProblem()
		fmt.Println("（未提供输入文件，运行内置演示用例；用法: roomsched input.json）")
		fmt.Println()
	}
	if err := p.Validate(); err != nil {
		fatal("输入不合法", err)
	}

	sol := Solve(p)
	printSolution(p, sol)

	fmt.Println("== 校验 ==")
	if err := Verify(p, sol); err != nil {
		fmt.Println("校验失败:", err)
		os.Exit(1)
	}
	fmt.Println("校验通过: 容量满足；会议不换房；拆合准备时间满足；合并期间房间未被单独分配；各房间时间线无重叠。")
}

func fatal(msg string, err error) {
	fmt.Fprintln(os.Stderr, msg+":", err)
	os.Exit(1)
}

func blockName(p *Problem, lo, hi int) string {
	names := make([]string, 0, hi-lo+1)
	for r := lo; r <= hi; r++ {
		names = append(names, p.Rooms[r].Name)
	}
	return strings.Join(names, "+")
}

func printSolution(p *Problem, sol *Solution) {
	totalWeight := 0
	for _, m := range p.Meetings {
		totalWeight += m.Weight
	}
	fmt.Println("== 求解结果 ==")
	fmt.Printf("接受 %d 场（权重 %d），拒绝 %d 场（拒绝权重 %d），拆合操作 %d 次\n",
		len(sol.Accepted), totalWeight-sol.RejectedWeight, len(sol.Rejected), sol.RejectedWeight, sol.TotalOps)
	fmt.Println()

	fmt.Println("== 接受的会议 ==")
	for _, a := range sol.Accepted {
		fmt.Printf("  %-4s [%2d,%2d) 人数 %-2d 房间 %s\n",
			a.Meeting.ID, a.Meeting.Start, a.Meeting.End, a.Meeting.Size, blockName(p, a.Lo, a.Hi))
	}
	fmt.Println()

	fmt.Println("== 拒绝的会议 ==")
	if len(sol.Rejected) == 0 {
		fmt.Println("  （无）")
	}
	for _, m := range sol.Rejected {
		fmt.Printf("  %-4s [%2d,%2d) 人数 %-2d 权重 %d\n", m.ID, m.Start, m.End, m.Size, m.Weight)
	}
	fmt.Println()

	fmt.Println("== 拆合操作序列 ==")
	if len(sol.Ops) == 0 {
		fmt.Println("  （无）")
	}
	ops := append([]Op(nil), sol.Ops...)
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].Start != ops[j].Start {
			return ops[i].Start < ops[j].Start
		}
		return ops[i].Boundary < ops[j].Boundary
	})
	for _, op := range ops {
		kind := "合并"
		if !op.Merge {
			kind = "拆分"
		}
		fmt.Printf("  [%2d,%2d) %s %s|%s （为会议 %s 准备）\n",
			op.Start, op.End, kind, p.Rooms[op.Boundary].Name, p.Rooms[op.Boundary+1].Name, op.MeetingID)
	}
	fmt.Println()

	fmt.Println("== 房间状态时间线 ==")
	type entry struct {
		s, e  int
		label string
	}
	lines := make([][]entry, len(p.Rooms))
	for _, a := range sol.Accepted {
		for r := a.Lo; r <= a.Hi; r++ {
			lines[r] = append(lines[r], entry{a.Meeting.Start, a.Meeting.End,
				fmt.Sprintf("会议%s[%s]", a.Meeting.ID, blockName(p, a.Lo, a.Hi))})
		}
	}
	for _, op := range sol.Ops {
		kind := "合并"
		if !op.Merge {
			kind = "拆分"
		}
		label := fmt.Sprintf("准备(%s %s|%s→%s)", kind,
			p.Rooms[op.Boundary].Name, p.Rooms[op.Boundary+1].Name, op.MeetingID)
		if op.End > op.Start {
			lines[op.Boundary] = append(lines[op.Boundary], entry{op.Start, op.End, label})
			lines[op.Boundary+1] = append(lines[op.Boundary+1], entry{op.Start, op.End, label})
		}
	}
	for r, room := range p.Rooms {
		ev := lines[r]
		sort.Slice(ev, func(i, j int) bool { return ev[i].s < ev[j].s })
		var parts []string
		cursor := -1
		for _, e := range ev {
			if cursor >= 0 && e.s > cursor {
				parts = append(parts, fmt.Sprintf("[%d,%d) 空闲", cursor, e.s))
			}
			parts = append(parts, fmt.Sprintf("[%d,%d) %s", e.s, e.e, e.label))
			cursor = e.e
		}
		if len(parts) == 0 {
			parts = append(parts, "（全程空闲）")
		}
		fmt.Printf("  房间 %-2s %s\n", room.Name, strings.Join(parts, "  |  "))
	}
	fmt.Println()
}

// demoProblem 内置演示：3 间相邻房间，10 场会议。
// 预期最优：9 点前空闲时合并 B+C 供 m2/m4 复用，拒绝 m3,m5,m6（权重 4），拆合 4 次。
func demoProblem() *Problem {
	return &Problem{
		Prep: 1,
		Rooms: []Room{
			{Name: "A", Capacity: 4},
			{Name: "B", Capacity: 4},
			{Name: "C", Capacity: 4},
		},
		Meetings: []Meeting{
			{ID: "m1", Start: 9, End: 10, Size: 3, Weight: 2},
			{ID: "m2", Start: 9, End: 10, Size: 3, Weight: 2},
			{ID: "m3", Start: 9, End: 10, Size: 3, Weight: 2},
			{ID: "m4", Start: 10, End: 12, Size: 8, Weight: 10},
			{ID: "m5", Start: 12, End: 13, Size: 3, Weight: 1},
			{ID: "m6", Start: 12, End: 13, Size: 3, Weight: 1},
			{ID: "m7", Start: 13, End: 14, Size: 12, Weight: 20},
			{ID: "m8", Start: 15, End: 16, Size: 2, Weight: 3},
			{ID: "m9", Start: 15, End: 16, Size: 2, Weight: 3},
			{ID: "m10", Start: 15, End: 16, Size: 2, Weight: 3},
		},
	}
}
