// sim 是槽位迁移故障重放 CLI：本地合成数据与消息，确定性执行，无真实网络/数据库/睡眠。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"

	"slotmig/sim"
)

func main() {
	var (
		M        = flag.Int("M", 7, "素数槽位数")
		B        = flag.Int("B", 1<<20, "全局未确认增量缓存上限（字节）")
		scenario = flag.String("scenario", "full", "场景: full|capacity|restart-boundary|conflict|multi-slot")
		record   = flag.String("record", "", "把生成的故障脚本记录到文件(JSON)")
		replay   = flag.String("replay", "", "从文件重放故障脚本(JSON)")
		expect   = flag.String("expect", "", "重放时期望的证据摘要")
		evOut    = flag.String("evidence", "", "把证据事件流写到文件(JSON)")
		list     = flag.Bool("list", false, "列出全部场景")
	)
	flag.Parse()

	scen := sim.Scenarios()
	if *list {
		fmt.Println("可用场景:", sim.SortedKeys(scen))
		return
	}

	var ops []sim.Op
	if *replay != "" {
		data, err := os.ReadFile(*replay)
		if err != nil {
			fatal(err)
		}
		if err := json.Unmarshal(data, &ops); err != nil {
			fatal(err)
		}
		fmt.Printf("重放脚本 %s（%d 条指令）\n", *replay, len(ops))
	} else {
		gen, ok := scen[*scenario]
		if !ok {
			fatal(fmt.Errorf("未知场景 %q，可选: %v", *scenario, sim.SortedKeys(scen)))
		}
		var err error
		ops, err = gen(*M, *B)
		if err != nil {
			fatal(err)
		}
	}

	if *record != "" {
		data, _ := json.MarshalIndent(ops, "", "  ")
		if err := os.WriteFile(*record, data, 0o644); err != nil {
			fatal(err)
		}
		fmt.Printf("脚本已记录到 %s（%d 条指令）\n", *record, len(ops))
	}

	r, err := sim.NewRunner(*M, *B)
	if err != nil {
		fatal(err)
	}
	r.Run(ops)

	// 逐请求结果
	fmt.Println("== 逐请求结果 ==")
	for i, res := range r.Res {
		fmt.Printf("#%d %-5s slot=%d node=%s epoch=%d seq=%d %s\n", i, res.Status, res.Slot, res.Node, res.Epoch, res.Seq, res.Detail)
	}
	// 各槽迁移阶段
	dump := r.C.Dump()
	if len(dump.Migs) > 0 {
		fmt.Println("== 迁移阶段 ==")
		for _, m := range dump.Migs {
			fmt.Printf("slot=%d %s->%s phase=%s baseline=%d final=%d newEpoch=%d\n",
				m.Slot, m.Source, m.Target, m.Phase, m.BaselineSeq, m.FinalSeq, m.NewEpoch)
		}
	}
	// 当前路由
	fmt.Println("== 当前路由 ==")
	fmt.Printf("version=%d primary=%v epoch=%v replica=%v\n", dump.Config.Version, dump.Config.Primary, dump.Config.Epoch, dump.Config.Replica)
	// 有序变更证据
	fmt.Println("== 有序变更证据 ==")
	for _, sd := range dump.Slots {
		if len(sd.Log) == 0 {
			continue
		}
		fmt.Printf("slot=%d primary=%s epoch=%d kv=%v\n", sd.Slot, sd.Primary, sd.Epoch, sd.KV)
		for _, ch := range sd.Log {
			fmt.Printf("  seq=%d epoch=%d %s %s=%s idem=%s\n", ch.Seq, ch.Epoch, ch.Op, ch.Key, ch.Val, ch.Idem)
		}
	}
	// 关键事件（路由/阶段/fence/决议/背压/冲突）
	fmt.Println("== 关键事件 ==")
	for _, e := range r.C.Ev {
		switch e.Kind {
		case "route", "phase", "fence", "decision", "backpressure", "conflict", "crash", "recover":
			fmt.Printf("[%s] slot=%d node=%s %s\n", e.Kind, e.Slot, e.Node, e.Detail)
		}
	}

	if *evOut != "" {
		data, _ := json.MarshalIndent(r.C.Ev, "", "  ")
		if err := os.WriteFile(*evOut, data, 0o644); err != nil {
			fatal(err)
		}
	}

	digest := r.Digest()
	fmt.Printf("证据摘要: %s\n", digest)
	if *expect != "" && *expect != digest {
		fmt.Printf("重放校验失败: 期望 %s 实得 %s\n", *expect, digest)
		os.Exit(1)
	}

	violations := r.Check()
	if *scenario == "conflict" && *replay == "" {
		// conflict 场景故意制造违规，检查器必须能报出来
		if len(violations) == 0 {
			fatal(fmt.Errorf("conflict 场景未被检查器检出"))
		}
		fmt.Printf("检查器如期检出 %d 项违规（冲突场景）\n", len(violations))
		return
	}
	if len(violations) > 0 {
		fmt.Println("== 不变量违规 ==")
		for _, v := range violations {
			fmt.Println(" -", v)
		}
		os.Exit(1)
	}
	fmt.Println("不变量检查: 全部通过")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(1)
}

var _ = sort.Strings
