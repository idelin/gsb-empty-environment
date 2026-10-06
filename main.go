// 命令行入口：
//   go run . solve  <problem.json>                枚举求解并输出最优策略
//   go run . check  <problem.json> "<s=a,...>"    核对给定策略的容量与规则
//   go run . list   <problem.json>                列出全部可行策略及目标值
package main

import (
	"fmt"
	"os"
	"sort"

	"strategysolver/solver"
)

func die(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: %s solve|check|list <problem.json> [strategy]\n", os.Args[0])
		os.Exit(2)
	}
	cmd, path := os.Args[1], os.Args[2]
	p, err := solver.Load(path)
	if err != nil {
		die("load: %v", err)
	}
	switch cmd {
	case "solve":
		res, err := p.Enumerate()
		if err != nil {
			die("solve: %v", err)
		}
		fmt.Printf("enumerated %d strategies, %d feasible\n", res.Enumerated, res.Feasible)
		fmt.Printf("optimal strategy: %s\n", res.Best.Strategy)
		fmt.Printf("worst-case loss:  %s\n", res.Best.Worst.RatString())
		fmt.Printf("average loss:     %s\n", res.Best.Average.RatString())
	case "check":
		if len(os.Args) < 4 {
			die("check requires a strategy, e.g. \"s1=a1,s2=a2\"")
		}
		st, err := solver.LoadStrategy(os.Args[3])
		if err != nil {
			die("parse strategy: %v", err)
		}
		ev, err := p.Evaluate(st)
		if err != nil {
			die("evaluate: %v", err)
		}
		if !ev.Feasible {
			fmt.Printf("INFEASIBLE: %s\n", ev.Violation)
			os.Exit(1)
		}
		fmt.Printf("feasible: worst=%s average=%s\n", ev.Worst.RatString(), ev.Average.RatString())
	case "list":
		listFeasible(p)
	default:
		die("unknown command %q", cmd)
	}
}

func listFeasible(p *solver.Problem) {
	signals := p.Signals()
	actions := p.SortedActions()
	idx := make([]int, len(signals))
	var evs []*solver.Evaluation
	for {
		st := make(solver.Strategy, len(signals))
		for i, s := range signals {
			st[s] = actions[idx[i]]
		}
		ev, err := p.Evaluate(st)
		if err != nil {
			die("evaluate: %v", err)
		}
		if ev.Feasible {
			evs = append(evs, ev)
		}
		i := len(idx) - 1
		for ; i >= 0; i-- {
			idx[i]++
			if idx[i] < len(actions) {
				break
			}
			idx[i] = 0
		}
		if i < 0 {
			break
		}
	}
	sort.Slice(evs, func(i, j int) bool { return solver.Better(evs[i], evs[j]) })
	for _, ev := range evs {
		fmt.Printf("%-32s worst=%-8s average=%s\n", ev.Strategy, ev.Worst.RatString(), ev.Average.RatString())
	}
}
