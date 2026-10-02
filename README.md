# 可擦除合规数据脱敏证明器 (erasable)

满足"数据被遗忘权"的可验证擦除存证系统。客户记录以 Merkle 树叶子形式承诺上链式存证；
客户注销时，系统将对应叶子**原地重写**为携带删除签名的 `Erase_Marker` 哈希，树根平滑演进，
其余客户的存证路径完全不受影响；被删明文被**物理清零**（原地覆写为零字节）。

## 设计

- **承诺结构**：叶子 `H(0x00 || data)`，内部节点 `H(0x01 || left || right)`，奇数节点向上晋升。
- **删除签名**：合规官 ECDSA P-256 私钥对 `"ERASABLE-VAULT-ERASE-v1" || index || originalLeafHash`
  的 SHA-256 摘要签名（64 字节 `r||s`），审计方凭公钥即可验证删除经过授权。
- **Erase_Marker**：`H(0xE1 || "ERASED" || index || originalLeafHash || signature)`，
  替换原叶子写入树中——既承诺了"这里曾有记录"，又不泄露任何原文。
- **物理擦除**：`Erase` 在重写叶子后对明文字节切片原地清零（`secureZero` + `runtime.KeepAlive`
  防止编译器优化掉写零），并从存证库中移除。
- **可验证性**：
  - 存活记录：`ProveRecord` 生成 Merkle 路径，`proof.Verify(leaf, root)` 对新根验证；
  - 已删记录：`ProveErasure` + `VerifyErasure` 同时校验 Marker 在树中且删除签名有效。

## 运行

```sh
go test -v ./...   # 测试
go run ./cmd/demo  # 演示完整注销流程
```

## 文件

- `merkle.go` — Merkle 树、包含证明（支持奇数晋升与叶子原地重写）
- `vault.go` — 存证库：登记、可验证擦除、擦除证明、物理清零
- `cmd/demo/main.go` — 端到端演示
