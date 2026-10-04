// 资金分配求解器：在来源限定用途、有效期与配捐规则约束下，
// 求总项目收益最大的分配方案（次目标：过期闲置金额最少），
// 并输出每笔资金流向及其触发依据。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"fundsolver/solver"
)

func main() {
	inputPath := flag.String("input", "", "输入 JSON 文件路径（缺省读标准输入）")
	maxProjects := flag.Int("max-projects", 0, "最多资助项目数（>0 时覆盖输入值，缺省 15）")
	asJSON := flag.Bool("json", false, "以 JSON 输出求解报告")
	flag.Parse()

	var data []byte
	var err error
	if *inputPath == "" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(*inputPath)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取输入失败:", err)
		os.Exit(1)
	}
	in, err := solver.ParseInput(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "输入校验失败:", err)
		os.Exit(1)
	}
	if *maxProjects > 0 {
		in.MaxProjects = *maxProjects
	}

	rep := solver.Solve(in)
	if *asJSON {
		out, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(out))
		return
	}
	fmt.Print(solver.RenderText(rep))
}
