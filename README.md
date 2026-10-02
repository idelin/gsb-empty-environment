# Sharded Merkle Tree with Cross-Shard Proof Aggregation

动态分片默克尔树 + 跨分片证明合并器。系统由 4 个独立分片树组成，每个分片独立维护
局部 Root；主汇总器把 4 个局部 Root 作为叶子构建顶层 Meta-Tree，并将分片内包含
证明与 Meta-Tree 位置证明合并为一份自包含、可 JSON 序列化的跨分片聚合证明，
任意外部验证端无需访问链上状态即可独立校验。

## 结构

- `src/merkle.ts` — 动态默克尔树：域分离哈希（`H(0x00||data)` / `H(0x01||l||r)`）、
  容量自动倍增、包含证明生成与纯函数式独立校验。
- `src/shard.ts` — 分片树：账户状态管理、片内转账、局部 Root、账户包含证明。
- `src/meta-tree.ts` — 顶层 Meta-Tree：叶子 i = 分片 i 的局部 Root。
- `src/aggregator.ts` — 主汇总器：同步分片 Root、合并两级证明；
  `verifyCrossShardProof` 为外部验证端使用的独立校验函数。
- `examples/transfer-demo.ts` — 验收场景：分片 2 内 carol→dave 转账 250，
  生成分片 2 内部证明 + 分片 2 在 Meta-Tree 中的位置证明，外部验证通过。
- `test/` — Node 内置测试运行器（`node:test`）测试。

## 安全性质

- 叶子/内部节点域分离，防第二原像攻击。
- 证明路径隐式编码叶子下标，校验端会重算下标并与声明的 `shardId` /
  `shardLeafIndex` 比对，防止证明被重新标号到其他分片。
- 校验端可钉住可信 `metaRoot`，过期 Root 上的证明会被拒绝。

## 运行

需要 Node ≥ 23.6（原生类型擦除）；Node 22.6+ 可加 `--experimental-strip-types`。
也可以 `npm i -D typescript @types/node && npm run typecheck` 做类型检查。

```sh
npm test    # node --test test/*.test.ts
npm run demo
```

## 测试结果

Node v23.9.0，14 个测试全部通过：

```
ℹ tests 14
ℹ pass 14
ℹ fail 0
```

演示输出（节选）：

```
=== Transfer in shard 2: carol -> dave, 250 ===
after:  carol=750 dave=550
shard 2 root changed: 32021b18971f… -> 80ad2c3adbec…
external verification (pinned to trusted meta root): PASS
verification against a stale meta root:                FAIL (expected)
```
