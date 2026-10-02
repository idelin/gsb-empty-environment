// Command demo walks through the full compliance erasure lifecycle:
// customers register, one exercises the right to be forgotten, and the
// system proves both the erasure and the integrity of everyone else.
package main

import (
	"encoding/hex"
	"fmt"
	"log"

	"erasable"
)

func short(b []byte) string {
	if len(b) > 8 {
		return hex.EncodeToString(b[:8]) + "…"
	}
	return hex.EncodeToString(b)
}

func main() {
	v, err := erasable.NewVault()
	if err != nil {
		log.Fatal(err)
	}

	customers := []struct{ id, pii string }{
		{"cust-001", "张三, 138****0001, 北京市朝阳区…"},
		{"cust-002", "李四, 139****0002, 上海市浦东新区…"},
		{"cust-003", "王五, 137****0003, 广州市天河区…"},
		{"cust-004", "赵六, 136****0004, 深圳市南山区…"},
	}
	for _, c := range customers {
		id, pii := c.id, c.pii
		idx, err := v.Insert(id, []byte(pii))
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("已登记 %s -> 叶子 #%d\n", id, idx)
	}
	fmt.Printf("\n[注销前] 树根 = %s\n", short(v.Root()))

	// cust-002 requests account deletion (right to be forgotten).
	idx, _ := v.IndexOf("cust-002")
	marker, err := v.EraseByID("cust-002")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\n>>> cust-002 申请注销，叶子 #%d 被重写为 Erase_Marker\n", idx)
	fmt.Printf("    删除签名 = %s\n", short(marker.Signature))
	fmt.Printf("    原叶子哈希 = %s (已承诺进 Marker，原文已物理清零)\n", short(marker.OriginalLeafHash))
	fmt.Printf("[注销后] 树根 = %s\n", short(v.Root()))
	fmt.Printf("    注销后读取 cust-002 明文: %v (nil = 已彻底擦除)\n", v.Get(idx))

	// Auditor verifies the erasure against the new root.
	ep, em, err := v.ProveErasure(idx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\n[审计] 擦除证明验证: %v (Marker 在树中且删除签名有效)\n",
		erasable.VerifyErasure(ep, em, v.Root(), v.PublicKey()))

	// All remaining customers' proofs still verify against the new root.
	fmt.Println("\n[存证] 其余客户证明在新根下逐一验证:")
	for _, c := range customers {
		if c.id == "cust-002" {
			continue
		}
		id := c.id
		i, _ := v.IndexOf(id)
		p, leaf, err := v.ProveRecord(i)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("    %s (叶子 #%d): 证明有效 = %v\n", id, i, p.Verify(leaf, v.Root()))
	}
}
