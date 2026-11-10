package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"example.org/offlinecli/internal/cors"
)

func main() {
	var (
		inputFile    = flag.String("input", "", "JSON 输入文件；留空运行内置演示场景")
		jsonOutput   = flag.Bool("json", false, "以 JSON 输出结构化复盘结果")
		writeExample = flag.Bool("write-example", false, "将内置演示场景的 JSON 输入打印到标准输出后退出")
	)
	flag.Parse()

	if *writeExample {
		if err := writeJSON(os.Stdout, cors.DemoInput()); err != nil {
			fatal(err)
		}
		return
	}

	in := cors.DemoInput()
	if *inputFile != "" {
		raw, err := os.ReadFile(*inputFile)
		if err != nil {
			fatal(err)
		}
		in = &cors.Input{}
		if err := json.Unmarshal(raw, in); err != nil {
			fatal(fmt.Errorf("解析输入: %w", err))
		}
	}

	result, err := cors.Run(in)
	if err != nil {
		fatal(err)
	}

	if *jsonOutput {
		if err := writeJSON(os.Stdout, result); err != nil {
			fatal(err)
		}
		return
	}
	cors.RenderText(os.Stdout, result)
}

func writeJSON(file *os.File, value any) error {
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "cors-replay: %v\n", err)
	os.Exit(1)
}
