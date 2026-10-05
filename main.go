package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
)

func main() {
	input := flag.String("i", "", "输入 JSON 文件(缺省从标准输入读取)")
	verbose := flag.Bool("v", false, "列出全部互可接受对的检查证据(缺省仅列出关键对)")
	flag.Parse()

	var data []byte
	var err error
	if *input == "" {
		data, err = ioutil.ReadAll(os.Stdin)
	} else {
		data, err = ioutil.ReadFile(*input)
	}
	if err != nil {
		fatal("读取输入失败: %v", err)
	}
	var inst Instance
	if err := json.Unmarshal(data, &inst); err != nil {
		fatal("解析 JSON 失败: %v", err)
	}
	p, err := Build(&inst)
	if err != nil {
		fatal("输入非法: %v", err)
	}

	s := NewSolver(p)
	best := s.Solve()

	printMatching(p, best)
	printEvidence(p, best, *verbose)
	printOptimality(p, s)
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "错误: "+format+"\n", args...)
	os.Exit(1)
}

func printMatching(p *Problem, m *Matching) {
	fmt.Println("=== 最大规模弱稳定匹配 ===")
	fmt.Printf("匹配规模: %d / %d 名申请者\n", m.Size(), p.NumA)
	pairs := make([]string, 0, m.Size())
	var unmatched []string
	for a, h := range m.Cur {
		if h >= 0 {
			pairs = append(pairs, fmt.Sprintf("  %s — %s", p.ANames[a], p.HNames[h]))
		} else {
			unmatched = append(unmatched, p.ANames[a])
		}
	}
	sort.Strings(pairs)
	for _, line := range pairs {
		fmt.Println(line)
	}
	if len(unmatched) > 0 {
		sort.Strings(unmatched)
		fmt.Printf("未匹配: %v\n", unmatched)
	}
	fmt.Println()
}

func printEvidence(p *Problem, m *Matching, verbose bool) {
	fmt.Println("=== 弱稳定性验证(阻挡对检查证据)===")
	fmt.Println("判定规则: 仅当申请者与医院双方严格更偏好彼此时才构成阻挡对;任一侧并列即不阻挡。")
	ev := VerifyWeakStability(p, m)
	blocking := BlockingPairs(ev)
	var eager, passive []PairEvidence
	for _, e := range ev {
		if e.AEager {
			eager = append(eager, e)
		} else {
			passive = append(passive, e)
		}
	}
	fmt.Printf("关键对(申请者一侧严格偏好对方医院)共 %d 个:\n", len(eager))
	for _, e := range eager {
		mark := "非阻挡"
		if e.Blocking {
			mark = "阻挡!"
		}
		fmt.Printf("  [%s] %s → %s: %s\n", mark, p.ANames[e.A], p.HNames[e.H], e.Reason)
	}
	if verbose {
		fmt.Printf("其余对(申请者一侧未严格偏好)共 %d 个:\n", len(passive))
		for _, e := range passive {
			fmt.Printf("  [非阻挡] %s → %s: %s\n", p.ANames[e.A], p.HNames[e.H], e.Reason)
		}
	} else {
		fmt.Printf("另有 %d 个互可接受对,申请者一侧并不严格偏好对方,不可能阻挡(用 -v 查看明细)。\n", len(passive))
	}
	if len(blocking) > 0 {
		fmt.Printf("结论: 存在 %d 个阻挡对,匹配不是弱稳定的!\n", len(blocking))
	} else {
		fmt.Println("结论: 不存在双方严格更优的阻挡对,匹配是弱稳定的。")
	}
	fmt.Println()
}

func printOptimality(p *Problem, s *Solver) {
	fmt.Println("=== 最优性证据 ===")
	fmt.Println("方法: 对全部匹配做精确分支定界枚举,弱稳定性按原始并列偏好直接判定,未做任何并列打破。")
	fmt.Printf("搜索统计: 节点 %d,完整匹配(叶子)%d,剪枝 %d。\n", s.Nodes, s.Leaves, s.Pruned)
	fmt.Printf("规模为 %d 的弱稳定匹配共 %d 个;不存在规模更大的弱稳定匹配。\n", s.Best.Size(), s.BestCount)
}
