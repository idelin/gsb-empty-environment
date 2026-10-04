# versioned-result-cache

面向「依赖输入数组版本 + 算法参数」的昂贵计算的不可变结果缓存（TypeScript）。

## 设计

缓存键：

```
#<底层身份>[<视图形状>]@v<底层版本>|<规范化参数>
#1[Float64Array@0+24]@v2|{"scale":2}
```

- **真实底层版本**（`src/version-tracker.ts`）：TypedArray/DataView 等视图的身份与版本
  挂在底层 `ArrayBuffer` 上，而不是视图对象上。通过任何一个 `subarray` 视图修改数据并
  `tracker.mutate/touch` 后，所有共享该 buffer 的视图都看到版本推进。视图形状
  （类型 + 偏移 + 长度）参与键，因此同一 buffer 上不同范围的视图不会互相误命中。
- **参数规范化**（`src/canonicalize.ts`）：键序无关、类型敏感的稳定编码；
  函数/symbol/循环引用直接拒绝。参数变了 -> 键变了 -> 绝不误命中。
- **不可变结果**（`src/immutable.ts`）：TypedArray 结果入库存私有副本、每次发放再复制；
  普通对象结果递归深冻结。调用方改返回数组不污染缓存。
- **同键合并 + 取消安全**（`src/result-cache.ts`）：同键并发请求合并为一次计算；
  调用方通过 `AbortSignal` 退出，最后一个订阅者离开时任务被取消。任务完成时
  若已被取消（`discard-cancelled`）或输入版本已推进（`discard-stale`，旧任务晚到），
  结果一律丢弃，绝不发布半成品/过期品；被丢弃任务的等待者按当前版本自动重试，
  通常合并到已在进行的新版本任务上。
- **额度回收**：已发布结果占用 `capacity` 额度，超额按 LRU 驱逐并记 `evict`。
- **命中账本**（`src/ledger.ts`）：每次 miss/start/coalesced/hit/store/abort/
  discard/evict 都追加一条带序号的事件，可表格化输出核对。

## 使用

```ts
import { ResultCache, VersionTracker } from './src';

const tracker = new VersionTracker();
const cache = new ResultCache<Float64Array, { scale: number }, Float64Array>({
  capacity: 128,
  tracker,
  compute: async (input, params, signal) => expensiveScale(input, params.scale, signal),
});

const data = new Float64Array([1, 2, 3]);
const r1 = await cache.run(data, { scale: 2 });          // miss -> 计算 -> store
const r2 = await cache.run(data, { scale: 2 });          // hit
tracker.mutate(data, (d) => { d[0] = 100; });            // 原地修改必须上报
const r3 = await cache.run(data, { scale: 2 });          // miss（版本已推进）
console.log(cache.ledger.format());                      // 可核对的命中账本
```

取消：`cache.run(input, params, { signal })`，`AbortController.abort()` 后该调用方
收到 `AbortError`；若还有其他订阅者任务继续，否则任务取消且产出不会发布。

## 验证

`src/controlled-computer.ts` 把 compute 包成「调用入队、手动兑现」的可控计算器，
`src/verify.ts` 用它精确编排七个场景：同键合并、参数误命中防护、视图版本追踪、
旧任务晚到、取消不发布、调用方篡改返回值、LRU 额度回收，并打印每个场景的命中账本。

```sh
npm run verify        # = node scripts/build.mjs && node dist/verify.js
```

构建需要任意 TypeScript >= 5 编译器：`npm i -D typescript`，或设置
`TYPESCRIPT_DIR` 指向已有安装（`scripts/build.mjs` 也会自动探测本机编辑器内置副本）。
