// 演示：规则热切换场景，打印决策与审计日志。
package main

import (
	"fmt"
	"time"

	"riskengine/risk"
)

var base = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }

func main() {
	v1 := &risk.RuleSet{Version: "v1", Rules: []risk.Rule{{
		Name: "freq+amount", Window: 10 * time.Minute, MaxCount: 3, MaxAmount: 10000,
	}}}
	v2 := &risk.RuleSet{Version: "v2", Rules: []risk.Rule{{
		Name: "freq+amount", Window: 10 * time.Minute, MaxCount: 1, MaxAmount: 5000,
	}}}

	engine := risk.New(v1, nil)
	fmt.Println("t=0    规则库加载 v1（10分钟超3次且累计超10000 拦截）")

	submit := func(sec int, id, acc, sess string, amount int64) {
		d := engine.Evaluate(risk.Txn{ID: id, AccountID: acc, SessionID: sess, Amount: amount, At: at(sec)})
		verdict := "放行"
		if !d.Allowed {
			verdict = "拦截"
		}
		fmt.Printf("t=%-4d 交易 %-3s 会话 %-6s 金额 %-5d -> %s (规则版本 %s)\n", sec, id, sess, amount, verdict, d.RuleVersion)
	}

	submit(90, "L1", "acc-A", "S-long", 2000)
	submit(91, "L2", "acc-A", "S-long", 2000)
	submit(92, "L3", "acc-A", "S-long", 2000)

	engine.UpdateRules(v2)
	fmt.Println("t=100  规则库热切换为 v2（更严格：10分钟超1次且累计超5000 拦截）")

	submit(101, "L4", "acc-A", "S-long", 2000) // 长事务按旧规则 v1 放行
	submit(101, "N1", "acc-B", "S-new", 3000)  // 新会话首笔按 v2 放行
	submit(102, "N2", "acc-B", "S-new", 3000)  // 新会话第二笔按 v2 拦截

	fmt.Println("\n===== 审计日志 =====")
	for _, entry := range engine.AuditLog() {
		fmt.Println(entry)
	}
}
