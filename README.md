# shardmerkle — 动态分片默克尔树与跨分片证明合并器

大规模分布式场景下的分片状态验证器，仅使用 Go 标准库（`crypto/sha256`）。

## 架构

- **4 个独立分片树**（`merkle.ShardTree`）：每个分片独立维护账户状态，
  支持动态插入 / 更新 / 删除，局部 Root 在变更后惰性重算。
- **主汇总器 Meta-Tree**（`merkle.MetaTree`）：以 4 个分片局部 Root 为叶子
  构建顶层树，任何分片更新都会推动 Meta Root 更新。
- **跨分片聚合证明**（`merkle.CrossShardProof`）= 分片内包含证明
  （leaf → shard root）+ 分片位置证明（shard root → meta root）。
  外部验证端只需公开的 Meta Root，调用
  `merkle.VerifyCrossShardProof` 即可独立校验，无需访问任何分片状态。

哈希采用域分离（叶子 `0x00` / 内部节点 `0x01` 前缀），防止第二原像攻击；
叶子按键排序，Root 与插入顺序无关。

## 验收场景

分片 2 中的账户发起转账 → 分片 2 生成内部包含证明 → 主汇总器附加
分片 2 在 Meta-Tree 中的位置证明 → 组合证明被外部验证端独立校验通过。
对应测试：`TestCrossShardTransferProof`（`merkle/merkle_test.go`）。

## 运行

```sh
go test ./... -v   # 测试
go run ./cmd/demo  # 端到端演示
```

## 测试结果

```
=== RUN   TestShardInclusionProof        --- PASS
=== RUN   TestDynamicUpdates             --- PASS
=== RUN   TestMetaTreeTracksShardRoots   --- PASS
=== RUN   TestCrossShardTransferProof    --- PASS
=== RUN   TestCrossShardProofAllShards   --- PASS
=== RUN   TestEmptySystemDeterministic   --- PASS
PASS
ok  	shardmerkle/merkle
```

## 运行样例输出

```
=== Sharded Merkle Tree: cross-shard proof demo ===
bootstrapped 4 shards, meta root = 27b9423ea7827594...

transfer in shard 2: shard2/account003 (1203) -> shard2/account004 (1204), amount 125
shard 2 local root = 61d481a0ac21a1bd...
meta root          = 9323f3929e8aae66...

aggregate proof for shard2/account003:
  shard-local path : 3 step(s)
  meta-tree path   : 2 step(s)
  proven balance   : 1078

EXTERNAL VERIFY: PASS (proof checked against meta root only)
tampered proof rejected: PASS
```
