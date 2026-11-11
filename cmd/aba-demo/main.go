// aba-demo 演示告警队列 ABA 问题：枚举三线程脚本的所有交错，
// 找出裸地址 CAS 与带代次 CAS 首次分歧且丢失事件的最短交错。
package main

import (
	"fmt"
	"os"

	netsec "example.org/netsec/q0399"
)

// 经典 ABA 场景：T0 弹出栈顶时被抢占，T1 弹出两个节点并释放，
// T2 复用槽位压入新告警，T0 恢复后裸地址 CAS 误成功，覆盖掉新告警。
const script = `
# T0: 建栈 [事件2 -> 事件1]，然后开始弹出栈顶（读 h、读 n 后被抢占）
T0 alloc a 1
T0 alloc b 2
T0 setnext b a
T0 read z
T0 cas z a
T0 read h
T0 cas h b
T0 read h
T0 next n h
T0 cas h n
# T1: 连续弹出两个节点并释放其槽位
T1 read h
T1 next n h
T1 cas h n
T1 free h
T1 read h
T1 next n h
T1 cas h n
T1 free h
# T2: 复用被释放的槽位，压入新告警事件 3、4
T2 alloc x 3
T2 alloc y 4
T2 read z
T2 cas z y
`

func main() {
	scripts, err := netsec.ParseScript(script)
	if err != nil {
		fmt.Fprintln(os.Stderr, "脚本解析失败:", err)
		os.Exit(1)
	}
	res, err := netsec.FindShortestLoss(scripts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "枚举失败:", err)
		os.Exit(1)
	}
	if res.Witness == nil {
		fmt.Println("未发现首次分歧且丢事件的交错")
		return
	}
	fmt.Print(res.Witness.String())
	full, err := netsec.EnumerateAll(scripts, netsec.DefaultMaxStates)
	if err != nil {
		fmt.Fprintln(os.Stderr, "完整枚举失败:", err)
		os.Exit(1)
	}
	fmt.Printf("完整状态枚举核对: 状态数=%d 丢事件交错数=%d 最短长度=%d 反向分歧=%d 截断=%v\n",
		full.Explored, len(full.Losses), full.Losses[0].Length, full.ReverseDivergences, full.Truncated)
}
