# Anti-Entropy Merkle Repairer (TypeScript)

概率性长尾丢包补偿机制：在轻量级 Gossip 之上运行低频后台反熵（Anti-Entropy）
协程，通过 Merkle 树按深度遍历比对，精确定位并补齐被丢包遗漏的节点状态。

## 架构

- `src/state.ts` — 成员状态表（`StateRecord` + incarnation 版本号，Last-Writer-Wins 合并）。
- `src/merkle.ts` — Merkle 树：key = `sha256(nodeId)`，取前 4 个十六进制位构成
  16 叉、深 4 层的树；内部节点存子节点哈希，用于分层剪枝比对。
- `src/network.ts` — 有损网络：每个 Gossip 捎带包以 `dropRate` 概率被丢弃
  （注入种子化 RNG，仿真可复现）。
- `src/node.ts` — `ClusterNode`：
  - `gossipTick()`：轻量 Gossip，向随机节点捎带最近事件缓冲区；
  - `antiEntropyTick()` / `syncWith()`：后台反熵，深度优先遍历双方 Merkle 树，
    哈希相同的子树一次比较即剪枝，只有分叉的叶子桶才交换真实记录。
- `src/simulation.ts` — 确定性内存集群仿真（同步驱动 gossip / anti-entropy 轮次）。
- `test/anti-entropy.test.ts` — Node.js 内置测试器（`node:test`）验收用例。
- `examples/demo.ts` — 可运行的验收场景样例。

## 环境要求

Node.js >= 22.6（推荐 >= 23.6，原生支持 TypeScript 类型剥离，无需编译步骤）。

```bash
npm test    # 运行内置测试
npm run demo  # 运行验收场景样例
```

## 验收场景

1. 6 节点集群初始全量收敛（全部 UP）；
2. `node-3`、`node-5` 下线，DOWN 事件依靠 Gossip 捎带传播；
3. 网络刻意丢弃 90% 的 Gossip 捎带包 → 部分节点长尾遗漏这 2 个下线事件；
4. 启动后台反熵协程 → Merkle 树分层比对，精确定位遗失的 2 个节点下线事件并补齐；
5. 全集群收敛，所有节点视图一致。

## 运行样例（`npm run demo` 实际输出）

```text
=== 1. bootstrap: 6 nodes, all UP, fully converged ===
converged: true

=== 2. failures: node-3 and node-5 go DOWN ===
node-3 marked DOWN (incarnation bumped)
node-5 marked DOWN (incarnation bumped)

=== 3. gossip phase: 12 rounds, 90% of piggyback packets dropped ===
gossip packets: sent=39 delivered=3 dropped=36 (92.3% loss)
  node-0 still missing: [node-3, node-5]
  node-1 still missing: [node-5]
  node-2 still missing: [node-3, node-5]
  node-4 still missing: [node-3]
  node-5 still missing: [node-3]
converged after gossip alone: false

=== 4. anti-entropy phase: background Merkle-tree repair starts ===
  round 1: node-0 <- node-1 repaired [node-3] (hash comparisons: 65, records pulled: 1)
  round 1: node-2 <- node-5 repaired [node-5] (hash comparisons: 65, records pulled: 1)
  round 1: node-4 <- node-1 repaired [node-3] (hash comparisons: 65, records pulled: 1)
  round 1: node-5 <- node-1 repaired [node-3] (hash comparisons: 65, records pulled: 1)
  round 2: node-0 <- node-4 repaired [node-5] (hash comparisons: 65, records pulled: 1)
  round 2: node-2 <- node-5 repaired [node-3] (hash comparisons: 65, records pulled: 1)

=== 5. result ===
converged after 2 anti-entropy round(s): true
  node-3 status across cluster: [DOWN, DOWN, DOWN, DOWN, DOWN, DOWN]
  node-5 status across cluster: [DOWN, DOWN, DOWN, DOWN, DOWN, DOWN]
```

## 测试结果（`npm test` 实际输出）

```text
✔ merkle tree: identical tables yield identical root hashes
✔ merkle tree: a single diverging record changes the root and localizes to one branch per level
✔ gossip under 90% packet loss leaves long-tail gaps
✔ anti-entropy precisely locates and repairs the 2 missed DOWN events
✔ anti-entropy uses tree pruning: converged peers cost 1 hash comparison and 0 records
✔ anti-entropy transfers records only for diverging leaf buckets
ℹ tests 6
ℹ pass 6
ℹ fail 0
```

测试要点：

- **长尾丢包**：90% 丢包率下断言丢包比例 ≥ 75%，且至少一个节点恰好遗漏
  `[node-3, node-5]` 两个 DOWN 事件；
- **精确修复**：反熵启动后，受害节点被修复的 key 集合断言为**恰好**
  `{node-3, node-5}`，不多不少，且集群在有限轮次内收敛；
- **树剪枝效率**：已收敛的节点对之间同步仅需 1 次根哈希比较、0 条记录传输；
  单条记录分叉时树遍历比较次数 ≤ 1 + 16×4（远小于全表扫描）。
