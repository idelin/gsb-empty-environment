# Anti-Entropy 后台全量乱序状态修复器（TypeScript）

在轻量级 Gossip 之上运行低频后台反熵协程：Gossip 负责高频率、概率性、可丢包的事件捎带传播；反熵协程负责通过 **Merkle 树按深度（BFS）分层比对**，精确定位并补齐被长尾丢包遗漏的节点状态。

## 架构

- `src/transport.ts` — 有损传输层：每个 Gossip 捎带包以固定概率丢弃（种子化 RNG，丢包可复现）。
- `src/node.ts` — 集群节点：
  - **Gossip**：周期性把最近事件捎带给随机对等节点（`gossipRound`），版本号 LWW 合并，乱序/过期事件自动丢弃。
  - **反熵协程**：`startAntiEntropy(intervalMs)` 启动低频后台循环，每拍与随机存活对等节点做一次 `syncWith`——分层哈希比对 → 精确拉取差异 key → 双向补齐。
- `src/merkle.ts` — 固定形状 Merkle 树（16 个叶子桶、分支因子 4、深度 2，形状与数据量无关，双端可比）；`diffByDepth` 自根向下只下钻哈希不一致的分支，叶子桶给出精确的差异 key 集合。
- `src/cluster.ts` — 集群编排：踢节点下线（检测者声明 dead 事件）、Gossip 轮次、反熵收敛驱动。
- `src/demo.ts` — 端到端运行样例。

## 环境要求

Node.js ≥ 23（原生 TypeScript 类型擦除，无需编译）。仓库附 `.nvmrc`：

```bash
nvm use          # 或 nvm use 23.9.0
npm run demo     # 运行样例
npm test         # Node.js 内置测试运行器
```

## 验收场景

刻意阻断 90% 的 Gossip 捎带包 → n6、n7 两个节点下线事件在部分副本上遗失 → 后台反熵启动后，通过树状分层比对精确定位到恰好这 2 个遗失的下线事件并完成补齐，最终全集群收敛到同一 Merkle 根。

## 运行样例（`npm run demo`，种子确定、输出可复现）

```
=== Phase 1: gossip with 90% piggyback packet loss ===
gossip packets: sent=12 dropped=11 delivered=1 (drop ratio 91.7%)

view of the two offline events after gossip:
  n0: n6=dead n7=alive
  n1: n6=dead n7=dead
  n2: n6=alive n7=alive
  ...
lagging replica: n2 (missed both offline events)

=== Phase 2: anti-entropy background repair ===

n2 root != n0 root -> walking the Merkle tree:
  depth 1 (root children):
    branch[0] DIFFER -> descend
    branch[1] equal (pruned)
    branch[2] equal (pruned)
    branch[3] DIFFER -> descend
  depth 2 (leaf buckets under differing branches):
    bucket[0][3] DIFFER -> keys: n6
    bucket[3][2] DIFFER -> keys: n7

n2 <- n0: located keys=[n6, n7] repaired=[n6, n7] (levels=2, hash comparisons=12, round trips=3)
  n2 now sees: n6=dead n7=dead

=== Phase 3: low-frequency background coroutine finishes the job ===
anti-entropy rounds to full convergence: 2
converged: true
  n0..n5: n6=dead n7=dead root=37207f46d52a…（全部一致）

anti-entropy totals: rounds=10 keysRepaired=3 hashComparisons=56
```

注意分层剪枝效果：定位 2 个遗失事件仅需 12 次哈希比较（全量逐叶比较需 20 次），且只传输差异 key 的状态记录。

## 测试结果（`npm test`，Node.js 内置 `node:test`）

```
✔ acceptance: 90% gossip loss, anti-entropy locates and repairs exactly the 2 missed offline events
✔ background anti-entropy coroutine converges the cluster on its own
✔ out-of-order delivery: stale events never overwrite newer state
✔ identical datasets produce identical root hashes
✔ diffByDepth returns no keys for identical trees
✔ a single divergent key is located exactly, with tree pruning
✔ a key missing on one side is located exactly
✔ empty tree vs populated tree yields every key
ℹ tests 8  ℹ pass 8  ℹ fail 0
```

验收测试（`test/anti-entropy.test.ts`）断言链：

1. 传输层丢包率 ≥ 80%（90% 阻断生效）；
2. Gossip 阶段后至少一个副本同时遗失 n6、n7 两条下线事件（长尾丢包成立）；
3. 落后副本与同步副本做一次反熵交换：Merkle 分层比对返回的差异 key **恰好** 是 `[n6, n7]`（精确定位），`repaired` 恰好为这 2 个事件，且哈希比较次数 < 20（剪枝生效）；
4. 落后副本上两个节点状态被补齐为 `dead`；
5. 后台协程持续运行后全集群收敛到同一 Merkle 根，所有存活副本均知晓两条下线事件。
