package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

func main() {
	input := flag.String("input", "", "JSON 配置文件路径, '-' 表示标准输入; 缺省运行内置示例")
	flag.Parse()

	var cfg Config
	if *input == "" {
		cfg = demoConfig()
		fmt.Println("(未指定 -input, 运行内置示例)")
	} else {
		data, err := readInput(*input)
		if err != nil {
			fatal(err)
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			fatal(fmt.Errorf("解析配置失败: %w", err))
		}
	}
	if err := cfg.check(); err != nil {
		fatal(fmt.Errorf("配置非法: %w", err))
	}

	sol := Solve(cfg)
	tl := BuildTimeline(cfg, sol)
	printSolution(cfg, sol, tl)

	if violations := Validate(cfg, sol, tl); len(violations) > 0 {
		fmt.Println("校验失败:")
		for _, v := range violations {
			fmt.Println("  - " + v)
		}
		os.Exit(1)
	}
	fmt.Println("校验通过: 合并期间房间未被分别分配, 容量/准备时间/操作数一致")
}

func readInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(2)
}

func printSolution(cfg Config, sol Solution, tl [][]Segment) {
	fmt.Println("=== 安排结果 ===")
	rejIDs := make([]string, 0, len(sol.Rejected))
	for _, m := range sol.Rejected {
		rejIDs = append(rejIDs, m.ID)
	}
	fmt.Printf("拒绝权重: %d (拒绝: %s)\n", sol.RejWeight, strings.Join(rejIDs, ", "))
	fmt.Printf("拆合操作数: %d\n", sol.Ops)

	assigns := append([]Assignment(nil), sol.Assigns...)
	sort.Slice(assigns, func(i, j int) bool {
		return assigns[i].Meeting.Start < assigns[j].Meeting.Start
	})
	fmt.Println("会议分配:")
	for _, a := range assigns {
		fmt.Printf("  %s [%d,%d) -> 房间 %d-%d (需求 %d 间, 权重 %d)\n",
			a.Meeting.ID, a.Meeting.Start, a.Meeting.End, a.Lo+1, a.Hi+1, a.Meeting.Rooms, a.Meeting.Weight)
	}

	fmt.Println("房间状态时间线:")
	fmt.Print(RenderTimeline(tl))
}

func demoConfig() Config {
	return Config{
		NumRooms:  4,
		MergeTime: 1,
		SplitTime: 1,
		Meetings: []Meeting{
			{ID: "A", Start: 1, End: 6, Rooms: 2, Weight: 5},
			{ID: "B", Start: 2, End: 4, Rooms: 1, Weight: 2},
			{ID: "C", Start: 3, End: 9, Rooms: 3, Weight: 9},
			{ID: "D", Start: 5, End: 7, Rooms: 1, Weight: 1},
			{ID: "E", Start: 7, End: 11, Rooms: 2, Weight: 4},
			{ID: "F", Start: 10, End: 13, Rooms: 4, Weight: 8},
			{ID: "G", Start: 12, End: 14, Rooms: 1, Weight: 2},
			{ID: "H", Start: 8, End: 10, Rooms: 1, Weight: 3},
		},
	}
}
