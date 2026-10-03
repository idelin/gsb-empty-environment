# Tombstone GC Coordinator (TypeScript)

因果历史修剪与垃圾回收协调器：清理 CRDT 长周期运行积累的已删除墓碑（tombstone），
通过**分布式因果稳定水位（Causal Stability Frontier）**识别已被全网所有节点感知的墓碑并物理释放。

## 核心设计

- `src/store.ts` — CRDT 存储：对象条目携带值、墓碑标记与删除点 `Dot{nodeId, clock}`，并做字节级内存记账。
- `src/clockMatrix.ts` — 时钟矩阵：每个节点通过 gossip 收集「每个节点所知的全网时钟向量」，
  因果稳定水位 = 矩阵按列取**最小值**（即全网所有节点都已越过该时钟）。
- `src/coordinator.ts` — `TombstoneGcCoordinator`：扫描本地墓碑，凡 `frontier[dot.nodeId] >= dot.clock`
  即该删除事件已被全网因果感知，物理 `purge` 释放内存；否则保留（安全）。
- `src/node.ts` / `src/cluster.ts` — 集群节点与 gossip 模拟（全量知识向量交换、收敛检测）。

## 运行

要求 Node.js >= 23.6（原生 TypeScript 类型擦除运行，无需编译）。

```bash
npm test    # Node.js 内置测试运行器
npm run demo
```

## 验收场景（test/tombstone-gc.test.ts）

5 节点集群（A–E）：写入 2000 个对象 → 在 T=10..49 删除 1940 个（生成墓碑）→
所有节点越过 T=50 并 gossip 收敛 → 追加 10 个 T>50 的新墓碑（必须保留）→
节点 A/B 运行协调器：

- A/B 的因果稳定水位对所有节点 > 50；
- T<50 的 1940 个墓碑被全部物理回收，T>50 的 10 个墓碑保留；
- 内存 `172200 → 9240` 字节，下降 **94.6%**（>= 90%）；
- 安全性：某节点滞后未越过 T=50 时，回收数为 0；
- 幂等性：第二次 GC 回收数为 0。

## 测试结果

```
▶ ClockMatrix causal stability frontier
  ✔ computes the component-wise minimum across all known node vectors
▶ TombstoneGcCoordinator acceptance: 5-node cluster, frontier T=50
  ✔ physically reclaims every tombstone with T<50 on A and B, dropping memory >=90%
  ✔ reclaims nothing when a lagging node has not passed T=50 (safety)
  ✔ is idempotent: a second GC pass reclaims nothing
ℹ tests 4  ℹ pass 4  ℹ fail 0
```

## 运行样例（npm run demo）

```
=== Phase 3: every node advances past T=50, gossip to converge ===
gossip converged after 1 round(s)

Causal Stability Frontier as seen by A and B:
  node A: { A:788 B:788 C:788 D:788 E:788 }
  node B: { A:788 B:788 C:788 D:788 E:788 }

=== Phase 5: coordinated Tombstone GC on nodes A and B ===
  node A: scanned=1950 reclaimed=1940 bytes=172200 -> 9240 (-94.6%)
  node B: scanned=1950 reclaimed=1940 bytes=172200 -> 9240 (-94.6%)

Other nodes still hold their tombstones until they run the coordinator:
  node C: tombstones=1950 bytes=172200
  ...
```
