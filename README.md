# 分层时间感知（TTL/TTD）LRU 双重索引器

支持**绝对过期时间（TTL）**与**滑动过期时间（TTD）**的 LRU 缓存，TypeScript 实现，
仅依赖 Node.js 内置模块（>= 22.6，利用原生类型擦除直接运行 `.ts`）。

## 架构：双重索引

```
            ┌──────────────── TtlLruCache ────────────────┐
  热度层     │  Map<key, Entry>  +  双向链表 (MRU ⇄ LRU)    │  get/set O(1) 提升热度
            ├──────────────────────────────────────────────┤
  时间层     │  小顶堆（按 expiresAt 排序，堆内索引 O(log n)）│  后台定时器批量剔除死对象
            └──────────────────────────────────────────────┘
```

- **热度提升 O(1)**：`get` 命中后在单次操作内完成「摘链 → 头插」，无额外遍历。
- **批量剔除 O(k log n)**：后台清扫器每 `sweepIntervalMs` 弹出堆顶所有 `expiresAt <= now`
  的死对象；读取路径另有惰性过期判定，超时立即返回空并同步摘除。
- **时间语义**：`expiresAt = min(createdAt + ttlMs, lastTouchAt + slidingMs)`，
  滑动续期永不可突破绝对 TTL 硬上限。
- **零泄漏**：清扫定时器 `unref()` 不阻碍进程退出；`dispose()` 停表并清空全部索引。

## API

```ts
const cache = new TtlLruCache<string>({ maxSize: 1000, sweepIntervalMs: 5 });
cache.set('k', 'v', { ttlMs: 100 });              // 绝对 TTL
cache.set('h', 'v', { ttlMs: 100, slidingMs: 30 }); // 绝对 + 滑动（TTD）
cache.get('k');        // 命中：O(1) 提升热度 + 滑动续期；超时：返回 undefined 并摘除
cache.sweepExpired();  // 手动批量剔除，返回剔除数量
cache.dispose();       // 停止后台清扫器并清空缓存
```

## 运行样例

```bash
npm run demo
```

```
[t=0ms]   写入完成: size=101, 时间堆待到期=101
[t≈45ms]  高频访问后热点仍存活: key-0=value-0
[t≈45ms]  同参数未访问的对照组已死: control=undefined
[t≈45ms]  短 TTL 冷对象已超时: key-20=undefined
[t≈175ms] 超时读取立即返回空: key-0=undefined
[t≈175ms] 后台批量回收完成: size=0, 时间堆待到期=0
[done]    dispose 后清扫器存活=false，内存零泄漏
```

## 测试结果

```bash
npm test   # node --test（Node.js 内置测试运行器）
```

```
✔ 基础读写与覆盖更新
✔ LRU 淘汰：get 单次操作内 O(1) 提升热度，改变淘汰顺序
✔ 绝对 TTL：读取时判定已超时立即返回空并摘除
✔ 滑动过期（TTD）：访问续期，停止访问后到期
✔ 滑动续期不得突破绝对 TTL 硬上限
✔ 批量剔除：sweepExpired 单次调用清除全部已过期对象
✔ 验收场景：100 个对象、10~100ms 不等 TTL、高频访问、后台批量回收、零泄漏
✔ dispose 后后台清扫器停止，不再自动回收
ℹ tests 8  ℹ pass 8  ℹ fail 0
```

验收场景对应 `test/TtlLruCache.test.ts`：插入 100 个对象（TTL 10~100ms 不等，
其中 20 个热点对象附加 30ms 滑动窗口），高频访问热点对象使其续期存活、
同参数未访问的对照组 30ms 即死；全部绝对 TTL 到期后读取立即返回空，
后台清扫器批量回收至 `size === 0` 且时间堆排空，`dispose()` 后无定时器残留。

## 目录

```
src/TtlLruCache.ts        实现（双重索引 + 后台清扫器）
test/TtlLruCache.test.ts  node:test 内置测试（假时钟保证时间语义确定性）
examples/demo.ts          运行样例
```
