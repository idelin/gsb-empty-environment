// demo 运行验收场景：发起长采购事务（审批 -> 采购 -> 付款），
// 付款阶段等待外部银行回执超时，引擎的超时监控器唤醒后执行
// 逆向补偿（取消采购单、通知审批人），并输出全链路审计轨迹。
package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"sagademo/saga"
)

func main() {
	dir := "demo-data"
	os.RemoveAll(dir)
	store, err := saga.NewStore(dir)
	if err != nil {
		log.Fatal(err)
	}
	eng := saga.NewEngine(store, saga.WithMonitorInterval(50*time.Millisecond))

	action := func(format string, a ...any) {
		fmt.Printf("  [业务动作] %s\n", fmt.Sprintf(format, a...))
	}

	eng.Register(&saga.SagaDef{
		Name: "procurement",
		Steps: []saga.Step{
			{
				Name: "approve",
				Action: func(ctx *saga.Context) error {
					action("审批通过：采购申请已批准（审批人 张经理）")
					ctx.Data["approver"] = "张经理"
					return nil
				},
				Compensate: func(ctx *saga.Context) error {
					action("逆向补偿：通知审批人 %s —— 事务已取消", ctx.Data["approver"])
					return nil
				},
			},
			{
				Name: "purchase",
				Action: func(ctx *saga.Context) error {
					action("采购下单：已创建采购单 PO-1001")
					ctx.Data["po"] = "PO-1001"
					return nil
				},
				Compensate: func(ctx *saga.Context) error {
					action("逆向补偿：取消采购单 %s", ctx.Data["po"])
					return nil
				},
			},
			{
				Name: "pay",
				// 真实场景为数小时，演示中压缩为 300ms。
				Timeout: 300 * time.Millisecond,
				Action: func(ctx *saga.Context) error {
					action("付款请求：已发送银行，等待异步回执（模拟数小时）")
					return saga.ErrWait
				},
				// 付款未完成即超时，无需补偿（未发生真实扣款）。
			},
		},
	})

	eng.StartMonitor()
	defer eng.Stop()

	fmt.Println("== 发起长采购事务（审批 -> 采购 -> 付款） ==")
	id, err := eng.Start("procurement")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("事务实例 ID: %s\n", id)

	if !eng.Wait(id, 5*time.Second) {
		log.Fatal("事务未在预期时间内到达终态")
	}
	inst, err := eng.Instance(id)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\n== 事务终态: %s ==\n", inst.Status)
	for _, s := range inst.Steps {
		fmt.Printf("  step=%-10s status=%-10s attempts=%d err=%s\n", s.Name, s.Status, s.Attempts, s.Err)
	}

	fmt.Println("\n== 可复核状态审计轨迹 ==")
	trail, err := eng.AuditTrail(id)
	if err != nil {
		log.Fatal(err)
	}
	for _, ev := range trail {
		fmt.Printf("  %s  %-20s step=%-10s %s\n",
			ev.Time.Format("15:04:05.000"), ev.Type, ev.Step, ev.Detail)
	}
	fmt.Printf("\n状态快照: %s/instances/%s.json\n", dir, id)
	fmt.Printf("审计日志: %s/audit/%s.log\n", dir, id)
}
