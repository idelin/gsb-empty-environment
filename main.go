package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"
)

type jsonGraph struct {
	Source string     `json:"source"`
	Target string     `json:"target"`
	Budget int        `json:"budget"`
	Edges  []jsonEdge `json:"edges"`
}

type jsonEdge struct {
	ID   int    `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
	Cost int64  `json:"cost"`
	Risk int    `json:"risk"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	var data []byte
	var err error
	if len(args) > 0 {
		data, err = ioutil.ReadFile(args[0])
	} else {
		data, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		return err
	}
	var jg jsonGraph
	if err := json.Unmarshal(data, &jg); err != nil {
		return fmt.Errorf("解析 JSON 失败: %v", err)
	}
	raw := make([]RawEdge, len(jg.Edges))
	for i, e := range jg.Edges {
		raw[i] = RawEdge{ID: e.ID, From: e.From, To: e.To, Cost: e.Cost, Risk: e.Risk}
	}
	g, index, err := NewGraph(raw)
	if err != nil {
		return err
	}
	src, ok := index[jg.Source]
	if !ok {
		return fmt.Errorf("源点 %q 不在图中", jg.Source)
	}
	dst, ok := index[jg.Target]
	if !ok {
		return fmt.Errorf("终点 %q 不在图中", jg.Target)
	}
	sol, _ := solve(g, src, dst, jg.Budget)
	if !sol.Found {
		fmt.Fprintf(out, "无可行路径：%s -> %s 在风险预算 %d 内不可达\n", jg.Source, jg.Target, jg.Budget)
		return nil
	}
	printSolution(out, g, jg, sol)
	return nil
}

func printSolution(out io.Writer, g *Graph, jg jsonGraph, sol Solution) {
	fmt.Fprintf(out, "最低费用: %d\n", sol.Cost)
	fmt.Fprintf(out, "总风险:   %d (预算 %d)\n", sol.Risk, jg.Budget)
	fmt.Fprintf(out, "边数:     %d\n", len(sol.Path))

	nodes := []string{jg.Source}
	ids := make([]string, 0, len(sol.Path))
	for _, id := range sol.Path {
		e := g.ByID[id]
		nodes = append(nodes, g.Names[e.To])
		ids = append(ids, fmt.Sprintf("%d", id))
	}
	fmt.Fprintf(out, "路径:     %s\n", strings.Join(nodes, " -> "))
	fmt.Fprintf(out, "边序列:   %s\n", strings.Join(ids, " -> "))

	fmt.Fprintln(out, "\n风险账本:")
	fmt.Fprintf(out, "%-4s %-6s %-12s %8s %8s %6s %8s\n", "步骤", "边ID", "区间", "费用", "累计费用", "风险", "累计风险")
	var cumCost int64
	cumRisk := 0
	from := jg.Source
	for i, id := range sol.Path {
		e := g.ByID[id]
		cumCost += e.Cost
		cumRisk += e.Risk
		fmt.Fprintf(out, "%-4d %-6d %-12s %8d %8d %6d %8d\n",
			i+1, id, from+"->"+g.Names[e.To], e.Cost, cumCost, e.Risk, cumRisk)
		from = g.Names[e.To]
	}
}
