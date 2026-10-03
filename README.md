# 动态容量感知一致性哈希（Capacity-Aware Consistent Hashing）

结合实时 CPU/内存负载的一致性哈希分发器：节点在哈希环上的虚拟节点数量随实时健康度动态伸缩；高负载节点虚拟节点收缩，流量自动向环上邻近节点分流泻洪，避免雪崩。

纯 TypeScript 实现，零第三方依赖，使用 Node.js 内置测试框架（`node:test`）。

## 结构

- `src/hash.ts` — 键到环位置的哈希（md5 前 4 字节 → uint32）
- `src/weight-policy.ts` — 容量感知策略：实时指标 → 健康度因子 → 虚拟节点权重
- `src/ring.ts` — 一致性哈希环（虚拟节点 + 二分查找顺时针路由）
- `src/distributor.ts` — 动态容量感知分发器（周期性感知、权重伸缩、环重建）
- `test/distributor.test.ts` — Node.js 内置测试
- `examples/demo.ts` — 可运行样例（模拟验收场景）

## 容量感知策略

```
effectiveLoad = max(cpu, memory)                    # 取瓶颈维度
healthFactor  = 1                            (load <= 0.5)
              = max(0.2, (1 - load) / 0.5)   (load >  0.5)
weight        = round(100 * healthFactor)           # 即虚拟节点数
```

- CPU 30% → healthFactor 1.0 → 权重 100（满配）
- CPU 90% → healthFactor 0.2 → 权重 20（收缩泻洪，仍保留地板值兜底）

节点通过 `reportMetrics()` 上报实时指标，下一个 `runCycle()` 周期重算权重并重建哈希环；热点节点虚拟节点收缩后，其键空间被环上邻居吸收。

## 运行

需要 Node.js >= 22.6（利用内置 TypeScript 类型擦除，无需编译）：

```bash
npm test        # node --experimental-strip-types --test test/*.test.ts
npm run demo    # node --experimental-strip-types examples/demo.ts
```

## 验收场景（examples/demo.ts 实测输出）

```
=== cycle 1: all nodes healthy (cpu 30%) ===
node A  cpu=30%  health=1.00  weight=100  traffic=32.1%
node B  cpu=30%  health=1.00  weight=100  traffic=33.9%
node C  cpu=30%  health=1.00  weight=100  traffic=33.9%

>>> node A cpu spikes 30% -> 90%

=== cycle 2: capacity sensor cuts A's weight 100 -> 20, traffic drains to B/C ===
node A  cpu=90%  health=0.20  weight=20   traffic=9.4%
node B  cpu=30%  health=1.00  weight=100  traffic=44.9%
node C  cpu=30%  health=1.00  weight=100  traffic=45.7%

>>> node A recovers to cpu 30%

=== cycle 3: A's weight restored to 100, traffic rebalanced ===
node A  cpu=30%  health=1.00  weight=100  traffic=32.1%
node B  cpu=30%  health=1.00  weight=100  traffic=33.9%
node C  cpu=30%  health=1.00  weight=100  traffic=33.9%
```

A 的 CPU 从 30% 骤升至 90% 后，下一个周期权重由 100 削减至 20，承载流量从 32.1% 骤降至 9.4%，泻洪流量由 B/C 吸收，系统避免雪崩。

## 测试结果

```
# tests 9
# suites 2
# pass 9
# fail 0
```

覆盖：权重策略边界（30%→100、90%→20、瓶颈维度、线性衰减）、健康节点均衡性、
路由一致性、验收场景（权重 100→20、流量骤降、流量守恒流向邻居）、
最小扰动（>90% 迁移键来自热点节点 A）、负载恢复后权重回弹。
