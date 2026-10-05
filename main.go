package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

// 价格修复求解器：从标准输入（或 -input 指定的文件）读取 JSON，
// 求解后将 JSON 结果写到标准输出。
func main() {
	inputPath := flag.String("input", "", "输入 JSON 文件路径（默认读取标准输入）")
	compact := flag.Bool("compact", false, "输出紧凑 JSON（默认缩进输出）")
	flag.Parse()

	var data []byte
	var err error
	if *inputPath != "" {
		data, err = os.ReadFile(*inputPath)
	} else {
		data, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取输入失败: %v\n", err)
		os.Exit(1)
	}
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		fmt.Fprintf(os.Stderr, "解析输入 JSON 失败: %v\n", err)
		os.Exit(1)
	}

	out := Solve(&in)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if !*compact {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "序列化输出失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(buf.String())
}
