package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strings"
)

// inputJSON 是输入文件格式:
// {
//   "nodes": ["A","B",...],            // 可省略, 省略时从路由表推导
//   "destinations": ["D1",...],        // 可省略, 省略时取路由表中出现过的所有目的
//   "old": {"A": {"D1": "B", ...}},    // 旧下一跳表: 节点 -> 目的 -> 下一跳
//   "new": {...}                       // 新下一跳表
// }
type inputJSON struct {
	Nodes        []string                     `json:"nodes"`
	Destinations []string                     `json:"destinations"`
	Old          map[string]map[string]string `json:"old"`
	New          map[string]map[string]string `json:"new"`
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	var data []byte
	var err error
	if len(args) == 0 || args[0] == "-" {
		data, err = ioutil.ReadAll(os.Stdin)
	} else {
		data, err = ioutil.ReadFile(args[0])
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取输入失败: %v\n", err)
		return 2
	}
	var in inputJSON
	if err := json.Unmarshal(data, &in); err != nil {
		fmt.Fprintf(os.Stderr, "JSON 解析失败: %v\n", err)
		return 2
	}
	nodes := in.Nodes
	if len(nodes) == 0 {
		nodes = deriveNodes(in)
	}
	dests := in.Destinations
	if len(dests) == 0 {
		dests = deriveDests(in)
	}
	prob, err := NewProblem(nodes, dests, in.Old, in.New)
	if err != nil {
		fmt.Fprintf(os.Stderr, "输入非法: %v\n", err)
		return 2
	}

	fmt.Println("=== 路由更新规划器 ===")
	fmt.Printf("节点 (%d): %s\n", len(prob.Nodes), strings.Join(prob.Nodes, ", "))
	fmt.Printf("目的节点 (%d): %s\n", len(prob.Dests), strings.Join(prob.Dests, ", "))
	fmt.Printf("需要更新的节点 (%d): %s\n", len(prob.useful), strings.Join(prob.namesOf(prob.usefulMask), ", "))

	if len(prob.useful) == 0 {
		fmt.Println("\n旧路由与新路由完全一致, 无需更新。")
		return 0
	}

	fmt.Println("\n[1] 校验初始状态 (全部使用旧路由)")
	ok, evs := prob.checkState(0)
	printEvidence(evs, "  ")
	if !ok {
		fmt.Println("!! 旧路由本身存在转发环或不可达, 无法规划。")
		return 1
	}

	fmt.Println("\n[2] 校验目标状态 (全部使用新路由)")
	ok, evs = prob.checkState(prob.usefulMask)
	printEvidence(evs, "  ")
	if !ok {
		fmt.Println("!! 新路由本身存在转发环或不可达, 无法规划。")
		return 1
	}

	fmt.Println("\n[3] 尝试单节点逐一更新 (每步只切换一个节点)")
	moves, ok, explored := prob.FindSchedule(1)
	if ok {
		fmt.Printf("  存在安全的单节点更新顺序 (搜索了 %d 个状态)。\n", explored)
	} else {
		fmt.Printf("  不存在安全的单节点更新顺序 (已穷举 %d 个状态)。\n", explored)
		fmt.Println("  从初始状态出发, 逐一更新每个节点的直接后果:")
		for _, u := range prob.useful {
			st := uint64(1) << uint(u)
			sok, sevs := prob.checkState(st)
			if sok {
				fmt.Printf("    更新 %s -> 状态安全, 但之后所有单节点续接均失败 (死胡同)\n", prob.Nodes[u])
				continue
			}
			for _, ev := range sevs {
				if ev.OK {
					continue
				}
				if ev.Cycle != nil {
					fmt.Printf("    更新 %s -> 目的 %s: 转发环 %s\n", prob.Nodes[u], ev.Dest, strings.Join(ev.Cycle, " -> "))
				} else if len(ev.Failures) > 0 {
					fmt.Printf("    更新 %s -> 目的 %s: %s\n", prob.Nodes[u], ev.Dest, ev.Failures[0])
				}
			}
		}
		fmt.Println("\n[4] 搜索最小同步更新组")
		for k := 2; ; k++ {
			moves, ok, explored = prob.FindSchedule(k)
			if ok {
				fmt.Printf("  最小同步更新组大小 k = %d (搜索了 %d 个状态)。\n", k, explored)
				break
			}
			fmt.Printf("  组大小 k = %d 不可行 (已穷举 %d 个状态)。\n", k, explored)
		}
	}

	fmt.Printf("\n=== 更新方案 (共 %d 步) ===\n", len(moves))
	fmt.Println("步骤 0: 初始状态 (全部旧路由) — 安全")
	_, evs = prob.checkState(0)
	printEvidence(evs, "  ")
	for i, mv := range moves {
		fmt.Printf("步骤 %d: 同步更新 {%s}\n", i+1, strings.Join(prob.namesOf(mv.Group), ", "))
		fmt.Printf("  已更新节点集合: {%s}\n", strings.Join(prob.namesOf(mv.State), ", "))
		_, evs := prob.checkState(mv.State)
		printEvidence(evs, "  ")
	}
	fmt.Println("完成: 全部节点已切换到新路由, 每一步均无转发环且全可达。")
	return 0
}

func printEvidence(evs []DestEvidence, indent string) {
	for _, ev := range evs {
		if ev.OK {
			fmt.Printf("%s目的 %s: %d/%d 可达, 无环\n", indent, ev.Dest, ev.Reached, ev.Total)
			for _, path := range ev.Paths {
				fmt.Printf("%s  %s\n", indent, path)
			}
		} else {
			fmt.Printf("%s目的 %s: 不安全 (%d/%d 可达)\n", indent, ev.Dest, ev.Reached, ev.Total)
			for _, f := range ev.Failures {
				fmt.Printf("%s  x %s\n", indent, f)
			}
		}
	}
}

func deriveNodes(in inputJSON) []string {
	set := make(map[string]bool)
	for _, t := range []map[string]map[string]string{in.Old, in.New} {
		for u, row := range t {
			set[u] = true
			for d, nh := range row {
				set[d] = true
				set[nh] = true
			}
		}
	}
	var nodes []string
	for n := range set {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	return nodes
}

func deriveDests(in inputJSON) []string {
	set := make(map[string]bool)
	for _, t := range []map[string]map[string]string{in.Old, in.New} {
		for _, row := range t {
			for d := range row {
				set[d] = true
			}
		}
	}
	var dests []string
	for d := range set {
		dests = append(dests, d)
	}
	sort.Strings(dests)
	return dests
}
