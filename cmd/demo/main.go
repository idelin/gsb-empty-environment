// demo 复现验收场景：t=100 规则热切换，t=90 的长事务按旧规则放行，
// t=101 的新请求按新规则拦截，审计日志标记不同规则版本。
package main

import (
	"fmt"

	"riskengine/risk"
)

func main() {
	clock := risk.NewManualClock(0)
	audit := risk.NewAuditLog()

	v1 := &risk.RuleSet{
		Version: "v1",
		Rules: []risk.Rule{
			{Name: "freq+amount", Window: 600, MaxCount: 3, MaxAmount: 10000}, // 10分钟内超3次且金额超1万
		},
	}
	v2 := &risk.RuleSet{
		Version: "v2",
		Rules: []risk.Rule{
			{Name: "freq+amount", Window: 600, MaxCount: 1, MaxAmount: 5000}, // 更严格：超1次且超5千
		},
	}

	engine := risk.NewEngine(clock, v1, audit)

	clock.Set(90)
	must(engine.BeginSession("S1", "alice"))
	fmt.Println("t=90  长事务 S1 开始（钉住规则版本 v1）")

	clock.Set(91)
	report(engine.Evaluate("S1", risk.Event{TxID: "tx1", Account: "alice", Amount: 100, Timestamp: 91}))
	clock.Set(92)
	report(engine.Evaluate("S1", risk.Event{TxID: "tx2", Account: "alice", Amount: 100, Timestamp: 92}))

	clock.Set(100)
	engine.UpdateRules(v2)
	fmt.Println("t=100 规则库热切换 -> v2（更严格阈值）")

	clock.Set(101)
	fmt.Println("t=101 S1 提交第 3 笔（金额 2 万）——按旧规则 v1 裁定：")
	report(engine.Evaluate("S1", risk.Event{TxID: "tx3", Account: "alice", Amount: 20000, Timestamp: 101}))

	fmt.Println("t=101 同账户全新请求（无会话）——按新规则 v2 裁定：")
	report(engine.Evaluate("", risk.Event{TxID: "tx4", Account: "alice", Amount: 20000, Timestamp: 101}))

	fmt.Println("\n===== 审计日志 =====")
	for _, e := range audit.Entries() {
		fmt.Println(e)
	}
}

func report(r risk.Result) {
	fmt.Printf("      -> decision=%s rule_version=%s %s\n", r.Decision, r.RuleVersion, r.Reason)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
