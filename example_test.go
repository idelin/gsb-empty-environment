package netsec_test

import (
	"errors"
	"fmt"

	"example.org/netsec/q0397"
)

// buildMessagePassing 构建秘密缓冲区发布范式：
//
//	T0: W(data)=secret; W(flag)=1
//	T1: R(flag);        R(data)
//
// writeOrder/readOrder 取 release/acquire 时为安全发布，取 plain 时为普通读写。
func buildMessagePassing(writeOrder, readOrder netsec.Order) (*netsec.Program, map[string]int) {
	b, _ := netsec.NewProgramBuilder(2)
	ids := map[string]int{}
	ids["initData"], _ = b.AddInitialWrite("data", 0)
	ids["initFlag"], _ = b.AddInitialWrite("flag", 0)
	ids["wData"], _ = b.Add(0, netsec.Event{Loc: "data", IsWrite: true, Value: 42})
	ids["wFlag"], _ = b.Add(0, netsec.Event{Loc: "flag", IsWrite: true, Order: writeOrder, Value: 1})
	ids["rFlag"], _ = b.Add(1, netsec.Event{Loc: "flag", Order: readOrder})
	ids["rData"], _ = b.Add(1, netsec.Event{Loc: "data"})
	prog, _ := b.Build()
	return prog, ids
}

func Example() {
	// 断言失败条件：rData 读到秘密值 42，但 rFlag 仍读到旧标记 0（未发布）。
	check := func(writeOrder, readOrder netsec.Order, label string) {
		prog, ids := buildMessagePassing(writeOrder, readOrder)
		q := netsec.Query{
			Program: prog,
			ReadFroms: []netsec.ReadFrom{
				{Reader: ids["rFlag"], Writer: ids["initFlag"]},
				{Reader: ids["rData"], Writer: ids["wData"]},
			},
		}
		rf, _ := netsec.ReadFromMap(q)
		q.Fail = netsec.FailOnRead(rf, map[int]int{ids["rData"]: 42})

		witness, err := netsec.ShortestWitness(q)
		switch {
		case errors.Is(err, netsec.ErrInfeasible):
			fmt.Printf("%s: 读自边组合被先行发生/写序排除，无泄漏\n", label)
		case err != nil:
			fmt.Println("error:", err)
		case witness == nil:
			fmt.Printf("%s: 合法执行都满足断言\n", label)
		default:
			fmt.Printf("%s: 存在泄漏见证，最短失败前缀长度=%d\n", label, witness.FailAt)
		}
	}

	check(netsec.OrderRelease, netsec.OrderAcquire, "发布/获取")
	check(netsec.OrderPlain, netsec.OrderPlain, "普通读写")

	// Output:
	// 发布/获取: 读自边组合被先行发生/写序排除，无泄漏
	// 普通读写: 存在泄漏见证，最短失败前缀长度=2
}
