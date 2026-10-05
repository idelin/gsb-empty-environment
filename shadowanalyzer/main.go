package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
)

func main() {
	input := flag.String("i", "", "输入 JSON 文件（缺省读标准输入）")
	asJSON := flag.Bool("json", false, "以 JSON 输出报告")
	flag.Parse()

	var data []byte
	var err error
	if *input == "" {
		data, err = ioutil.ReadAll(os.Stdin)
	} else {
		data, err = ioutil.ReadFile(*input)
	}
	if err != nil {
		fatal("读取输入失败: %v", err)
	}

	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		fatal("解析输入失败: %v", err)
	}

	rep, err := Analyze(&in)
	if err != nil {
		fatal("分析失败: %v", err)
	}

	if *asJSON {
		out, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			fatal("序列化报告失败: %v", err)
		}
		fmt.Println(string(out))
	} else {
		fmt.Print(rep.FormatText())
	}

	for _, rr := range rep.Rules {
		if rr.Status == StatusFull {
			os.Exit(1)
		}
	}
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
