package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
)

func main() {
	pretty := flag.Bool("pretty", false, "以缩进格式输出 JSON")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: %s [-pretty] [input.json]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "不带参数时从标准输入读取 JSON。\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	var data []byte
	var err error
	if flag.NArg() > 0 {
		data, err = ioutil.ReadFile(flag.Arg(0))
	} else {
		data, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		fatal("读取输入失败: %v", err)
	}

	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		fatal("解析输入 JSON 失败: %v", err)
	}

	out, err := Solve(&in)
	if err != nil {
		fatal("求解失败: %v", err)
	}

	var buf []byte
	if *pretty {
		buf, err = json.MarshalIndent(out, "", "  ")
	} else {
		buf, err = json.Marshal(out)
	}
	if err != nil {
		fatal("序列化结果失败: %v", err)
	}
	fmt.Println(string(buf))
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "错误: "+format+"\n", args...)
	os.Exit(1)
}
