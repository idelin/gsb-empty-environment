// 运行样例：演示 t=1000 锚定根哈希，t=2000 出示并校验审计凭据。
package main

import (
	"encoding/hex"
	"fmt"
	"log"

	"merkleanchor"
)

func main() {
	// ---- t=1000：业务数据入库，锚定器定时提交默克尔根 ----
	records := [][]byte{
		[]byte("tx-001: alice pays bob 10"),
		[]byte("tx-002: carol pays dave 3"),
		[]byte("tx-003: erin pays frank 7"),
	}
	leaves := make([][32]byte, len(records))
	for i, r := range records {
		leaves[i] = merkleanchor.HashLeaf(r)
	}
	root := merkleanchor.MerkleRoot(leaves)

	anchorService, err := merkleanchor.NewAnchorer("biz-ledger")
	if err != nil {
		log.Fatal(err)
	}
	anchorService.Now = func() int64 { return 1000 } // 演示：固定时钟在 t=1000
	anchor, err := anchorService.Anchor(root)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("[t=1000] 锚定完成: seq=%d root=%s...\n",
		anchor.Seq, hex.EncodeToString(anchor.Root[:8]))
	fmt.Printf("         签名=%x...\n", anchor.Signature[:16])

	// ---- t=2000：审计方要求证明 tx-002 的存在时间 ----
	cert, err := merkleanchor.IssueCertificate(records[1], leaves, anchor)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("[t=2000] 已为 %q 签发存证证书（默克尔路径 %d 步）\n",
		records[1], len(cert.Proof))

	verifier := merkleanchor.NewVerifier(anchorService.PublicKey(), "biz-ledger", 60)
	existedBefore, err := merkleanchor.VerifyCertificate(cert, records[1], verifier, 2000)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("[t=2000] 证书校验通过：该业务事实必然在 t=%d 之前已入库固定\n", existedBefore)

	// ---- 攻击面演示 ----
	if _, err := merkleanchor.IssueCertificate([]byte("tx-004: forged"), leaves, anchor); err != nil {
		fmt.Printf("[攻击] 事后伪造数据签发证书被拒绝: %v\n", err)
	}
	if _, err := merkleanchor.VerifyCertificate(cert, records[1], verifier, 2000); err != nil {
		fmt.Printf("[攻击] 重放同一张证书被拒绝: %v\n", err)
	}
}
