// demo 演示验收场景：写入 100 笔转账 -> 对第 50 笔冲正 -> 验证历史证据。
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"log"

	"auditledger"
)

func short(h [32]byte) string { return fmt.Sprintf("%x…%x", h[:4], h[28:]) }

func main() {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	l, err := auditledger.NewLedger(priv, map[string]int64{"alice": 1_000_000, "bob": 0})
	if err != nil {
		log.Fatal(err)
	}

	// 1. 写入 100 笔转账事务，形成哈希链。
	for i := 1; i <= 100; i++ {
		if _, err := l.AppendTransfer("alice", "bob", 100, fmt.Sprintf("transfer #%d", i)); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Printf("[1] 已写入 %d 笔转账事务\n", l.Len())
	b0, _ := l.GetBlock(0)
	b99, _ := l.GetBlock(99)
	fmt.Printf("    区块#0   hash=%s prev=%s\n", short(b0.Hash), short(b0.PrevHash))
	fmt.Printf("    区块#99  hash=%s prev=%s\n", short(b99.Hash), short(b99.PrevHash))
	fmt.Printf("    余额: alice=%d bob=%d\n", l.Balance("alice"), l.Balance("bob"))

	// 记录第 50 笔（序号 49）的证据与版本 100 的默克尔证明。
	const target = uint64(49)
	before, _ := l.GetBlock(target)
	proofV100, _ := l.ProveAt(target, 100)
	rootV100, _ := l.RootAt(100)
	fmt.Printf("\n[2] 第 50 笔证据: hash=%s sig=%x…\n", short(before.Hash), before.Signature[:8])
	fmt.Printf("    版本 100 默克尔根: %s (路径长度 %d, 验证=%v)\n",
		short(rootV100), len(proofV100.Path), auditledger.VerifyProof(proofV100, rootV100))

	// 2. 对第 50 笔发起冲正：新增第 101 笔记录，历史区块不动。
	rev, err := l.AppendReversal(target, "reverse transfer #50")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\n[3] 冲正完成: 新增区块#%d (type=%s, reverses=%d)\n",
		rev.Index, rev.Payload.Type, rev.Payload.Reverses)
	fmt.Printf("    账簿长度=%d, 余额: alice=%d bob=%d\n",
		l.Len(), l.Balance("alice"), l.Balance("bob"))

	// 3. 验证原第 50 笔证据与路径证明依然有效。
	after, _ := l.GetBlock(target)
	proofV101, _ := l.Prove(target)
	rootV101, _ := l.RootAt(101)
	fmt.Printf("\n[4] 冲正后审计:\n")
	fmt.Printf("    第 50 笔未被覆写: %v (hash 仍为 %s)\n", after.Equal(before), short(after.Hash))
	fmt.Printf("    历史版本 100 证明仍有效: %v\n", auditledger.VerifyProof(proofV100, rootV100))
	fmt.Printf("    最新版本 101 证明有效:   %v (根 %s)\n",
		auditledger.VerifyProof(proofV101, rootV101), short(rootV101))
	if err := l.VerifyChain(); err != nil {
		log.Fatalf("    哈希链校验失败: %v", err)
	}
	fmt.Println("    全链哈希与签名校验: 通过")
}
