package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
)

const maxApplicants = 10

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	var data []byte
	var err error
	if len(args) > 0 {
		data, err = ioutil.ReadFile(args[0])
	} else {
		data, err = ioutil.ReadAll(stdin)
	}
	if err != nil {
		return err
	}
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		return fmt.Errorf("解析输入失败: %v", err)
	}
	p, err := BuildProblem(in)
	if err != nil {
		return err
	}
	if len(p.Applicants) > maxApplicants {
		return fmt.Errorf("申请者 %d 名, 超过上限 %d", len(p.Applicants), maxApplicants)
	}
	res := Solve(p)
	PrintReport(stdout, p, res)
	return nil
}

// PrintReport 输出匹配结果、最优性依据与逐对阻挡检查证据。
func PrintReport(w io.Writer, p *Problem, res Result) {
	fmt.Fprintln(w, "=== 最大规模弱稳定匹配 ===")
	fmt.Fprintf(w, "匹配规模: %d\n", res.BestSize)
	fmt.Fprintf(w, "穷举统计: 共检查 %d 个匹配(仅按规模上界剪枝), 其中弱稳定 %d 个, 达到最大规模的 %d 个\n",
		res.Enumerated, res.StableCount, res.MaxCount)
	fmt.Fprintln(w, "最优性依据: 对所有匹配做了穷举, 不存在规模更大的弱稳定匹配;")
	fmt.Fprintln(w, "            求解全程未对并列偏好做任何任意打破。")
	fmt.Fprintln(w)

	fmt.Fprintln(w, "--- 匹配结果 ---")
	jobAssign := buildJobAssign(p, res.BestAssign)
	for j := range p.Jobs {
		names := make([]string, 0, len(jobAssign[j]))
		for _, a := range jobAssign[j] {
			names = append(names, p.Applicants[a])
		}
		if len(names) == 0 {
			fmt.Fprintf(w, "  %s (容量 %d): 空缺\n", p.Jobs[j], p.Capacity[j])
		} else {
			fmt.Fprintf(w, "  %s (容量 %d, 已占 %d): %v\n", p.Jobs[j], p.Capacity[j], len(names), names)
		}
	}
	for a := range p.Applicants {
		if res.BestAssign[a] == -1 {
			fmt.Fprintf(w, "  %s: 未匹配\n", p.Applicants[a])
		}
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "--- 阻挡对检查证据 ---")
	fmt.Fprintf(w, "互相可接受但未配对的组合共 %d 对, 其中阻挡对 %d 个\n",
		len(res.Evidence), res.BlockingHits)
	for _, c := range res.Evidence {
		mark := "未阻挡"
		if c.Blocking {
			mark = "阻挡!"
		}
		fmt.Fprintf(w, "  [%s] (%s, %s): %s\n", mark, c.Applicant, c.Job, c.Detail)
	}
	if len(res.Evidence) == 0 {
		fmt.Fprintln(w, "  (无待检查的组合)")
	}
}
