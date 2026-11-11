package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q0097"
)

// 扫描模式自动机依次要求目标 a、b、c 出现在三个时间段。
func ExampleDecide() {
	domain := []netsec.Target{"a", "b", "c", "d"}
	nfa := &netsec.NFA{
		Start:  []int{0},
		Accept: []int{3},
		Trans: map[netsec.TransKey][]int{
			{State: 0, Target: "a"}: {1},
			{State: 1, Target: "b"}: {2},
			{State: 2, Target: "c"}: {3},
		},
	}

	// 第三段只观测到 d；在至多 1 次遗漏的前提下，c 可能被漏采。
	observed := [][]netsec.Target{{"a"}, {"b"}, {"d"}}
	decision, err := netsec.Decide(domain, observed, 1, nfa, nil)
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println("判定:", decision.Verdict)
	fmt.Println("确定告警:", decision.Alarm)
	fmt.Println("合法补全:", decision.Evidence.TotalCompletions)
	fmt.Println("接受补全:", decision.Evidence.AcceptingCompletions)
	fmt.Println("拒绝补全:", decision.Evidence.RejectingCompletions)
	fmt.Println("接受见证第三段补入:", decision.Evidence.Accepting.Added[2])

	// 当观测本身已经满足模式时，任何补全都保持接受，结论才是必然发生。
	full, err := netsec.Decide(domain, [][]netsec.Target{{"a"}, {"b"}, {"c"}}, 1, nfa, nil)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("满观测判定:", full.Verdict, "告警:", full.Alarm)

	// Output:
	// 判定: 可能发生
	// 确定告警: false
	// 合法补全: 10
	// 接受补全: 1
	// 拒绝补全: 9
	// 接受见证第三段补入: [c]
	// 满观测判定: 必然发生 告警: true
}
