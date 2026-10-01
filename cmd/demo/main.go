// 运行样例：两个客户端并发编辑并通过因果树 CRDT 收敛。
package main

import (
	"fmt"

	"causaltree"
)

func main() {
	alice := causaltree.New("A")
	bob := causaltree.New("B")

	// Alice 输入 "hello" 并同步给 Bob。
	parent := alice.LastID()
	for _, ch := range "hello" {
		op := alice.InsertAfter(parent, ch)
		bob.Apply(op)
		parent = op.ID
	}
	fmt.Printf("共同基础:  alice=%q bob=%q\n", alice.String(), bob.String())

	// 两端在 'o' 之后并发插入，互不可见。
	opBang := alice.InsertAfter(alice.LastID(), '!')
	opQ := bob.InsertAfter(bob.LastID(), '?')
	fmt.Printf("并发插入:  alice 插入 %q (id=%v), bob 插入 %q (id=%v)\n",
		opBang.Char, opBang.ID, opQ.Char, opQ.ID)
	fmt.Printf("同步前:    alice=%q bob=%q\n", alice.String(), bob.String())

	// 交换操作，因果树按 (Lamport, ClientID) 确定性排序。
	alice.Apply(opQ)
	bob.Apply(opBang)
	fmt.Printf("同步后:    alice=%q bob=%q\n", alice.String(), bob.String())
	fmt.Printf("逐字一致:  %v\n", alice.String() == bob.String())
}
