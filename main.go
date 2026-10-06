package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
)

func main() {
	specPath := flag.String("spec", "spec.json", "输入规格 JSON 路径")
	outPath := flag.String("o", "", "输出报告路径（默认标准输出）")
	flag.Parse()

	data, err := ioutil.ReadFile(*specPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取规格失败:", err)
		os.Exit(2)
	}
	var spec Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		fmt.Fprintln(os.Stderr, "解析规格失败:", err)
		os.Exit(2)
	}
	plan, err := planCompression(&spec)
	if err != nil {
		fmt.Fprintln(os.Stderr, "压缩规划失败:", err)
		os.Exit(2)
	}
	report := buildReport(&spec, plan)
	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "生成报告失败:", err)
		os.Exit(2)
	}
	if *outPath == "" {
		os.Stdout.Write(out)
		fmt.Println()
	} else if err := ioutil.WriteFile(*outPath, out, 0644); err != nil {
		fmt.Fprintln(os.Stderr, "写入报告失败:", err)
		os.Exit(2)
	}
	if !report.Verification.OK {
		fmt.Fprintln(os.Stderr, "核对未通过")
		os.Exit(1)
	}
}
