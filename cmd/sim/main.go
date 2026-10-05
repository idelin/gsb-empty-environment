// sim 是保障结算模拟器的命令行入口。
//
// 用法: sim <input.json>
// 输入 JSON 描述保障规则与理赔事件（事件按 seq 顺序到达），
// 输出每笔结算的扣减原因、迟到事件引发的追补/追回记录，
// 以及限额不变量校验结果。存在违规时退出码为 1。
package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"sort"

	"github.com/example/settlement-sim/sim"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: sim <input.json>")
		os.Exit(2)
	}
	data, err := ioutil.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "read input:", err)
		os.Exit(2)
	}
	var in sim.Input
	if err := json.Unmarshal(data, &in); err != nil {
		fmt.Fprintln(os.Stderr, "parse input:", err)
		os.Exit(2)
	}

	engine, err := sim.NewEngine(in.Policy)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid policy:", err)
		os.Exit(2)
	}

	// 事件按到达序号（seq）依次进入引擎，模拟迟到事件。
	events := append([]sim.Event(nil), in.Events...)
	sort.SliceStable(events, func(i, j int) bool { return events[i].Seq < events[j].Seq })
	for _, ev := range events {
		if err := engine.Submit(ev); err != nil {
			fmt.Fprintln(os.Stderr, "submit event:", err)
			os.Exit(2)
		}
	}

	report := engine.Report()
	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "encode report:", err)
		os.Exit(2)
	}
	fmt.Println(string(out))

	if len(report.Violations) > 0 {
		os.Exit(1)
	}
}
