package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
)

func main() {
	file := flag.String("f", "", "输入 JSON 文件（缺省读标准输入）")
	asJSON := flag.Bool("json", false, "以 JSON 输出计划")
	flag.Parse()

	var data []byte
	var err error
	if *file != "" {
		data, err = ioutil.ReadFile(*file)
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
	plan, err := Solve(&in)
	if err != nil {
		fatal("规划失败: %v", err)
	}
	if *asJSON {
		out, _ := json.MarshalIndent(plan, "", "  ")
		fmt.Println(string(out))
		return
	}
	fmt.Print(renderReport(plan))
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func renderReport(p *Plan) string {
	var b strings.Builder
	if p.Feasible {
		fmt.Fprintf(&b, "取证计划：可行（最低成本 %d，共 %d 项证据）\n", p.TotalCost, len(p.Selected))
		ids := make([]string, 0, len(p.Selected))
		for _, ev := range p.Selected {
			ids = append(ids, fmt.Sprintf("%s(成本%d)", ev.ID, ev.Cost))
		}
		fmt.Fprintf(&b, "采集清单: %s\n", strings.Join(ids, ", "))
	} else {
		fmt.Fprintf(&b, "取证计划：不可行\n%s\n", p.Note)
	}
	if len(p.Expired) > 0 {
		ids := make([]string, 0, len(p.Expired))
		for _, ev := range p.Expired {
			ids = append(ids, fmt.Sprintf("%s(有效期至%s)", ev.ID, ev.ValidUntil))
		}
		fmt.Fprintf(&b, "已过期不计入: %s\n", strings.Join(ids, ", "))
	}
	b.WriteString("\n验收条目：\n")
	for _, r := range p.Results {
		if r.Satisfied {
			fmt.Fprintf(&b, "  [满足]   %s: %s\n           路径: %s\n", r.ID, r.Expr, r.Path)
		} else {
			fmt.Fprintf(&b, "  [未满足] %s: %s\n           原因: %s\n", r.ID, r.Expr, r.Reason)
		}
	}
	return b.String()
}
