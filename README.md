# Tiered TTL/TTD LRU Dual Indexer

分层时间感知（TTL/TTD）LRU 双重索引器，TypeScript 实现，零依赖，使用 Node.js 内置测试运行器验证。

## 架构

每个条目只有**一份**内存分配，同时挂在三个索引上：

| 索引 | 结构 | 职责 | 复杂度 |
| --- | --- | --- | --- |
| 键索引 | `Map<K, CacheNode>` | 按键查找 | O(1) |
| 热度索引 | 侵入式双向链表 | 访问热度提升（MRU 头 → LRU 尾） | O(1) |
| 到期索引 | 按 `expiresAt` 排序的小顶堆 | 最近到期对象置顶，批量剔除 | 入堆/修复 O(log n)，堆顶 O(1) |

过期模型：

- `ttlMs` — 绝对过期时间（TTL），写入时定死 deadline；
- `slidingMs` — 滑动过期时间（TTD），每次命中刷新 deadline；
- 两者可叠加，生效 deadline 取较早者。

回收策略：每次公开操作开头先执行一次 `sweep()`，把堆顶整段已过期前缀**一次性批量弹出**并从三个索引同时摘除（O(k log n)，k 为死亡对象数）；读取路径另有防御性兜底，超时对象绝不外泄。另提供 `startJanitor()` 后台定时回收（定时器已 `unref`，不阻止进程退出）与 `dispose()` 零泄漏拆除。

## 环境要求

Node.js ≥ 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译；≥ 23.6 可去掉 `--experimental-strip-types` 标志）。

## 运行

```bash
npm test        # Node.js 内置测试运行器（node:test）
npm run demo    # 运行样例
```

## 运行样例输出

```text
== insert 100 objects, TTL staggered 10ms..100ms ==
size after insert: 100
next expiry in (ms): 10.0

== hot loop: rescue the 5 soonest-to-die objects via sliding TTD ==
read expired obj-50 -> undefined
read rescued obj-1  -> { id: 1, payload: 'hot-1' }

== one explicit sweep batch-reaps everything left ==
batch-reaped dead objects: 5
size after sweep: 0
stats: { size: 0, hits: 21, misses: 1, expiredReaped: 100, lruEvicted: 0 }
integrity ok: true
after dispose -> size: 0 | keys: []
```

（95 个死对象在读取路径上已被惰性批量回收，显式 `sweep()` 再清掉剩余 5 个。）

## 测试结果

```text
✔ basic set/get/delete round-trip (0.885084ms)
✔ absolute TTL: read after deadline returns undefined and frees the entry (57.383375ms)
✔ sliding TTD: hot access renews the deadline, idleness lets it die (487.191958ms)
✔ LRU hotness: O(1) promotion reorders the list, capacity evicts coldest (0.763875ms)
✔ acceptance: 100 objects, 10..100ms TTL, hot reads, batch reap, zero leak (272.829ms)
✔ batch reaper drains a large expired prefix in one call (34.552541ms)
✔ set on an existing key refreshes value, deadline and hotness (41.8635ms)
✔ background janitor reaps during idle periods and unrefs its timer (60.2755ms)
ℹ tests 8
ℹ pass 8
ℹ fail 0
```

## API 摘要

```ts
const cache = new TieredTtlLruCache<string, V>({ maxSize: 1000, ttlMs: 60_000 });
cache.set('k', value, { ttlMs: 100, slidingMs: 50 }); // 绝对 + 滑动可叠加
cache.get('k');        // 命中即 O(1) 提升热度，并刷新滑动 deadline
cache.peek('k');       // 只读，不提升热度、不刷新 deadline
cache.sweep();         // 显式批量剔除已过期对象，返回剔除数量
cache.keys();          // 按热度从热到冷列出
cache.stats;           // { size, hits, misses, expiredReaped, lruEvicted }
cache.verifyIntegrity(); // 三索引一致性自检（测试用）
cache.dispose();       // 释放全部引用，零泄漏
```

## 文件结构

- `src/tiered-ttl-lru.ts` — 核心实现
- `test/tiered-ttl-lru.test.ts` — Node.js 内置测试（含验收边界用例）
- `examples/demo.ts` — 运行样例
