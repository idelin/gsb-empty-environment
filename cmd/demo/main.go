// Command demo runs the acceptance scenario end to end:
//
//	A long procurement transaction with three steps: approve ->
//	purchase -> pay. The payment step waits for an external bank
//	that never answers; the engine's timeout monitor wakes up and
//	drives reverse compensation: cancel the purchase order and
//	notify the approver. The full journaled audit trail is printed.
//
// In the demo, hours are compressed into seconds via -pay-timeout.
package main

import (
	"flag"
	"fmt"
	"os"
	"sync"
	"time"

	"sagademo/saga"
)

// ---- Simulated external systems -------------------------------------

// approver is the approval system; its human approves asynchronously.
type approver struct {
	eng *saga.Engine
}

func (a *approver) request(ctx *saga.StepContext) (interface{}, error) {
	fmt.Printf("  [审批系统] 收到审批请求 key=%s，审批人处理中...\n", ctx.IdempotencyKey)
	txID := ctx.TxID
	go func() {
		time.Sleep(300 * time.Millisecond) // 模拟审批人耗时
		a.eng.Deliver(txID, "APPROVED")
	}()
	return nil, saga.ErrAwait
}

func (a *approver) notifyCancel(ctx *saga.StepContext) error {
	fmt.Printf("  [审批系统] 已通知审批人：事务 %s 已取消（补偿动作 key=%s）\n",
		ctx.TxID, ctx.IdempotencyKey)
	return nil
}

// purchaseGateway creates purchase orders, deduplicated by idempotency key.
type purchaseGateway struct {
	mu     sync.Mutex
	orders map[string]string
	next   int
}

func newPurchaseGateway() *purchaseGateway {
	return &purchaseGateway{orders: make(map[string]string)}
}

func (p *purchaseGateway) create(ctx *saga.StepContext) (interface{}, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id, ok := p.orders[ctx.IdempotencyKey]; ok {
		fmt.Printf("  [采购系统] 幂等去重：key=%s 已存在订单 %s，不重复创建\n", ctx.IdempotencyKey, id)
		ctx.Data["order_id"] = id
		return id, nil
	}
	p.next++
	id := fmt.Sprintf("PO-%04d", p.next)
	p.orders[ctx.IdempotencyKey] = id
	ctx.Data["order_id"] = id
	fmt.Printf("  [采购系统] 创建采购单 %s key=%s\n", id, ctx.IdempotencyKey)
	return id, nil
}

func (p *purchaseGateway) cancel(ctx *saga.StepContext) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	orderID, _ := ctx.Data["order_id"].(string)
	delete(p.orders, ctx.IdempotencyKey)
	fmt.Printf("  [采购系统] 撤销采购单 %s（补偿动作 key=%s）\n", orderID, ctx.IdempotencyKey)
	return nil
}

// bank is the external payment system. In this scenario it is
// "down": it accepts the request but never calls back.
type bank struct{}

func (b *bank) pay(ctx *saga.StepContext) (interface{}, error) {
	fmt.Printf("  [银行] 收到付款请求 key=%s（银行故障，永不回调）\n", ctx.IdempotencyKey)
	return nil, saga.ErrAwait
}

// ---- Demo ------------------------------------------------------------

func main() {
	dir := flag.String("dir", "./demo-data", "journal directory")
	payTimeout := flag.Duration("pay-timeout", 2*time.Second, "payment callback timeout (stands in for hours)")
	flag.Parse()

	os.RemoveAll(*dir)

	ap := &approver{}
	po := newPurchaseGateway()
	bk := &bank{}

	eng, err := saga.Open(*dir, nil)
	if err != nil {
		panic(err)
	}
	ap.eng = eng

	eng.Register(&saga.Definition{
		Name: "procurement",
		Steps: []saga.Step{
			{Name: "approve", Timeout: time.Hour, Action: ap.request, Compensate: ap.notifyCancel},
			{Name: "purchase", Action: po.create, Compensate: po.cancel},
			{Name: "pay", Timeout: *payTimeout, Action: bk.pay},
		},
	})

	fmt.Println("== 发起长采购事务（审批 -> 采购 -> 付款） ==")
	txID, err := eng.Start("procurement", map[string]interface{}{"amount": "98000", "currency": "CNY"})
	if err != nil {
		panic(err)
	}
	fmt.Printf("事务ID: %s\n\n", txID)

	status, err := eng.Wait(txID)
	if err != nil {
		panic(err)
	}
	fmt.Printf("\n== 事务终态: %s ==\n\n", status)

	events, err := eng.Audit(txID)
	if err != nil {
		panic(err)
	}
	fmt.Println("== 可复核状态审计轨迹（持久化日志） ==")
	for _, ev := range events {
		line := fmt.Sprintf("  #%02d %-20s", ev.Seq, ev.Type)
		if ev.Step != "" {
			line += fmt.Sprintf(" step=%-8s", ev.Step)
		}
		if ev.Output != nil {
			line += fmt.Sprintf(" output=%v", ev.Output)
		}
		if ev.Err != "" {
			line += fmt.Sprintf(" err=%s", ev.Err)
		}
		if !ev.Deadline.IsZero() {
			line += fmt.Sprintf(" deadline=%s", ev.Deadline.Format("15:04:05.000"))
		}
		fmt.Println(line)
	}
	eng.Close()
}
