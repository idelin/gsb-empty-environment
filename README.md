# 备份删除证明覆盖

隐私删除作业需要证明所有包含目标数据的备份分支都已处理。写供Go备份宿主调用的证据核验包，输入不可变备份DAG、块引用集合及删除或不可解密证明，只有覆盖全部可达引用才判完成；共享块不能因一个分支删除就当作全局删除。返回未覆盖路径，用小图全遍历对照增量证明合并。

## 包结构

- `types.go`：`BlockID` / `Block` / `Branch` / `DAG` / `BlockSet` / `Path`
- `dag.go`：DAG 结构校验（缺块、重复边、环、分支头）与全路径枚举
- `proofs.go`：删除/不可解密证明、规范签名输入与 `Ed25519Authenticator`
- `verifier.go`：增量证明合并器 `Verifier` 与一次性 `FullScan` 参考实现
- `report.go`：覆盖判定、未覆盖路径报告与两种算法的交叉对照

## 覆盖语义

- 核验单元是 `(分支, 目标块)` 可达引用对；所有可达引用对都被覆盖时
  `VerifyReport.Complete` 才为真。
- `Scope == GlobalScope` 的全局证明覆盖目标块在每条分支上的引用。
- 分支作用域证明只覆盖本分支。共享块被多条分支引用时，单个分支的
  删除证明不会让其他分支的引用消失，未覆盖分支会出现在 `UncoveredPaths`。
- 不可解密证明（密钥销毁、残余密文不可恢复）与删除证明同权参与覆盖。
- 每次 `Report` 都会独立跑一次小图全遍历，结果放在 `CrossCheck`；
  两条算法结论不一致时不会判定完成。

## 调用示例

```go
dag := &netsec.DAG{ /* 不可变备份图 */ }
targets := netsec.NewBlockSet("block-X")
auth := netsec.NewEd25519Authenticator(map[string]ed25519.PublicKey{
	"backup-host": pubKey,
})

verifier, err := netsec.NewVerifier(dag, targets, auth)
if err != nil { /* DAG 结构非法 */ }

// 宿主随作业进度增量提交证明；重复提交幂等，非法/伪造证明进拒证列表。
err = verifier.Add(netsec.SignProof(netsec.Proof{
	BlockID: "block-X",
	Kind:    netsec.KindDeletion,
	Scope:   "branch-a", // 或 netsec.GlobalScope
}, "backup-host", privKey))

report := verifier.Verify()
if !report.Complete {
	for _, path := range report.UncoveredPaths {
		// path.String() 形如 "branch-b: b1->b2->block-X"
		_ = path
	}
}
```
