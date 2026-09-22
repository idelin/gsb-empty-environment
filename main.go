// Command outboxsim 运行一个带三个故障窗口的端到端时间线：
//
//	c1：提交前崩溃            -> 未提交不外发，重投后成功，业务只生效一次
//	c2：提交后响应丢失        -> 重投命中 inbox，不再改业务
//	c3：outbox 发送后未标记   -> relay 重发，下游去重，效果恰好一次
package main

import (
	"fmt"
	"strings"

	"outboxsim/bank"
	"outboxsim/sim"
	"outboxsim/store"
)

func main() {
	s := sim.New(20260922)
	svcDB, dsDB := store.New(), store.New()
	svc := bank.NewService(svcDB, s.Bus, func(ids []string) {
		s.RecordTrace("commit", bank.NodeSvc, strings.Join(ids, " "))
	})
	ds := bank.NewDownstream(dsDB, s.Bus)
	cmds := []bank.Command{
		{CmdID: "c1", Acct: "A", Amount: 10},
		{CmdID: "c2", Acct: "A", Amount: 20},
		{CmdID: "c3", Acct: "A", Amount: 30},
	}
	client := bank.NewClient("client", s.Bus, cmds)

	s.AddNode(bank.NodeSvc, svc).Boot()
	s.AddNode(bank.NodeDS, ds).Boot()
	s.AddNode("client", client).Boot()

	// 三个故障窗口，每个只注入一次。
	s.Arm("svc.before_commit:c1", 1)
	s.Arm("svc.after_commit:c2", 1)
	s.Arm("svc.after_publish:ev-c3", 1)

	var interesting []sim.Trace
	seen := 0
	ok := s.RunUntil(func(*sim.Sim) bool {
		// 每事件收集关键轨迹。
		t := s.Traces()
		for _, x := range t[seen:] {
			seen++
			if notable(x) {
				interesting = append(interesting, x)
			}
		}
		return client.Done() &&
			bank.CountPrefix(svcDB, "outbox:") == bank.CountPrefix(dsDB, "effect:") &&
			allSent(svcDB)
	}, 5000)

	fmt.Println("== 故障窗口重放时间线（逻辑时钟）==")
	for _, x := range interesting {
		fmt.Print(line(x))
	}
	fmt.Println()
	fmt.Println("== 最终状态 ==")
	for _, c := range cmds {
		fmt.Printf("命令 %s 响应: %v\n", c.CmdID, client.DoneReceipts()[c.CmdID])
	}
	fmt.Printf("账户 A 余额: %d (期望 60；重投未重复加钱)\n", bank.Balance(svcDB, "A"))
	fmt.Printf("上游 inbox/outbox 行数: %d / %d\n",
		bank.CountPrefix(svcDB, "inbox:"), bank.CountPrefix(svcDB, "outbox:"))
	fmt.Printf("下游 inbox/effect 行数: %d / %d (重复投递被去重)\n",
		bank.CountPrefix(dsDB, "dsinbox:"), bank.CountPrefix(dsDB, "effect:"))
	fmt.Printf("event 发送 %d 次、投递 %d 次 (至少一次, 含重发)\n",
		s.Bus.SendCount("event"), s.Bus.DeliveredCount("event"))
	fmt.Printf("收敛: %v @t=%d\n", ok, s.Now)
}

func allSent(db *store.DB) bool {
	for _, k := range db.ViewKeys("outbox:") {
		id := strings.TrimPrefix(k, "outbox:")
		if st, ok := bank.OutboxStatus(db, id); !ok || st != "sent" {
			return false
		}
	}
	return true
}

func notable(x sim.Trace) bool {
	if x.Msg != nil {
		return x.Kind == "deliver" || x.Kind == "drop"
	}
	return x.Kind == "crash" || x.Kind == "reboot" || x.Kind == "commit"
}

func line(x sim.Trace) string {
	switch {
	case x.Msg != nil:
		tag := ""
		if x.Kind == "drop" {
			tag = " [丢失]"
		}
		return fmt.Sprintf("t%-5d %-8s %s%s\n", x.Tick, x.Kind, x.Msg, tag)
	case x.Kind == "commit":
		return fmt.Sprintf("t%-5d %-8s outbox 已提交: %s\n", x.Tick, x.Kind, x.Label)
	default:
		return fmt.Sprintf("t%-5d %-8s %s @%s\n", x.Tick, x.Kind, x.Node, x.Label)
	}
}
