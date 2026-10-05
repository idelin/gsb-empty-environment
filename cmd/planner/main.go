// 迁移脚本规划器 CLI。
//
// 用法:
//   planner [plan.json]     省略文件时从标准输入读取
//
// 输入 JSON 格式:
//   {
//     "preexisting": ["table:users"],
//     "ops": [ {"id": "...", "kind": "...", "reads": [...], "writes": [...],
//               "requires": [...], "provides": [...],
//               "needs_write_stop": false, "rollback": "..."} ]
//   }
package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"migrateplanner"
)

type input struct {
	Preexisting []string     `json:"preexisting"`
	Ops         []planner.Op `json:"ops"`
}

func main() {
	var data []byte
	var err error
	if len(os.Args) > 1 {
		data, err = ioutil.ReadFile(os.Args[1])
	} else {
		data, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		fatal("读取输入失败: %v", err)
	}
	var in input
	if err := json.Unmarshal(data, &in); err != nil {
		fatal("解析 JSON 失败: %v", err)
	}
	plan, err := planner.Build(in.Ops, in.Preexisting)
	if err != nil {
		fatal("规划失败: %v", err)
	}

	for _, st := range plan.Steps {
		var marks []string
		if st.NeedsWriteStop {
			marks = append(marks, "停写窗口")
		}
		if st.Irreversible {
			marks = append(marks, "不可逆边界")
		}
		mark := ""
		if len(marks) > 0 {
			mark = "  [" + strings.Join(marks, ", ") + "]"
		}
		fmt.Printf("步骤 %d%s\n", st.Index, mark)
		for _, op := range st.Ops {
			fmt.Printf("  - %s (%s)\n", op.ID, op.Kind)
		}
	}
	fmt.Printf("\n共 %d 步，停写窗口 %d 个", len(plan.Steps), plan.WriteStopWindows)
	if len(plan.IrreversibleBoundaries) > 0 {
		fmt.Printf("，不可逆边界位于步骤 %v", plan.IrreversibleBoundaries)
	}
	fmt.Println()
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
