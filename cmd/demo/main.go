// demo 演示增量 SCC 维护器的验收场景：
// 三角形环合并 -> 挂载独立节点 -> 删边后精准分裂。
package main

import (
	"fmt"
	"strings"

	"sccdemo"
)

func show(tr *scc.Tracker) {
	parts := make([]string, 0)
	for _, c := range tr.Components() {
		parts = append(parts, fmt.Sprintf("{%s}(id=%s)", strings.Join(c.Nodes, ","), c.ID))
	}
	fmt.Println("  当前分量:", strings.Join(parts, "  "))
}

func report(d scc.Delta) {
	if d.Empty() {
		fmt.Println("  差量: 无变化")
		return
	}
	for _, m := range d.Merges {
		fmt.Printf("  差量: 合并 %v -> {%s}(id=%s)\n", m.OldIDs, strings.Join(m.New.Nodes, ","), m.New.ID)
	}
	for _, s := range d.Splits {
		parts := make([]string, 0)
		for _, p := range s.Parts {
			parts = append(parts, fmt.Sprintf("{%s}(id=%s)", strings.Join(p.Nodes, ","), p.ID))
		}
		fmt.Printf("  差量: 分量 %s 分裂 -> %s\n", s.OldID, strings.Join(parts, "  "))
	}
}

func main() {
	tr := scc.NewTracker()

	fmt.Println("== 1. 构建三角形环 A->B->C->A ==")
	report(tr.AddEdge("A", "B"))
	report(tr.AddEdge("B", "C"))
	report(tr.AddEdge("C", "A"))
	show(tr)

	fmt.Println("== 2. 加入独立节点 D 及边 C->D ==")
	tr.AddVertex("D")
	report(tr.AddEdge("C", "D"))
	show(tr)

	fmt.Println("== 3. 删除边 B->C ==")
	report(tr.RemoveEdge("B", "C"))
	show(tr)
}
