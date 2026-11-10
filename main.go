package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	inputPath := flag.String("input", "", "票据事件输入 JSON 文件路径（- 表示标准输入）")
	policy := flag.String("policy", "both", "委派策略：both / constrained / resource")
	flag.Parse()

	if *inputPath == "" {
		fmt.Fprintln(os.Stderr, "用法: kerb-replay -input <events.json> [-policy both|constrained|resource]")
		os.Exit(2)
	}

	data, err := readInput(*inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取输入失败: %v\n", err)
		os.Exit(1)
	}
	in, err := ParseInput(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	result, err := Run(in, *policy)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "序列化结果失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}

func readInput(path string) ([]byte, error) {
	if path == "-" {
		stat, err := os.Stdin.Stat()
		if err != nil {
			return nil, err
		}
		if (stat.Mode() & os.ModeCharDevice) != 0 {
			return nil, fmt.Errorf("标准输入为空")
		}
		return os.ReadFile("/dev/stdin")
	}
	return os.ReadFile(path)
}
