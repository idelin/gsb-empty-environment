// 停车计费工具: 处理缺失一次进出事件的记录, 在最多补 1 个事件的条件下
// 恢复合法停车段, 输出最低/最高可能费用及对应补事件时间区间。
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("parkingfee", flag.ContinueOnError)
	fs.SetOutput(stderr)
	free := fs.Duration("free", 0, "每段免费时长, 如 15m")
	unit := fs.Duration("unit", time.Hour, "计费单位(每段不足一单位向上进位), 如 30m")
	rate := fs.String("rate", "", "每计费单位价格, 单位元, 如 2.5 (必填)")
	dailyCap := fs.String("daily-cap", "0", "每个自然日封顶, 单位元, 0 表示不封顶")
	sinceStr := fs.String("since", "", "补事件时间下界(RFC3339), 可选")
	asOfStr := fs.String("as-of", "", "补事件时间上界(RFC3339), 可选")
	asJSON := fs.Bool("json", false, "以 JSON 格式输出")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "用法: parkingfee [flags] <events.json | ->")
		fs.PrintDefaults()
		return 2
	}

	var data []byte
	var err error
	if fs.Arg(0) == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(fs.Arg(0))
	}
	if err != nil {
		fmt.Fprintln(stderr, "读取事件文件失败:", err)
		return 1
	}
	events, err := ParseEvents(data)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	if *rate == "" {
		fmt.Fprintln(stderr, "缺少必需参数 -rate")
		return 2
	}
	rateCents, err := parseYuan(*rate)
	if err != nil {
		fmt.Fprintln(stderr, "-rate:", err)
		return 2
	}
	capCents, err := parseYuan(*dailyCap)
	if err != nil {
		fmt.Fprintln(stderr, "-daily-cap:", err)
		return 2
	}
	rule := FeeRule{Free: *free, Unit: *unit, RateCents: rateCents, DailyCapCents: capCents}

	var since, asOf *time.Time
	if *sinceStr != "" {
		t, err := time.Parse(time.RFC3339, *sinceStr)
		if err != nil {
			fmt.Fprintln(stderr, "-since 时间格式无效:", err)
			return 2
		}
		since = &t
	}
	if *asOfStr != "" {
		t, err := time.Parse(time.RFC3339, *asOfStr)
		if err != nil {
			fmt.Fprintln(stderr, "-as-of 时间格式无效:", err)
			return 2
		}
		asOf = &t
	}

	analysis, err := Analyze(events, rule, since, asOf)
	if err != nil {
		fmt.Fprintln(stderr, "分析失败:", err)
		return 1
	}
	if *asJSON {
		if err := printJSON(stdout, events, analysis); err != nil {
			fmt.Fprintln(stderr, "输出失败:", err)
			return 1
		}
	} else {
		printText(stdout, events, analysis)
	}
	return 0
}
