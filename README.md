# 动态分片默克尔树与跨分片证明合并器

仅使用 Go 标准库实现的大规模分布式分片状态验证器。

## 架构

- **4 个独立分片树**（`Shard`）：各自维护账户状态与局部 Root，支持
  `Set` / `Transfer` 动态更新，更新后局部 Root 独立变化、互不影响。
- **主汇总器**（`Aggregator`）：将 4 个局部 Root 作为固定位置的叶子
  构建顶层 Meta-Tree，产出全局 Meta Root。
- **跨分片组合证明**（`CombinedProof`）：
  1. 账户在分片树内的包含证明（account → shard root）；
  2. 分片 Root 在 Meta-Tree 中的位置证明（shard root → meta root）。
  任意外部验证端仅凭 Meta Root 即可用 `VerifyCombinedProof` 独立校验。

## 哈希方案

SHA-256，域分离：叶子 `SHA256(0x00 || data)`，内部节点
`SHA256(0x01 || left || right)`；奇数节点直接提升（不复制）。

## 目录

- `merkle/merkle.go` — 默克尔树、包含证明生成与验证
- `sharded.go` — 分片、汇总器、组合证明与外部验证
- `cmd/demo/main.go` — 验收场景运行样例（分片 2 转账 + 端到端证明）

## 运行

```sh
go test ./...      # 全部测试（含验收场景 TestAcceptanceTransferInShard2）
go run ./cmd/demo  # 运行样例
```
