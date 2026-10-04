// Demo: bitemporal correction of an employee salary record.
package main

import (
	"fmt"
	"time"

	"bitemporal"
)

func d(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func iv(from, to string) bitemporal.Interval {
	end := bitemporal.Infinity
	if to != "" {
		end = d(to)
	}
	return bitemporal.Interval{From: d(from), To: end}
}

func printHistory(title string, s *bitemporal.Store, key string) {
	fmt.Println(title)
	for _, v := range s.History(key) {
		fmt.Println("  ", v)
	}
}

func main() {
	s := bitemporal.NewStore()

	// 2024-01-01 系统获知：Alice 的薪资自 2024-01-01 起为 100。
	if _, err := s.Put("alice", "100", iv("2024-01-01", ""), d("2024-01-01")); err != nil {
		panic(err)
	}
	printHistory("== 更正前历史 ==", s, "alice")

	// 2024-06-01 发现：其实 2024-03-01 起已调整为 120。
	// 更正 = 关闭旧系统区间 + 追加两个相邻业务区间（拆分）。
	segs := []bitemporal.Segment{
		{Valid: iv("2024-01-01", "2024-03-01"), Value: "100"},
		{Valid: iv("2024-03-01", ""), Value: "120"},
	}
	if err := bitemporal.CheckTiling(segs, d("2024-01-01"), bitemporal.Infinity); err != nil {
		panic(err)
	}
	fmt.Println("边界相接校验通过: [01-01,03-01) + [03-01,∞) 完整覆盖原区间")
	tx, err := s.Correct("alice", d("2024-06-01"), segs, "salary adjustment")
	if err != nil {
		panic(err)
	}
	fmt.Printf("tx#%d 关闭 %d 个旧版本, 追加 %d 个新版本\n", tx.ID, len(tx.Closed), len(tx.Added))
	printHistory("== 更正后历史 ==", s, "alice")

	// 双时间查询。
	q := func(valid, sys string) {
		v, _ := s.AsOf("alice", d(valid), d(sys))
		fmt.Printf("  业务时间 %s @ 系统时间 %s => %s\n", valid, sys, v)
	}
	fmt.Println("== 双时间查询 ==")
	q("2024-04-15", "2024-05-01") // 旧系统切面：当时以为还是 100
	q("2024-04-15", "2024-06-01") // 新系统切面：已知 3 月起是 120
	q("2024-02-15", "2024-06-01") // 拆分出的前半段

	// 重叠禁止演示。
	if _, err := s.Put("alice", "999", iv("2024-02-01", "2024-04-01"), d("2024-06-02")); err != nil {
		fmt.Println("== 重叠被拒绝 ==", err)
	}

	// 撤销更正。
	undo, err := s.Undo(tx.ID, d("2024-07-01"))
	if err != nil {
		panic(err)
	}
	fmt.Printf("== 撤销更正 (tx#%d) ==\n", undo.ID)
	q("2024-04-15", "2024-07-01") // 撤销后回到 100
	printHistory("== 撤销后完整历史（只增不改）==", s, "alice")
}
