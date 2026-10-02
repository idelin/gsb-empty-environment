// demo 演示完整流程：t=1000 锚定根哈希 → 后续生成审计凭据 → 验证凭据。
package main

import (
	"encoding/hex"
	"fmt"
	"log"

	"anchor"
)

func main() {
	// 1. 初始化锚定器（业务时钟从 t=1000 开始）。
	key, err := anchor.GenerateKey()
	if err != nil {
		log.Fatal(err)
	}
	clock := int64(1000)
	anchorer := anchor.NewAnchorer(key, func() int64 { return clock })
	verifier := anchor.NewVerifier(anchorer.PublicKey())

	// 2. 业务数据入库，构建默克尔树。
	records := [][]byte{
		[]byte("order#1 pay 100 CNY"),
		[]byte("order#2 pay 200 CNY"),
		[]byte("order#3 refund 50 CNY"),
	}
	tree := anchor.NewMerkleTree(records)
	root := tree.Root()
	fmt.Printf("[t=%d] %d 条业务记录入库, 默克尔根 = %s\n",
		clock, len(records), hex.EncodeToString(root[:8]))

	// 3. t=1000：定时锚定 —— 根哈希 + 时间戳打包签名。
	anc, err := anchorer.AnchorRoot(tree.Root())
	if err != nil {
		log.Fatal(err)
	}
	if err := verifier.RegisterAnchor(anc); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("[t=%d] 锚定完成: seq=%d timestamp=%d sig=%s...\n",
		clock, anc.Seq, anc.Timestamp, hex.EncodeToString(anc.Signature[:8]))

	// 4. 时间推进到 t=5000，为 order#2 生成审计凭据。
	clock = 5000
	cert, err := anchor.IssueCertificate(tree, records[1], 1, anc)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("[t=%d] 为 %q 签发存证证书 (默克尔路径长度=%d)\n",
		clock, cert.Data, len(cert.Proof))

	// 5. 验证：该业务事实早在 t=1000 前已入库固定。
	if err := anchor.VerifyCertificate(anchorer.PublicKey(), cert, 1000); err != nil {
		log.Fatalf("证书验证失败: %v", err)
	}
	fmt.Println("验证通过: 数据在 t=1000 前已确定存在，无法事后伪造篡改")

	// 6. 反面演示：篡改数据后验证失败。
	cert.Data = []byte("order#2 pay 999 CNY")
	if err := anchor.VerifyCertificate(anchorer.PublicKey(), cert, 1000); err != nil {
		fmt.Printf("篡改检测: %v\n", err)
	}
}
