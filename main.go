package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
)

// 输入 JSON 格式：
// {
//   "system1": {"states": ["s0","s1"], "initial": "s0",
//               "transitions": [["s0","a","s1"]]},
//   "system2": { ... }
// }
// 内部动作用标签 "tau" 表示；每个系统至多 30 个状态。
type jsonLTS struct {
	States      []string    `json:"states"`
	Initial     string      `json:"initial"`
	Transitions [][3]string `json:"transitions"`
}

type input struct {
	System1 jsonLTS `json:"system1"`
	System2 jsonLTS `json:"system2"`
}

func toLTS(name string, j jsonLTS) (*LTS, error) {
	trs := make([]Transition, len(j.Transitions))
	for i, t := range j.Transitions {
		trs[i] = Transition{From: t[0], Label: t[1], To: t[2]}
	}
	return NewLTS(name, j.States, j.Initial, trs)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(1)
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "用法: %s <input.json>\n", os.Args[0])
		os.Exit(2)
	}
	data, err := ioutil.ReadFile(os.Args[1])
	if err != nil {
		fatal(err)
	}
	var in input
	if err := json.Unmarshal(data, &in); err != nil {
		fatal(err)
	}
	l1, err := toLTS("系统一", in.System1)
	if err != nil {
		fatal(err)
	}
	l2, err := toLTS("系统二", in.System2)
	if err != nil {
		fatal(err)
	}

	res := WeakBisimulation(l1, l2)

	fmt.Printf("最大弱互模拟关系（共 %d 对）:\n", len(res.Relation))
	for _, pr := range res.Relation {
		fmt.Printf("  %s ~ %s\n", pr.P, pr.Q)
	}

	if res.Related(l1.Initial, l2.Initial) {
		fmt.Println("结论: 两系统（的初始状态）弱互模拟等价")
		return
	}

	fmt.Println("结论: 两系统（的初始状态）不弱互模拟等价")
	f, err := res.Distinguish(l1.Initial, l2.Initial)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("区分公式（系统一初始状态满足、系统二初始状态不满足）:\n  %s\n", f)
	fmt.Println("可重放区分过程:")
	for _, ln := range Replay(f, l1, l1.Initial, l2, l2.Initial) {
		fmt.Println("  " + ln)
	}
}
