package main

import (
	"fmt"
	"time"

	"bitemporal"
)

func d(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}

func printCurrent(s *bitemporal.Store, key string, txAt time.Time, label string) {
	fmt.Printf("== %s (系统切面 %s) ==\n", label, txAt.Format("2006-01-02"))
	for _, v := range s.Current(key, txAt) {
		fmt.Printf("  valid=[%s, %s)  value=%s\n",
			v.ValidFrom.Format("2006-01-02"), fmtTo(v.ValidTo), v.Value)
	}
	if err := s.CheckContiguous(key, txAt); err != nil {
		fmt.Printf("  边界校验: 失败 %v\n", err)
	} else {
		fmt.Println("  边界校验: 首尾相接 ✓")
	}
	if err := s.ValidateNoOverlap(key, txAt); err != nil {
		fmt.Printf("  重叠校验: 失败 %v\n", err)
	} else {
		fmt.Println("  重叠校验: 无重叠 ✓")
	}
	fmt.Println()
}

func fmtTo(t time.Time) string {
	if t.Equal(bitemporal.Inf) {
		return "∞"
	}
	return t.Format("2006-01-02")
}

func main() {
	s := bitemporal.NewStore()
	t1 := d("2024-06-01") // 系统获知：2024 全年薪资 100
	t2 := d("2024-09-01") // 更正：2~3 月应为 120
	t3 := d("2024-11-01") // 撤销更正

	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}

	_, err := s.Put("salary", "100", d("2024-01-01"), d("2025-01-01"), t1)
	must(err)
	printCurrent(s, "salary", t1, "初始写入后")

	opID, err := s.Put("salary", "120", d("2024-02-01"), d("2024-04-01"), t2)
	must(err)
	printCurrent(s, "salary", t2, "更正 2~3 月为 120 后（业务区间被拆分）")

	fmt.Println("== 双时间查询 ==")
	q := func(validAt, txAt time.Time) {
		v, ok := s.AsOf("salary", validAt, txAt)
		fmt.Printf("  业务 %s @ 系统 %s => %s (found=%v)\n",
			validAt.Format("2006-01-02"), txAt.Format("2006-01-02"), v, ok)
	}
	q(d("2024-02-15"), t1) // 更正前系统视角：100
	q(d("2024-02-15"), t2) // 更正后系统视角：120
	fmt.Println()

	must(s.Undo(opID, t3))
	printCurrent(s, "salary", t3, "撤销更正后")

	fmt.Println("== 完整历史（含已关闭系统区间） ==")
	for _, v := range s.History("salary") {
		fmt.Printf("  %s\n", v)
	}
}
