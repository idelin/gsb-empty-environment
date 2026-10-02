# Dual-Ring Hot-Standby Hash Router

网络拓扑感知的主备哈希环路由控制器（TypeScript，零依赖）。

- **双环热备**：主环（Active）承载全量流量；备环（Standby）在后台同步拓扑变更。
- **预热（Prewarm）**：备环在切换前预计算键迁移影响（vnode 级 diff + 采样键精确迁移集）。
- **原子指针翻转**：`route()` 只读取单个完整构建好的环引用，切换即一次引用交换，
  毫秒级（实测微秒级）全局一致切换，零瞬时路由错乱。
- **拓扑感知**：节点携带 `zone`（故障域）元数据，`lookupDistinct` 优先跨 zone 分散副本。

## 环境要求

Node.js >= 22.6（内置 `node:test` 与 `--experimental-strip-types`，直接运行 TypeScript，无需编译）。

## 结构

```
src/hash-ring.ts    一致性哈希环：虚拟节点、二分查找、zone 感知副本、ownership diff
src/controller.ts   双环控制器：syncStandby / prewarm / flip / failover
test/               Node.js 内置测试（node:test + assert）
demo/demo.ts        端到端验收场景演示
```

## 核心流程

```ts
const controller = new DualRingController(nodes);   // 主环承载流量
controller.route(key);                              // 热路径：读当前环引用
controller.syncStandby([{ type: "remove", nodeId }]); // 变更只进备环
const plan = controller.prewarm(sampleKeys);        // 预计算迁移影响
controller.flip();                                  // 原子指针翻转（或 failover()）
```

## 运行

```bash
npm test    # node --experimental-strip-types --test test/*.test.ts
npm run demo
```

## 运行样例（demo 实际输出）

```
=== 1. Boot: active ring serving steady traffic ===
active ring: 12 nodes, version=12, standbyReady=true
routed 200,000 keys in 100.3ms (2.0M lookups/s)

=== 2. Inject scale-in into the STANDBY ring (active untouched) ===
retiring nodes: cache-03, cache-07, cache-11
active still on version=12, standby on version=15, standbyReady=false

=== 3. Prewarm: pre-compute key-migration impact ===
migration plan:
  removed nodes      : cache-03, cache-07, cache-11
  vnode ranges moving: 480/1920 (25.00%)
  keys migrating     : 52,714/200,000 (26.36% of sampled traffic)
standbyReady=true

=== 4. Atomic pointer flip under live traffic ===
flip duration        : 0.002ms (single pointer swap)
post-flip checks     : 100,000 lookups verified
transient misrouting : 0 (zero-tolerance)
active ring now      : 9 nodes, version=15, flipCount=1

=== Result ===
OK - smooth cutover: millisecond-level flip, zero routing errors.
```

## 测试结果（node:test 实际输出）

```
ok 1 - active ring serves all traffic while standby syncs in the background
ok 2 - prewarm pre-computes the migration impact of a scale-in
ok 3 - atomic flip cuts over in milliseconds with zero transient misrouting
ok 4 - flip is rejected while the standby ring is dirty
ok 5 - failover performs the same one-click atomic switch
ok 6 - controller supports repeated sync/prewarm/flip cycles
ok 7 - emptying the standby ring is rejected
ok 8 - lookup is deterministic and hits only member nodes
ok 9 - lookup throws on an empty ring
ok 10 - load is balanced across nodes within statistical tolerance
ok 11 - removing one node moves roughly 1/N of keys and nothing else
ok 12 - lookupDistinct prefers distinct zones (topology-aware spread)
ok 13 - clone is independent from the source ring
ok 14 - diffOwnership reports exactly the changed vnode ranges
ok 15 - fnv1a is stable and well distributed over the 32-bit space
# tests 15
# pass 15
# fail 0
```
