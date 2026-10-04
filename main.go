package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

func main() {
	f := flag.String("f", "", "包含 tables 与 plan 的 JSON 文件")
	flag.Parse()
	if *f == "" {
		fmt.Fprintln(os.Stderr, "用法: ra -f input.json")
		os.Exit(2)
	}
	if err := runCLI(*f); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func runCLI(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	env, plan, err := ParseInput(data)
	if err != nil {
		return err
	}

	fmt.Println("== 原计划 ==")
	printJSON(planJSON(plan))

	rewritten, notes, rerr := Rewrite(plan, env)
	if rerr != nil {
		fmt.Println("== 改写结果 ==")
		fmt.Println("拒绝改写:", rerr)
	} else {
		fmt.Println("== 改写计划 ==")
		printJSON(planJSON(rewritten))
		fmt.Println("== 适用条件 ==")
		for _, n := range notes {
			fmt.Println("-", n)
		}
	}

	schema, rows, err := evalPlan(plan, env, nil)
	if err != nil {
		return fmt.Errorf("执行原计划: %w", err)
	}
	printResult("原计划结果", schema, rows)

	if rerr == nil {
		schema2, rows2, err := evalPlan(rewritten, env, nil)
		if err != nil {
			return fmt.Errorf("执行改写计划: %w", err)
		}
		printResult("改写计划结果", schema2, rows2)
		fmt.Println("== 行多重集一致:", bagOf(rows) == bagOf(rows2), "==")
	}
	return nil
}

func printJSON(v interface{}) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Println("<marshal error:", err, ">")
		return
	}
	fmt.Println(string(b))
}

func printResult(title string, schema []string, rows []Row) {
	fmt.Printf("== %s（%d 行）==\n", title, len(rows))
	printJSON(map[string]interface{}{"schema": schema, "rows": rows})
}

func bagOf(rows []Row) string {
	ks := make([]string, len(rows))
	for i, r := range rows {
		parts := make([]string, len(r))
		for j, v := range r {
			parts[j] = v.key()
		}
		ks[i] = strings.Join(parts, ",")
	}
	sort.Strings(ks)
	return strings.Join(ks, "\n")
}
