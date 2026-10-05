package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	maxStates := flag.Int("max-states", 2000000, "状态空间上限，超过则拒绝分析")
	flag.Parse()

	var (
		data []byte
		err  error
	)
	if flag.NArg() > 0 {
		data, err = os.ReadFile(flag.Arg(0))
	} else {
		data, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取输入失败:", err)
		os.Exit(2)
	}
	var spec Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		fmt.Fprintln(os.Stderr, "解析 JSON 失败:", err)
		os.Exit(2)
	}
	model, err := Compile(&spec, *maxStates)
	if err != nil {
		fmt.Fprintln(os.Stderr, "规则无效:", err)
		os.Exit(2)
	}
	report := Analyze(model)
	fmt.Print(report.String())
	if !report.Terminating || !report.Unique {
		os.Exit(1)
	}
}
