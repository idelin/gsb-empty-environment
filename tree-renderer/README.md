# tree-renderer — 两阶段（准备/提交）树渲染器

- `renderer.ts` — TypeScript 源（交付物）。
- `renderer.mjs` — `renderer.ts` 的类型擦除孪生，逻辑逐行一致；本环境无 tsc/网络，用它跑实际校验。
- `verify.mjs` — 显式步进器重放交错更新的 8 个场景。

## 运行

```sh
node verify.mjs
```

## 核心不变量

1. 只有“最新且完整”的准备版本可提交，过期/未完成提交抛 `StaleCommitError`。
2. 准备被更新版本取代时，其已建订阅立即清理（`Sub.dispose` 带恰一次护栏，重复清理计 `doubleDispose` 并抛错）。
3. 准备阶段只写 WIP 树，可见树仅在 `commit` 内被 reconcile 一次性替换。
4. `unmount` 幂等；可见树与在途准备的订阅各自恰清理一次（`disposeCount === 1` 可核对）。

## 机制

- `Stepper` 把每个 VNode 的渲染作为一个工作单元入队，`step(n)` / `runUntilIdle()` 显式驱动，测试可精确交错“调度/执行/提交”。
- 准备期间创建的订阅先挂在 prepare 的 `bag` 上；提交时 reconcile 收编存活订阅、清理被替换节点的订阅；被取代时整袋清理。
- `VirtualHost.stats` 记账：`createdSubs / disposedSubs / nodesCreated / nodesDisposed / doubleDispose`，场景结束核对账本平衡。
