// demo 演示验收场景：写入 100 笔转账 → 对第 50 笔冲正 → 校验历史证明。
package main

import (
	"fmt"

	"ledger"
)

func main() {
	l := ledger.New([]byte("demo-signing-key"))

	// 1. 写入 100 笔转账事务，形成哈希链。
	for i := 0; i < 100; i++ {
		from := fmt.Sprintf("acct-%02d", i%10)
		to := fmt.Sprintf("acct-%02d", (i+1)%10)
		if _, err := l.Transfer(from, to, int64(100+i)); err != nil {
			panic(err)
		}
	}
	fmt.Printf("[1] 已写入 100 笔转账，高度=%d，哈希链校验=%v\n",
		l.Height(), l.VerifyChain())
	head, _ := l.Block(99)
	fmt.Printf("    链尾区块: %s\n", head)

	// 2. 对第 50 笔（高度 49）发起冲正。
	const target = 49
	orig, _ := l.Block(target)
	rootV100, _ := l.Root(100)
	proofV100, _ := l.Prove(target, 100)
	fmt.Printf("[2] 原第 50 笔: %s\n", orig)
	fmt.Printf("    冲正前余额: %s=%d  %s=%d\n",
		orig.Payload.From, l.Balance(orig.Payload.From),
		orig.Payload.To, l.Balance(orig.Payload.To))

	rev, err := l.Reverse(target)
	if err != nil {
		panic(err)
	}
	fmt.Printf("[3] 新增第 101 笔冲正: %s\n", rev)
	fmt.Printf("    冲正后余额: %s=%d  %s=%d\n",
		orig.Payload.From, l.Balance(orig.Payload.From),
		orig.Payload.To, l.Balance(orig.Payload.To))

	// 3. 历史证据与路径证明依然有效。
	origAfter, _ := l.Block(target)
	fmt.Printf("[4] 原区块未被覆写: %v\n", origAfter.Equal(orig))
	fmt.Printf("    版本100 历史证明有效: %v\n",
		ledger.VerifyProof(origAfter.Hash, proofV100, rootV100))
	rootV101, _ := l.Root(101)
	proofV101, _ := l.Prove(target, 101)
	fmt.Printf("    版本101 新版证明有效: %v\n",
		ledger.VerifyProof(origAfter.Hash, proofV101, rootV101))
	fmt.Printf("    冲正后全链校验: %v（高度=%d）\n", l.VerifyChain(), l.Height())
}
