package main

import (
	"fmt"

	"eventrules/rules"
)

func main() {
	e := rules.New()
	emit := func(d rules.Delta) { fmt.Println(d) }

	fmt.Println("== 同对象多事件，按业务优先级再事件序号裁决 ==")
	emit(e.Apply(rules.Event{ID: "e1", Object: "订单A", Priority: 1, Seq: 100, Payload: "待支付"}))
	emit(e.Apply(rules.Event{ID: "e2", Object: "订单A", Priority: 1, Seq: 101, Payload: "已支付"}))
	emit(e.Apply(rules.Event{ID: "e3", Object: "订单A", Priority: 5, Seq: 1, Payload: "已发货"}))

	fmt.Println("\n== 撤回先于事件到达（只针对特定事件 id） ==")
	emit(e.Retract("e5")) // e5 尚未到达，先立墓碑
	emit(e.Apply(rules.Event{ID: "e4", Object: "订单A", Priority: 9, Seq: 1, Payload: "已签收"}))
	emit(e.Apply(rules.Event{ID: "e5", Object: "订单A", Priority: 9, Seq: 2, Payload: "恶意重放"})) // 到达即被墓碑拦截

	fmt.Println("\n== 撤回赢家，下一候选显露 ==")
	emit(e.Retract("e4"))
	emit(e.Retract("e3"))

	fmt.Println("\n== 重复与冲突载荷 ==")
	emit(e.Apply(rules.Event{ID: "e2", Object: "订单A", Priority: 1, Seq: 101, Payload: "已支付"}))
	emit(e.Apply(rules.Event{ID: "e2", Object: "订单A", Priority: 9, Seq: 999, Payload: "伪造载荷"}))
	fmt.Printf("重复投递: %d 次, 冲突载荷: %d 次\n", len(e.Duplicates), len(e.Conflicts))

	if ps := e.Verify(); len(ps) != 0 {
		fmt.Println("全量模型核对失败:", ps)
	} else {
		fmt.Println("全候选排序模型核对: 通过")
	}
}
