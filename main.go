package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	jsonOut := flag.Bool("json", false, "以 JSON 格式输出结果")
	maxP := flag.Int("max-projects", 0, "覆盖输入中的 max_projects（最多资助项目数，默认 15）")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: fundsolver [选项] [input.json]\n")
		fmt.Fprintf(os.Stderr, "不指定文件时从标准输入读取。\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	var inFile *os.File
	var err error
	if flag.NArg() > 0 {
		inFile, err = os.Open(flag.Arg(0))
		if err != nil {
			fatal("打开输入文件失败: %v", err)
		}
		defer inFile.Close()
	} else {
		inFile = os.Stdin
	}

	in, err := loadInput(inFile)
	if err != nil {
		fatal("%v", err)
	}
	if *maxP > 0 {
		in.MaxProjects = *maxP
	}

	res, err := Solve(in)
	if err != nil {
		fatal("求解失败: %v", err)
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			fatal("输出 JSON 失败: %v", err)
		}
	} else {
		printReport(os.Stdout, res)
	}
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "错误: "+format+"\n", args...)
	os.Exit(1)
}
