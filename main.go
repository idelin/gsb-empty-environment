package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	input := flag.String("input", "", "输入 JSON 文件（缺省使用内置示例）")
	outJSON := flag.Bool("json", false, "以 JSON 输出完整报告")
	verbose := flag.Bool("v", false, "打印每个学生的惩罚明细")
	flag.Parse()

	in, err := LoadInput(*input)
	if err != nil {
		fmt.Fprintln(os.Stderr, "输入错误:", err)
		os.Exit(1)
	}
	rep, err := Solve(in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "求解失败:", err)
		os.Exit(1)
	}

	if *outJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fmt.Fprintln(os.Stderr, "输出失败:", err)
			os.Exit(1)
		}
		return
	}
	printReport(in, rep, *verbose)
}

func printReport(in *Input, rep *Report, verbose bool) {
	fmt.Println("=== 考试安排 ===")
	bySlot := map[int][]int{}
	for e, sl := range rep.Assign.Slot {
		bySlot[sl] = append(bySlot[sl], e)
	}
	for sl := 0; sl < in.NumSlots; sl++ {
		exams := bySlot[sl]
		if len(exams) == 0 {
			continue
		}
		fmt.Printf("时段 %d:\n", sl)
		for _, e := range exams {
			fmt.Printf("  %-4s %-8s 考场 %s\n", in.Exams[e].ID, in.Exams[e].Name, rep.Assign.Room[e])
		}
	}

	fmt.Println("\n=== 冲突核验 ===")
	if len(rep.Conflicts) == 0 {
		fmt.Println("学生冲突: 无 ✓")
	} else {
		for _, c := range rep.Conflicts {
			fmt.Println("冲突:", c)
		}
	}
	if len(rep.RoomViolations) == 0 {
		fmt.Println("考场约束: 无违规 ✓")
	} else {
		for _, v := range rep.RoomViolations {
			fmt.Println("考场违规:", v)
		}
	}

	fmt.Println("\n=== 惩罚核验（分级:", in.GapPenalties, "）===")
	for _, sp := range rep.StudentPenalty {
		fmt.Printf("学生 %-5s 惩罚 %d\n", sp.Student, sp.Penalty)
		if verbose {
			for _, d := range sp.Details {
				fmt.Println("   ", d)
			}
		}
	}
	fmt.Printf("\n最大个人惩罚: %d（首要目标，已最小化）\n", rep.MaxPenalty)
	fmt.Printf("总惩罚:       %d（次要目标，已最小化）\n", rep.TotalPenalty)
	fmt.Printf("搜索节点:     %d（穷举证明最优）\n", rep.Nodes)
}
