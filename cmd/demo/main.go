// 运行样例：四个节点在网络分区下投票并动态变更权重，愈合后合并出确定性决策。
package main

import (
	"fmt"

	wv "weightedvote"
)

func main() {
	// 创世配置：投票人 A/B/C/D，初始权重 4/3/2/1。
	base := wv.NewState("boot")
	base.SetWeight("A", 4)
	base.SetWeight("B", 3)
	base.SetWeight("C", 2)
	base.SetWeight("D", 1)
	fmt.Println("genesis weights: A=4 B=3 C=2 D=1")

	// 网络分区：{A,B} 与 {C,D} 各自分叉。
	a, b := base.Fork("node-A"), base.Fork("node-B")
	c, d := base.Fork("node-C"), base.Fork("node-D")
	fmt.Println("partition: {A,B} | {C,D}")

	// 分区期间分别投票。
	a.CastVote("A", wv.Yes)
	b.CastVote("B", wv.Yes)
	c.CastVote("C", wv.No)
	d.CastVote("D", wv.Abstain)
	fmt.Println("votes: A=Yes B=Yes | C=No D=Abstain")

	// 分区期间权重因果动态调整（含一处并发冲突：两侧同时改 C 的权重）。
	a.SetWeight("B", 5) // 侧1：B 3->5
	c.SetWeight("D", 6) // 侧2：D 1->6
	a.SetWeight("C", 4) // 侧1：C 2->4
	c.SetWeight("C", 1) // 侧2：C 2->1（并发冲突，按节点 ID 决胜，node-C 胜出）
	fmt.Println("weight changes: B->5 | D->6, C->4 vs C->1 (conflict, node-C wins)")

	// 分区内同步。
	a.Merge(b)
	c.Merge(d)

	// 网络愈合：全量合并。
	merged := wv.NewState("heal")
	merged.Merge(a)
	merged.Merge(c)

	fmt.Println("\nafter heal:")
	for _, voter := range merged.Voters() {
		vrec, _ := merged.VoteOf(voter)
		wrec, _ := merged.WeightOf(voter)
		fmt.Printf("  voter %s: vote=%-7s weight=%d (weight ts %s)\n",
			voter, vrec.Choice, wrec.Weight, wrec.Time)
	}

	tally := merged.Tally()
	fmt.Println("\ntally:", tally)
}
