package main

import (
	"fmt"
	"os"

	"relalg"
)

func main() {
	sql := "SELECT u.name FROM users u JOIN orders o ON u.id = o.uid WHERE u.age > 18"
	if len(os.Args) > 1 {
		sql = os.Args[1]
	}
	stmt, err := relalg.ParseSelect(sql)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse error:", err)
		os.Exit(1)
	}
	plan, err := relalg.BuildPlan(stmt)
	if err != nil {
		fmt.Fprintln(os.Stderr, "plan error:", err)
		os.Exit(1)
	}
	fmt.Println("SQL:")
	fmt.Println(" ", sql)
	fmt.Println()
	fmt.Println("逻辑计划（优化前）:")
	fmt.Println(relalg.Format(plan))
	fmt.Println()
	fmt.Println("逻辑计划（优化后：谓词下推 + 投影消除）:")
	fmt.Println(relalg.Format(relalg.Optimize(plan)))
}
