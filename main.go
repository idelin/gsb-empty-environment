package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
)

func main() {
	jsonOut := flag.Bool("json", false, "以 JSON 输出方案")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: cyclecut [-json] [输入文件]\n")
		fmt.Fprintf(os.Stderr, "从文件或标准输入读取依赖图(JSON)，输出最便宜的循环拆解方案。\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	var data []byte
	var err error
	if flag.NArg() > 0 && flag.Arg(0) != "-" {
		data, err = ioutil.ReadFile(flag.Arg(0))
	} else {
		data, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		fatal("读取输入失败: %v", err)
	}

	var spec GraphSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		fatal("解析输入 JSON 失败: %v", err)
	}

	plan, err := Solve(spec)
	if err != nil {
		fatal("无解: %v", err)
	}

	if *jsonOut {
		out, _ := json.MarshalIndent(plan, "", "  ")
		fmt.Println(string(out))
		return
	}
	printPlan(plan)
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "cyclecut: "+format+"\n", args...)
	os.Exit(1)
}

func costStr(c float64) string {
	return strconv.FormatFloat(c, 'f', -1, 64)
}

func printPlan(p *Plan) {
	fmt.Printf("总成本: %s\n", costStr(p.TotalCost))
	if len(p.Cuts) == 0 {
		fmt.Println("无需切断任何边，原图已无环。")
	} else {
		fmt.Printf("切断 %d 条依赖边:\n", len(p.Cuts))
		for _, c := range p.Cuts {
			fmt.Printf("  %s -> %s  [引导产物: %s, 成本: %s]\n", c.From, c.To, c.Artifact, costStr(c.Cost))
		}
	}
	fmt.Println("拓扑构建计划:")
	for i, stage := range p.Stages {
		fmt.Printf("  阶段 %d: ", i+1)
		for j, n := range stage {
			if j > 0 {
				fmt.Print(", ")
			}
			fmt.Print(n)
		}
		fmt.Println()
	}
}
