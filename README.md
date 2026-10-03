# Sharded Merkle Tree & Cross-Shard Proof Aggregator

大规模分布式场景下的分片状态验证器：4 个独立分片树各自维护局部 Root，
主汇总器将 4 个局部 Root 构建为顶层 Meta-Tree，产出端到端跨分片聚合证明，
任意外部验证端可离线独立校验。

## 架构

```
            Meta-Root (aggregator)
           /        \
      h(s0,s1)    h(s2,s3)
      /    \      /    \
   root0  root1 root2  root3        <- 各分片局部 Root（Meta-Tree 叶子）
                   |
              Shard-2 Tree           <- 分片内部账户状态树（动态增改）
              /        \
         alice:600   bob:650         <- 账户叶子 = H(shard|id|balance|nonce)
```

- `src/merkleTree.ts` — 动态默克尔树：SHA-256、域分离哈希（叶子 `0x00` / 节点 `0x01`）、
  支持 `append` / `update` / `getProof` / `verifyProof`，奇数层复制末节点。
- `src/shardTree.ts` — 分片树：账户状态（balance/nonce）的动态插入与更新、
  片内转账 `applyTransfer`、局部 Root 与片内包含证明。
- `src/aggregator.ts` — 主汇总器：以 4 个分片 Root 为叶子构建 Meta-Tree，
  `buildCrossShardProof` 组合「片内包含证明 + 分片位置证明」；
  `verifyCrossShardProof` 为纯函数外部验证器（只需证明本身，可选钉扎可信 Meta-Root）。

组合证明完全自包含且可 JSON 序列化，外部验证链：
账户状态 → 重算叶子哈希 → 折叠片内路径 = 分片 Root → 折叠 Meta 路径 = Meta-Root。

## 环境

- Node.js >= 22.6（使用内置类型擦除运行 TypeScript，零依赖）
- 测试使用 Node 内置 `node:test`，无需任何 npm 包

## 运行

```bash
npm test        # 16 个内置测试：树/分片/跨分片证明与篡改拒绝
npm run demo    # 验收样例：分片2转账 → 组合证明 → 外部独立校验
```

## 验收场景（examples/transferDemo.ts）

1. 分片 2 中 alice 向 bob 转账 400（alice: 1000→600, nonce+1）；
2. 分片 2 独立更新局部 Root，其余分片 Root 不变，Meta-Root 随之更新；
3. 汇总器产出 alice 的组合证明（片内路径 1 层 + Meta 路径 2 层）；
4. 外部验证端仅凭证明与可信 Meta-Root 独立校验通过；篡改余额即被拒绝。
