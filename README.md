# snapdelta — 增量快照校验工具

接收端只保存**旧根 + 版本号**，即可验证一个声明的差量（新增 / 删除 /
继承块）是否精确产生声明的新根。

## 模型

- **键空间**：有限有序 ID，`uint16`（0..65535），深度 16 的稀疏 Merkle 树。
- **Delta**：有序操作序列（`OpPut` / `OpDelete`，重复键按序生效、后者为准）
  加上声明的新根。
- **Proof**：绑定 `OldVersion`/`NewVersion` 与键范围 `[RangeStart, RangeEnd]`；
  携带触及键的旧值（`OldEntries`）和继承块（`Nodes`，未触及子树的哈希，
  按 `(Depth, Index)` 规范序排列）。

## 安全性质（`Verify` 强制）

- **不能遗漏删除**：重放声明的操作必须精确得到声明的新根，少一个删除
  （或把删除换成等值 put）都会导致新根不匹配。
- **不能偷换未声明块**：未触及子树由继承块哈希或规范空哈希钉死，
  篡改 / 对调 / 夹带证明节点都会使旧根重建失败或被拒绝。
- **版本与范围绑定**：`proof.OldVersion` 必须等于接收端当前版本，
  `NewVersion` 必须严格递增；所有操作键必须落在证明范围内。

## 用法

```go
delta, proof, newMap, _ := snapdelta.BuildDelta(oldMap, version, ops, 0, 65535)
newRoot, newVersion, err := snapdelta.Verify(oldRoot, version, delta, proof)
// newRoot == snapdelta.FullRoot(newMap)（全量重建参考实现）
```

运行演示（随机生成 5 跳跨版本链并逐跳校验）：

```sh
go run ./cmd/snapverify [seed]
```

## 测试

```sh
go test ./...
```

覆盖：空差量、重复键操作、证明节点顺序（乱序 / 重复 / 多余节点拒绝）、
跨版本拼接、遗漏删除、偷换继承块、旧值伪造、范围与版本绑定、随机回环
对照全量重建。
