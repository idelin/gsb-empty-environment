// permdiff 是权限差异检查器的命令行入口。
//
// 用法：
//   permdiff -old old.json -new new.json [-domain 0:100]
//   permdiff -eval rule.json -attrs attrs.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"

	"permdiff"
)

func main() {
	oldPath := flag.String("old", "", "旧规则 JSON 文件")
	newPath := flag.String("new", "", "新规则 JSON 文件")
	evalPath := flag.String("eval", "", "只求值：规则 JSON 文件")
	attrsPath := flag.String("attrs", "", "只求值：属性赋值 JSON 文件")
	domain := flag.String("domain", "0:100", "整数属性的有限域，格式 min:max")
	flag.Parse()

	lo, hi := 0, 100
	if parts := strings.Split(*domain, ":"); len(parts) == 2 {
		if v, err := strconv.Atoi(parts[0]); err == nil {
			lo = v
		}
		if v, err := strconv.Atoi(parts[1]); err == nil {
			hi = v
		}
	}

	switch {
	case *evalPath != "" && *attrsPath != "":
		rule := mustParseFile(*evalPath)
		var a permdiff.Assignment
		must(json.Unmarshal(mustRead(*attrsPath), &a))
		allowed, tree := permdiff.Decide(rule, a)
		fmt.Printf("决定: %v（unknown 视为拒绝）\n%s", allowed, tree.String())
	case *oldPath != "" && *newPath != "":
		res := permdiff.Diff(mustParseFile(*oldPath), mustParseFile(*newPath),
			permdiff.Config{IntMin: lo, IntMax: hi})
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
		if res.Expands {
			fmt.Printf("\n新规则解释树:\n%s\n旧规则解释树:\n%s", res.NewTree.String(), res.OldTree.String())
		}
	default:
		flag.Usage()
		os.Exit(2)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func mustRead(path string) []byte {
	data, err := ioutil.ReadFile(path)
	must(err)
	return data
}

func mustParseFile(path string) permdiff.Cond {
	c, err := permdiff.ParseCond(mustRead(path))
	must(err)
	return c
}
