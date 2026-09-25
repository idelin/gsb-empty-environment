# font-measure-cache

代际化字体测量缓存：缓存字体度量、字形簇与样式组合测量；字体热更新原子切换缓存代际；异步旧回包精确失效；同键任务共享、按订阅者计数取消；预估/最终测量分通道发布；布局反馈有界。全部核心计算基于夹具（fixture）数据，不依赖浏览器布局引擎。

## 运行环境

- Node.js >= 23.6（原生 TypeScript 类型擦除 + 内置 `node:test`，零依赖）
- 运行：`node --test test/*.test.ts` 或 `npm test`；样例：`node sample/demo.ts` 或 `npm run sample`

## 结构

- `src/cache.ts` — `GenerationCache`：LRU + 字节额度（按条目实际大小计费）；借用（borrow）中的条目不可驱逐；`swapGeneration()` 原子切换代际，旧代际写入被精确拒绝
- `src/scheduler.ts` — `ControllableScheduler`：手动泵驱动的确定性调度器，测试用它复现旧回包、乱序字体加载等时序
- `src/measurer.ts` — `FontMeasurer` / `FontRegistry`：三层缓存（字体度量 / 字形簇 / 测量结果）共享一个代际；同键合并的在途任务按订阅者计数，最后一个订阅者离开才取消；每次 await 后重新校验字体版本，晚到的旧版本回包被丢弃并按新版本重试
- `src/layout.ts` — `LayoutEngine`：预估布局同步发布、最终布局异步发布；布局反馈按轮次上限（默认 8 轮）截断且同键去重，不会形成无界循环
- `src/fixtures.ts` — 夹具字体度量（Serif v1/v2、Mono v1、估算字体）与夹具节点树
- `test/` — 22 个用例；`sample/demo.ts` — 端到端样例

## 关键语义

- 缓存键 = 文本 + 样式（含 `letterSpacing`）+ 字体版本：相同文本不同字距不共享结果
- 字体热更新：`FontRegistry.hotReload` 同步触发三层缓存的代际切换，单次同步完成、对外原子
- 旧回包：任务在途期间版本前移时，晚到的字体回包不入缓存，测量结果丢弃并按当前版本重算；`measureCache.set` 的代际检查兜底，旧代际写入直接失败
- 取消：取消一个订阅者不影响其他等待者；引用计数归零才取消共享任务
- 字节额度：按 `sizeof*` 计算的实际条目大小扣减；`checkConservation()` 校验账目守恒；被借用条目可把用量顶过额度，但账目始终精确，释放后恢复驱逐

## 测试结果

```
✔ GenerationCache (197ms)
✔ LayoutEngine (424ms)
✔ FontMeasurer (577ms)
✔ ControllableScheduler (378ms)
ℹ tests 22
ℹ suites 4
ℹ pass 22
ℹ fail 0
```

覆盖：字节额度守恒、驱逐借用、代际原子切换、旧回包拒绝、同键合并、取消语义、字距隔离、晚到字体版本校验、预估/最终分通道、反馈去重收敛、恶意反馈有界。

## 样例输出（`node sample/demo.ts`）

```
=== 1. Estimate and final layout are published separately ===
[estimate] round=0
root: 120.0x55.2 lines=3
  t1: 86.4x19.2 lines=1
  ...
[final] round=2
root: 120.0x52.4 lines=3
  t1: 68.0x19.2 lines=1
  ...
=== 5. Font hot update mid-flight: stale reply discarded, atomic switch ===
cache generation after hot reload: 1
stale replies discarded: 1
"abcd efgh" measures 81.6px with Serif v2 (was 68px on v1)
```
