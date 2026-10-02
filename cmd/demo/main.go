// 运行样例：社交网络紧密圈子发现 + 动态增删边时极大团的诞生与消解。
package main

import (
	"fmt"
	"strings"

	"clique"
)

func format(cs [][]uint64) string {
	if len(cs) == 0 {
		return "(无)"
	}
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = fmt.Sprintf("%v", c)
	}
	return strings.Join(parts, "  ")
}

func main() {
	g := clique.New()

	// 社交圈：1..5 几乎全员互为好友（仅缺 1-5 这一条边），
	// 6 是 1、2 的共同好友，7 只认识 6。
	friendships := [][2]uint64{
		{1, 2}, {1, 3}, {1, 4}, {2, 3}, {2, 4}, {2, 5}, {3, 4}, {3, 5}, {4, 5},
		{1, 6}, {2, 6}, {6, 7},
	}
	for _, e := range friendships {
		g.AddEdge(e[0], e[1])
	}
	fmt.Println("== 初始社交网络的极大团（紧密圈子） ==")
	fmt.Println("  ", format(g.MaximalCliques()))

	fmt.Println("\n== 包含节点集合 {1,2} 的极大团清单 ==")
	fmt.Println("  ", format(g.CliquesContaining(1, 2)))

	fmt.Println("\n>>> 1 和 5 成为好友：加入边 (1,5)，促成 5 节点完全子图")
	born, died := g.AddEdge(1, 5)
	fmt.Println("   诞生的极大团:", format(born))
	fmt.Println("   消解的极大团:", format(died))
	fmt.Println("   当前极大团:  ", format(g.MaximalCliques()))

	fmt.Println("\n>>> 2 和 4 断交：删除边 (2,4)，5 团退化")
	born, died = g.RemoveEdge(2, 4)
	fmt.Println("   诞生的极大团:", format(born))
	fmt.Println("   消解的极大团:", format(died))
	fmt.Println("   当前极大团:  ", format(g.MaximalCliques()))
}
