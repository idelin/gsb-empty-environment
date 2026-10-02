# 双层层次化哈希聚合路由网关

面向海量设备的双层分片网关，TypeScript 实现，零第三方依赖，使用 Node.js 内置
`node:test` 测试（Node >= 22.6，原生类型擦除直接运行 `.ts`）。

## 架构

```
请求 { tenant, device }
        │
  第一层环 (红黑树 + 一致性哈希)
        │  hash(tenantId)  ── 租户级扩缩容 / 容灾
        ▼
  网关集群 cluster-x
        │
  第二层环 (每租户独立红黑树环)
        │  hash(deviceMac) ── 单租户扩缩容 / 容灾，互不影响
        ▼
  协议处理节点 tN-node-M
```

- `src/redBlackTree.ts` — 红黑树（插入 + `ceiling` 查找），哈希环的有序索引。
- `src/hashRing.ts` — 一致性哈希环：虚拟节点（默认每权重 160 个）落在红黑树上，
  `lookup` 顺时针取 `ceiling`，越界回绕 `minimum`；支持排除集合实现容灾跳过。
- `src/gateway.ts` — `HierarchicalGateway`：
  - 第一层：所有网关集群共享一条环，按 `hash(tenantId)` 路由；
  - 第二层：**每个租户一条独立的环**，按 `hash(deviceMac)` 路由到协议处理节点，
    因此单租户扩容只重建该租户的环，其他租户的映射分布完全不变；
  - 两层各自维护 down 集合（`setClusterHealth` / `setNodeHealth`），
    路由时沿环顺时针故障转移，恢复后映射自动还原。

## 运行

```bash
npm test          # node --test，13 个用例
npm run sample    # 运行验收样例 sample/demo.ts
```

## 运行样例输出

```
=== 1. Acceptance route: Tenant=100, Device=ABC ===
  layer 1: hash(100) -> cluster-b
  layer 2: hash(ABC) -> t100-node-3
  final: tenant=100 device=ABC cluster=cluster-b node=t100-node-3

=== 2. Determinism: same request routed 3 times ===
  attempt 1: cluster=cluster-b node=t100-node-3
  attempt 2: cluster=cluster-b node=t100-node-3
  attempt 3: cluster=cluster-b node=t100-node-3

=== 3. Single-tenant scale-out: tenant 100 gains 3 nodes ===
  tenant 100 scaled: t100-node-1..3 -> t100-node-1..6
  tenant 200 mapping unchanged: true
  tenant 100 remapped keys: 1006/2000 (~1/2 expected for 3 -> 6 nodes)

=== 4. Node failover: kill the node serving Device=ABC ===
  t100-node-3 down -> rerouted to t100-node-4 (cluster still cluster-b)
  t100-node-3 recovered -> t100-node-3

=== 5. Cluster failover: kill the cluster serving Tenant=100 ===
  cluster-b down -> layer 1 bypasses to cluster-a, node still t100-node-3
```

## 测试结果

```
✔ acceptance: Tenant=100 Device=ABC cascades through both rings
✔ scaling tenant 100 does not disturb tenant 200 hash mapping
✔ tenant scale-out keeps key movement near the consistent-hash bound
✔ node failover: dead node is skipped, mapping restored after recovery
✔ cluster failover: dead cluster is bypassed at layer 1
✔ layer-1 scale-out moves only a minority of tenants
✔ removing a cluster only remaps tenants that were on it
✔ lookup is deterministic
✔ adding one node moves only a small fraction of keys
✔ removing a node only remaps keys that lived on it
✔ exclusion set fails over to the next node on the ring
✔ ceiling returns the smallest key >= query
✔ matches a sorted-array oracle under random inserts and queries
ℹ tests 13  ℹ pass 13  ℹ fail 0
```

## 验收边界对应

| 验收点 | 实现 / 用例 |
| --- | --- |
| Tenant=100 + Device=ABC 两层级联穿透 | `gateway.route` 返回 `hops: [layer1, layer2]`，见 `test/gateway.test.ts` 验收用例 |
| 两层红黑树环 | `RedBlackTree` 驱动 `HashRing`，第一层集群环 + 每租户节点环 |
| 双层独立扩缩容容灾 | `addCluster/removeCluster/setClusterHealth` 与 `addNode/removeNode/setNodeHealth` 互不耦合 |
| 单租户扩容不影响其他租户 | 第二层为每租户独立环，`scaling tenant 100 does not disturb tenant 200 hash mapping` 用例逐键校验 |
