package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
)

func main() {
	inputPath := flag.String("input", "", "排考问题 JSON 文件路径（必填）")
	slotsOverride := flag.Int("slots", 0, "覆盖输入中的时段数（0 = 使用输入配置）")
	nodeLimit := flag.Int("maxnodes", 0, "搜索节点上限（0 = 不限；达到上限时结果不再保证最优）")
	top := flag.Int("top", 10, "展示个人惩罚最高的前 N 名学生")
	jsonOut := flag.Bool("json", false, "以 JSON 输出完整解")
	flag.Parse()

	if *inputPath == "" {
		fmt.Fprintln(os.Stderr, "用法: examsched -input problem.json [-slots N] [-maxnodes N] [-top N] [-json]")
		os.Exit(2)
	}
	data, err := os.ReadFile(*inputPath)
	if err != nil {
		fatal("读取输入失败: %v", err)
	}
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		fatal("解析输入 JSON 失败: %v", err)
	}
	if *slotsOverride > 0 {
		in.Slots = *slotsOverride
	}
	if err := validate(&in); err != nil {
		fatal("输入不合法: %v", err)
	}

	sol, err := Solve(&in, *nodeLimit)
	if err != nil {
		fatal("求解失败: %v", err)
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(sol); err != nil {
			fatal("输出 JSON 失败: %v", err)
		}
		return
	}
	printReport(&in, sol, *top)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "错误: "+format+"\n", args...)
	os.Exit(1)
}

func validate(in *Input) error {
	if len(in.Exams) == 0 {
		return fmt.Errorf("没有考试需要安排")
	}
	if len(in.Exams) > maxExams {
		return fmt.Errorf("考试数量 %d 超过上限 %d", len(in.Exams), maxExams)
	}
	if len(in.Rooms) == 0 {
		return fmt.Errorf("至少需要一个考场")
	}
	ids := map[string]bool{}
	for _, ex := range in.Exams {
		if ex.ID == "" {
			return fmt.Errorf("存在缺少 id 的考试")
		}
		if ids[ex.ID] {
			return fmt.Errorf("考试 id %q 重复", ex.ID)
		}
		ids[ex.ID] = true
		if len(ex.Students) == 0 {
			return fmt.Errorf("考试 %s 没有学生", ex.ID)
		}
	}
	for _, r := range in.Rooms {
		if r.Name == "" || r.Capacity <= 0 {
			return fmt.Errorf("考场定义不合法: %+v", r)
		}
	}
	if in.SlotsPerDay < 0 || in.Slots < 0 {
		return fmt.Errorf("时段配置不能为负")
	}
	return nil
}

func printReport(in *Input, sol *Solution, top int) {
	fmt.Printf("=== 考试安排（%d 个时段，%d 门考试，%d 个考场）===\n", sol.Slots, len(in.Exams), len(in.Rooms))
	bySlot := map[int][]Placement{}
	for _, p := range sol.Placements {
		bySlot[p.Slot] = append(bySlot[p.Slot], p)
	}
	for slot := 0; slot < sol.Slots; slot++ {
		list := bySlot[slot]
		sort.Slice(list, func(a, b int) bool { return list[a].Exam < list[b].Exam })
		if in.SlotsPerDay > 0 {
			fmt.Printf("时段 %d（第 %d 天第 %d 节）:\n", slot, slot/in.SlotsPerDay+1, slot%in.SlotsPerDay+1)
		} else {
			fmt.Printf("时段 %d:\n", slot)
		}
		if len(list) == 0 {
			fmt.Println("  （空）")
		}
		for _, p := range list {
			fmt.Printf("  %-10s %3d 人 -> 考场 %s\n", p.Exam, p.Enrollment, p.Room)
		}
	}

	fmt.Println("\n=== 冲突与惩罚核验 ===")
	problems := verify(in, sol)
	if len(problems) == 0 {
		fmt.Println("硬约束: 通过（无学生同时段冲突，考场容量与不重复占用均满足）")
		fmt.Printf("惩罚重算: 一致（最大个人惩罚 %d，总惩罚 %d）\n", sol.MaxPenalty, sol.TotalPenalty)
	} else {
		for _, p := range problems {
			fmt.Println("违规:", p)
		}
	}
	if sol.Proven {
		fmt.Println("最优性: 已证明（先最小化最大个人惩罚，再最小化总惩罚）")
	} else {
		fmt.Println("最优性: 未证明（达到搜索节点上限，为当前最优解）")
	}

	fmt.Println("\n=== 个人惩罚分布 ===")
	for _, kv := range penaltyHistogram(sol) {
		fmt.Printf("惩罚 %3d: %d 人\n", kv[0], kv[1])
	}
	fmt.Printf("\n个人惩罚最高的前 %d 名学生:\n", top)
	shown := 0
	for _, sp := range sol.PerStudent {
		if sp.Penalty == 0 && shown >= 3 {
			break
		}
		if shown >= top {
			break
		}
		fmt.Printf("  %-12s 时段 %v 惩罚 %d\n", sp.Student, sp.Slots, sp.Penalty)
		shown++
	}
	if shown == 0 {
		fmt.Println("  （所有学生均无连续考试，惩罚全为 0）")
	}
}
