# Version Vector & 最终一致同步器

分布式键值存储的动态版本向量合并内核（TypeScript 实现）。
处理多副本并发离散写入：客户端提交新数据时出示 **Context**（版本向量），
合并时依据版本向量偏序执行 **快速快照覆盖** 或标记 **分支分叉（Sibling）**。

## 结构

- `src/version-vector.ts` — 不可变版本向量：`tick` / `merge` / `compare`（equal · before · after · concurrent）
- `src/replica.ts` — 副本节点：`get` 返回 siblings + context；`put` 依据 context 决定覆盖或分叉；`resolve` 供上层业务收敛冲突
- `src/synchronizer.ts` — 最终一致同步器：`mergeSiblings` 丢弃被支配分支、保留并发分支；`syncOneWay` / `syncBidirectional`
- `demo/scenario.ts` — 验收场景演示
- `test/` — Node.js 内置 `node:test` 测试（13 例）

## 运行

要求 Node ≥ 23.6（原生 TypeScript type-stripping，无需编译）：

```bash
npm test        # node --test "test/*.test.ts"
npm run demo    # node demo/scenario.ts
```

## 验收边界

1. 客户端从副本1读取 Key（空 context）写入 → `(R1:1)`
2. 携带 context 的覆盖写入 → `(R1:2)`（fast-overwrite）
3. 离线客户端持过期 context 在副本2写入 → `(R1:1, R2:1)`
4. 合并器判定 `(R1:2)` 与 `(R1:1, R2:1)` 并发 → 两版本保留为同名分支（sibling-fork），等待上层业务 `resolve` 解决
