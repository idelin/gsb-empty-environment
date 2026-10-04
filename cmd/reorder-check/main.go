// reorder-check：指令重排合法性检查器。
//
// 用法：
//   reorder-check -orig 原始序列文件 -cand 候选序列文件 [选项]
//
// 退出码：0 合法；1 非法重排；2 用法/解析错误。
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"

	"reordercheck/reorder"
)

func main() {
	origPath := flag.String("orig", "", "原始指令序列文件（必需）")
	candPath := flag.String("cand", "", "待验证的候选序列文件（必需）")
	domainFlag := flag.String("domain", "0,1", "穷举取值域，逗号分隔")
	maxStates := flag.Int("max-states", 1<<20, "穷举初始状态数上限")
	verbose := flag.Bool("v", false, "打印全部依赖边")
	flag.Parse()

	if *origPath == "" || *candPath == "" {
		fmt.Fprintln(os.Stderr, "必须同时指定 -orig 与 -cand")
		flag.Usage()
		os.Exit(2)
	}

	orig, err := loadFile(*origPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "原始序列：%v\n", err)
		os.Exit(2)
	}
	cand, err := loadFile(*candPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "候选序列：%v\n", err)
		os.Exit(2)
	}
	domain, err := parseDomain(*domainFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "取值域：%v\n", err)
		os.Exit(2)
	}

	fmt.Printf("原始序列 %d 条指令，候选序列 %d 条指令\n", len(orig), len(cand))

	edges := reorder.ComputeEdges(orig)
	var nRAW, nWAR, nWAW, nBar int
	for _, e := range edges {
		switch e.Kind {
		case reorder.DepRAW:
			nRAW++
		case reorder.DepWAR:
			nWAR++
		case reorder.DepWAW:
			nWAW++
		case reorder.DepBarrier:
			nBar++
		}
	}
	fmt.Printf("依赖边共 %d 条：RAW %d，WAR %d，WAW %d，屏障 %d\n",
		len(edges), nRAW, nWAR, nWAW, nBar)
	if *verbose {
		for _, e := range edges {
			fmt.Printf("  %s\n", e)
		}
	}

	res := reorder.Check(orig, cand)
	legal := res.Legal()

	if !res.Permutation {
		fmt.Printf("结论：非法 —— 候选不是原始序列的排列（%s）\n", res.MatchErr)
	} else if !legal {
		fmt.Printf("结论：非法重排 —— 违反 %d 条依赖边\n", len(res.Violated))
		if e, ok := res.Earliest(); ok {
			fmt.Printf("最早冲突依赖边：%s\n", e)
			fmt.Printf("  原始 #%d：%s\n", e.From+1, orig[e.From])
			fmt.Printf("  原始 #%d：%s\n", e.To+1, orig[e.To])
		}
		if *verbose {
			for _, e := range res.Violated {
				fmt.Printf("  违反：%s\n", e)
			}
		}
	} else {
		fmt.Printf("结论：合法重排 —— 全部 %d 条依赖边均保持\n", len(edges))
	}

	// 有限初始状态穷举（辅助核对）
	if res.Permutation {
		ex := reorder.ExhaustiveCheck(orig, cand, domain, *maxStates)
		scope := "完全穷举"
		if !ex.Exhaustive {
			scope = "状态空间过大，模式+采样（非穷举）"
		}
		fmt.Printf("有限状态核对：域 {%s}，测试 %d 个初始状态（%s）\n",
			*domainFlag, ex.Tested, scope)
		if ex.Divergence != nil {
			fmt.Printf("  发现发散初始状态：\n")
			for _, line := range strings.Split(ex.Divergence.Describe(), "\n") {
				fmt.Printf("    %s\n", line)
			}
		} else {
			fmt.Printf("  未发现发散（注意：碰巧结果相同不能证明无依赖/合法）\n")
		}
	}

	if !legal {
		os.Exit(1)
	}
}

func loadFile(path string) ([]reorder.Inst, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return reorder.Parse(string(data))
}

func parseDomain(s string) ([]uint64, error) {
	var domain []uint64
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		v, err := strconv.ParseUint(tok, 0, 64)
		if err != nil {
			return nil, fmt.Errorf("非法取值 %q", tok)
		}
		domain = append(domain, v)
	}
	if len(domain) == 0 {
		return nil, fmt.Errorf("取值域为空")
	}
	return domain, nil
}
