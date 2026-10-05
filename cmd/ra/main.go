// ra 是有限关系代数解释器 + 去相关改写器的 JSON 计划 CLI。
//
// 用法:
//
//	ra plan.json     从文件读取
//	ra < plan.json   从标准输入读取
//
// 输入格式: {"tables": {"R": [{"k": 1}, ...], ...}, "plan": {...}}
// 输出: 原计划、改写计划、适用条件（或拒绝原因）以及两个计划的执行结果。
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"ra"
)

func main() {
	var data []byte
	var err error
	if len(os.Args) > 1 {
		data, err = os.ReadFile(os.Args[1])
	} else {
		data, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		fatal("读取输入失败: %v", err)
	}
	in, err := ra.ParseInput(data)
	if err != nil {
		fatal("%v", err)
	}

	out := map[string]any{
		"original_plan": in.Plan.ToJSON(),
	}

	origRows, err := in.Plan.Exec(nil)
	if err != nil {
		fatal("原计划执行失败: %v", err)
	}
	out["original_result"] = rowsJSON(origRows)

	rewritten, conds, err := ra.Rewrite(in.Plan)
	out["conditions"] = conds
	if err != nil {
		out["rewritten"] = "rejected"
		out["reject_reason"] = err.Error()
	} else {
		out["rewritten_plan"] = rewritten.ToJSON()
		rwRows, err := rewritten.Exec(nil)
		if err != nil {
			fatal("改写计划执行失败: %v", err)
		}
		out["rewritten_result"] = rowsJSON(rwRows)
		out["results_equal"] = ra.MultisetEqual(origRows, rwRows)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fatal("输出失败: %v", err)
	}
}

func rowsJSON(rows []ra.Row) []map[string]any {
	out := make([]map[string]any, len(rows))
	for i, r := range rows {
		out[i] = map[string]any(r)
	}
	return out
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ra: "+format+"\n", args...)
	os.Exit(1)
}
