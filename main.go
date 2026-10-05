package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
)

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(2)
}

func main() {
	var (
		format  string
		maxRows int
	)
	flag.StringVar(&format, "format", "text", "输出格式: text | json")
	flag.IntVar(&maxRows, "max-rows", 25, "允许的最大行数（问题设计上限为 25）")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: tablefixer [flags] <spec.json>\n  省略 spec.json 时从标准输入读取。\n\n")
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
		fatal(err)
	}

	spec, err := ParseSpec(data)
	if err != nil {
		fatal(err)
	}
	model, err := BuildModel(spec, maxRows)
	if err != nil {
		fatal(err)
	}

	res := Solve(model)
	reasons := ComputeReasons(model, res.State)
	violations := Verify(model, res.State)

	switch format {
	case "json":
		printJSONReport(model, res, reasons, violations)
	case "text":
		printTextReport(model, res, reasons, violations)
	default:
		fatal(fmt.Errorf("未知输出格式 %q（可选 text|json）", format))
	}

	if len(violations) > 0 {
		os.Exit(1)
	}
}
