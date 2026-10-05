package main

import (
	"fmt"
	"sort"
	"strings"
)

// demoScenario 构造一个同时包含正常提交、迟到提交、参与者缺失、
// 事务未决四种情形的三分区日志。
func demoScenario() ([]Partition, []Txn) {
	parts := []Partition{
		{Name: "A", Log: []Entry{
			{Seq: 1, TxnID: "t1", Kind: Data, Key: "x"},
			{Seq: 2, TxnID: "t1", Kind: Commit},
			{Seq: 3, TxnID: "t2", Kind: Data, Key: "y"},
			{Seq: 4, TxnID: "t2", Kind: Commit},
			{Seq: 5, TxnID: "t4", Kind: Data, Key: "w"},
			{Seq: 6, TxnID: "t4", Kind: Commit},
		}},
		{Name: "B", Log: []Entry{
			{Seq: 1, TxnID: "t1", Kind: Data, Key: "x"},
			{Seq: 2, TxnID: "t1", Kind: Commit},
			{Seq: 3, TxnID: "t2", Kind: Data, Key: "y"}, // t2 在 B 上一直未决
			{Seq: 4, TxnID: "t3", Kind: Data, Key: "z"},
			{Seq: 5, TxnID: "t3", Kind: Commit},
		}},
		{Name: "C", Log: []Entry{
			{Seq: 1, TxnID: "t4", Kind: Data, Key: "w"},
			{Seq: 2, TxnID: "t4", Kind: Commit},
			// t3 在 C 上毫无痕迹：参与者缺失
		}},
	}
	txns := []Txn{
		{ID: "t1", Participants: []string{"A", "B"}},
		{ID: "t2", Participants: []string{"A", "B"}},
		{ID: "t3", Participants: []string{"B", "C"}},
		{ID: "t4", Participants: []string{"A", "C"}},
	}
	return parts, txns
}

func main() {
	parts, txns := demoScenario()

	fmt.Println("== 分区日志 ==")
	for _, p := range parts {
		var items []string
		for _, e := range p.Log {
			items = append(items, fmt.Sprintf("#%d %s/%s", e.Seq, e.TxnID, e.Kind))
		}
		fmt.Printf("  %s: %s\n", p.Name, strings.Join(items, "  "))
	}

	cut := RecoverableCut(parts, txns)

	fmt.Println("\n== 可恢复截点 ==")
	names := make([]string, 0, len(cut))
	for n := range cut {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Printf("  %s -> seq <= %d\n", n, cut[n])
	}

	included, evidences := Analyze(parts, txns, cut)
	fmt.Println("\n== 截点内完整包含的事务 ==")
	if len(included) == 0 {
		fmt.Println("  (无)")
	}
	for _, id := range included {
		fmt.Printf("  %s\n", id)
	}

	fmt.Println("\n== 被排除事务与缺失参与者证据 ==")
	for _, ev := range evidences {
		fmt.Printf("  %s:\n", ev.TxnID)
		if len(ev.Missing) > 0 {
			fmt.Printf("    参与者无痕迹:   %s\n", strings.Join(ev.Missing, ", "))
		}
		if len(ev.Undecided) > 0 {
			fmt.Printf("    有数据但未决:   %s\n", strings.Join(ev.Undecided, ", "))
		}
		if len(ev.LateCommits) > 0 {
			fmt.Printf("    迟到提交标记:   %s\n", strings.Join(ev.LateCommits, ", "))
		}
		if len(ev.Aborted) > 0 {
			fmt.Printf("    已中止:         %s\n", strings.Join(ev.Aborted, ", "))
		}
	}

	fmt.Printf("\n截点有效性校验: %v\n", ValidCut(parts, txns, cut))
}
