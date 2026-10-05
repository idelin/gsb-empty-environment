package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
)

// Report 是修复器的完整输出：删除原因闭包 + 保留表核验。
type Report struct {
	TotalDeletedWeight int64               `json:"total_deleted_weight"`
	Deleted            []DeletionReason    `json:"deleted"`
	Kept               map[string][]string `json:"kept"`
	Verification       Verification        `json:"verification"`
}

func main() {
	inputPath := flag.String("input", "", "输入 JSON 文件路径（缺省读标准输入）")
	flag.Parse()

	var data []byte
	var err error
	if *inputPath == "" {
		data, err = ioutil.ReadAll(os.Stdin)
	} else {
		data, err = ioutil.ReadFile(*inputPath)
	}
	if err != nil {
		fatal("读取输入失败: %v", err)
	}

	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		fatal("解析输入 JSON 失败: %v", err)
	}

	m, err := BuildModel(&in)
	if err != nil {
		fatal("建模失败: %v", err)
	}

	sol, err := Solve(m)
	if err != nil {
		fatal("求解失败: %v", err)
	}

	report := Report{
		Deleted:      ExplainDeletions(m, sol),
		Kept:         map[string][]string{},
		Verification: VerifyKept(m, sol),
	}
	for i, ref := range m.rowRef {
		if sol.keptMask&(1<<uint(i)) != 0 {
			report.Kept[ref.Table] = append(report.Kept[ref.Table], ref.Row)
		} else {
			report.TotalDeletedWeight += m.weight[i]
		}
	}

	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fatal("序列化结果失败: %v", err)
	}
	fmt.Println(string(out))

	if !report.Verification.OK {
		os.Exit(2)
	}
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "tablefix: "+format+"\n", args...)
	os.Exit(1)
}
