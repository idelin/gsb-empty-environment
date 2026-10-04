// policydiff 是权限差异检查器的命令行入口。
//
// 用法：
//
//	policydiff -schema schema.json -old old.json -new new.json
//
// 判断新规则是否相对旧规则扩大授权；若扩大，输出最小的
// 主体/资源属性组合（反例）以及新旧两条规则的解释树。
package main

import (
	"flag"
	"fmt"
	"os"

	"policydiff/policydiff"
)

func main() {
	schemaPath := flag.String("schema", "", "属性域模式 JSON 文件")
	oldPath := flag.String("old", "", "旧规则 JSON 文件")
	newPath := flag.String("new", "", "新规则 JSON 文件")
	flag.Parse()
	if *schemaPath == "" || *oldPath == "" || *newPath == "" {
		fmt.Fprintln(os.Stderr, "用法: policydiff -schema schema.json -old old.json -new new.json")
		os.Exit(2)
	}

	schema, err := load(*schemaPath, policydiff.ParseSchema)
	check(err)
	oldRule, err := load(*oldPath, policydiff.ParseRule)
	check(err)
	newRule, err := load(*newPath, policydiff.ParseRule)
	check(err)

	res, err := policydiff.CheckExpansion(schema, oldRule, newRule)
	check(err)

	if !res.Expands {
		fmt.Println("结论：新规则未扩大授权（新规则放行的所有请求均被旧规则覆盖）")
		return
	}
	fmt.Println("结论：新规则扩大了授权")
	fmt.Println("最小反例（新规则放行、旧规则拒绝的最小属性组合；未列出的属性为缺失）：")
	for _, p := range policydiff.SortedPairs(res.Counterexample) {
		fmt.Println("  " + p)
	}
	fmt.Println()
	fmt.Println("新规则解释树：")
	fmt.Print(indent(res.NewTree.String()))
	fmt.Println()
	fmt.Println("旧规则解释树：")
	fmt.Print(indent(res.OldTree.String()))
}

func load[T any](path string, parse func([]byte) (T, error)) (T, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		var zero T
		return zero, err
	}
	return parse(data)
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func indent(s string) string {
	out := ""
	for _, line := range splitLines(s) {
		if line != "" {
			out += "  " + line + "\n"
		}
	}
	return out
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
