# reactive-async-store

零依赖的异步派生状态管理库（TypeScript，不使用任何响应式框架）。

## 特性

- **异步派生值**：`derived()` 由异步任务计算，状态机为 `pending / success / error`
- **代际取消**：依赖变化自动 `abort` 旧任务，旧任务的结果/错误按代际（generation）拒绝回传
- **取消不是业务错误**：`AbortError` 不会进入 `error` 状态
- **共享依赖去重**：`shared(key)` 让多处消费共享同一任务，不重复执行
- **事务提交**：`transaction()` 两阶段提交，读者（订阅者/派生任务）看不到中间态
- **可回放**：所有源单元写入记录为命令日志，`Store.replay()` 重建一致的最终状态
- **可等待**：`whenSettled()` 等待所有派生任务落定，便于测试与 SSR

## 运行

需要 Node.js ≥ 23.6（原生运行 TypeScript，无需编译步骤）。

```bash
npm test          # Node.js 内置测试运行器（node:test）
npm run sample    # 运行演示样例
```

如需静态类型检查，自行安装 `typescript` 与 `@types/node` 后执行 `npm run typecheck`
（本仓库代码只使用可擦除类型语法，`tsconfig.json` 已开启 `erasableSyntaxOnly` 约束）。

## API 摘要

| API | 说明 |
| --- | --- |
| `store.source(id, initial)` | 注册可写源单元，初始状态为 `success` |
| `store.derived(id, deps, runner)` | 注册异步派生单元；`runner(ctx, ...depValues)`，`ctx.signal` 用于取消 |
| `store.shared(key, deps, runner)` | 同一 `key` 只注册/执行一次，多处调用返回同一单元 |
| `cell.set(value)` / `store.set(cell, value)` | 写入源单元；相同值写入被忽略（不产生新代际） |
| `store.transaction(fn)` | 批量写入一次提交；`fn` 抛错则全部回滚；不支持嵌套 |
| `cell.subscribe(listener)` | 订阅状态；订阅即收到当前快照 |
| `store.whenSettled()` | 等待所有已调度/进行中的派生任务落定 |
| `store.getReplayLog()` / `Store.replay(log, build)` | 导出命令日志 / 在新仓库上回放 |
| `store.snapshot()` | 导出全部单元的状态快照 |

## 设计说明

- **代际（generation）**：每次重算 `++generation` 并 `abort` 旧任务的 `AbortController`；
  任务落定回调先比对代际号，不匹配则直接丢弃——慢的旧结果永远无法覆盖快的新结果。
- **两阶段提交**：`commit()` 先应用全部写入（此阶段无任何通知），再统一标脏派生单元、
  通知订阅者。因此通知回调里读到的永远是提交后的终态。
- **依赖签名去重**：派生单元按依赖版本号签名判断是否真的变化，事务中的无效写入、
  重复标脏都不会导致任务重复执行。
- **stale-while-revalidate**：重新进入 `pending` 时保留旧值，UI 可平滑过渡。
- **命令日志**：只记录源单元的有效写入（深拷贝）；回放时按序重放，派生状态由重算得出，
  回放产生的新日志与原日志一致，可继续回放。

## 目录结构

```
src/store.ts        # 全部实现（约 400 行，零依赖）
test/store.test.ts  # node:test 测试（11 个用例）
examples/demo.ts    # 四个场景的演示样例
```
