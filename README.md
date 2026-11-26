分布式密钥透明目录在跨多个连续纪元发生前缀分裂、邻接前缀合并及部分子树迁出迁回时，若服务端在两台审计员之间分叉纪元根，或通过把旧纪元撤销过的子树与新分裂节点拼接，即可悄无声息复活废除公钥。为Go透明客户端实现可导入的多纪元并发迁移与防分叉验证组件：支持至多256位定长位串标签、连续 `1..8` 个纪元的签名根链，域标记哈希绑定节点类型、高度、位前缀与纪元号，并支持非相邻纪元之间的 Merkle 前缀仅追加（append-only consistency）跨代证明与单调递增撤销计数器校验。新增拓扑变动约束：每个纪元允许多个互不相交前缀发生子前缀分裂（split）与兄弟合并（merge）迁移；一旦某个前缀在过往纪元被标记撤销并迁出，即便后续经由合并或拆分再次出现相同位前缀，也严禁复用任何早期纪元子树哈希，必须提供全新签发授权记录并验证合并前后子节点哈希完整性。同时验证双独立审计员签收视图的一致性：若任意审计视图揭示同一纪元号存在不同根哈希或冲突迁移历史，立即锁定并输出可独立验算的分叉欺诈证明。继续强制校验所交边界节点的极小性与唯一重构性，并用6位全展开参考树对比验证跨代Split+Merge复活阻断、双审计分叉捕获与冗余证明节点拒收。

## 组件概述（package kt，module `formal.local/w3-38e8b6c020`）

- `BitPrefix`（`bitprefix.go`）：至多 256 位定长位串标签，支持父子/兄弟/前缀关系、全序排列与规范二进制编码。
- 域标记哈希（`hash.go`）：单字节域标签 + 长度前缀编码；节点哈希绑定节点类型、高度、位前缀与纪元号，空/叶子/内部节点角色不可互换重放。
- 全展开 Merkle 前缀树（`tree.go`）：固定标签深度，包含/非包含证明可独立重算根哈希。
- 签名根链（`epoch.go`）：连续 `1..8` 纪元、Ed25519 签名、PrevRoot 链接、撤销计数器单调校验。
- 迁移与授权（`migration.go`）：split / merge / revoke-out，每纪元允许多个互不相交前缀；授权记录含 Nonce 与单调 Serial，防止重放。
- 复活阻断（`verifier.go`、`verifier_migrate.go`）：撤销子树哈希进入墓地（graveyard），任何后续复用（含经 split/merge 重新贴标签）一律拒收并硬锁定；同前缀再次出现必须提供全新签发授权；merge 校验兄弟前缀与父子哈希完整性。
- 跨代一致性（`proof.go`、`builder.go`）：支持非相邻纪元 append-only 证明；边界集强制极小性、完全覆盖与唯一重构，冗余/重叠/未知类型边界节点拒收。
- 双审计员防分叉（`auditor.go`）：同一纪元根哈希或迁移历史冲突时输出第三方可独立验算的 `ForkProof`。
- 并发安全：`Verifier` 内部 `sync.RWMutex` 保护，`go test -race` 通过。

## 快速使用

```go
verifier, _ := kt.NewVerifier(directoryPublicKey, trustedIssuers, labelDepth)
err := verifier.IngestEpoch(signedRoot, migrationLog, oldTree, newTree)
proof, _ := kt.BuildConsistencyProof(tree1, tree4, logsBetween)
err = verifier.VerifyConsistency(proof)
forkProof, err := kt.CompareAuditorViews(auditorA, auditorB, directoryPublicKey)
```

## 测试

`go test ./...`（含 `-race`）基于 6 位全展开参考树，覆盖跨代 Split+Merge 复活阻断、双审计分叉捕获、冗余证明节点拒收、授权重放、计数器单调性、纪元链边界等场景。
