# scoped-resources

异步资源归属作用域（TypeScript，零依赖，仅用 Node.js 内置能力）：

- **节点订阅**：作用域树中的每个节点都可 `subscribe()`，事件只在状态**提交后**发出。
- **异步取得**：`scope.acquire()` 同步预留槽位，取得异步完成；作用域销毁后**迟到取得立即归还**。
- **依赖逆序清理**：`close()` 按依赖图逆拓扑序释放（被依赖者后释放，同级按注册逆序 LIFO）。
- **失败不中断**：单个资源清理失败不阻止其他资源；所有错误确定性地聚合为 `ScopeCloseError`（`AggregateError`）。
- **幂等销毁**：重复 `close()` 返回同一个 Promise，资源绝不重复释放。
- **子作用域**：`fork()` 创建子作用域；`borrow()` 借用祖先资源，子作用域关闭**不回收**父资源；父关闭时先关子。
- **环的确定规则**：DFS（注册序遍历、声明序沿边）发现回边即忽略并记录 `DependencyCycleError` 进聚合，每个节点仍恰好释放一次，顺序确定。
- **销毁中新增**：`acquire()` 在 closing/closed 时立即抛 `ScopeClosedError`。
- **可回放状态**：所有状态变更走 `Store.transact(ops)`，纯 reducer + 追加日志，`replay()` 可完整重建；快照深冻结，读者看不到事务中间态。
- **独立所有权账本**：`Ledger` 由运行时被动作账，测试独立验证**每资源最终恰一次释放**。
- **可控调度**：`Scheduler` 手动 `pump()` 枚举取得/借用/取消/双重关闭的交错（见 `test/scope.test.ts` 的 enumeration 测试，32 种交错全部账本平衡）。

## 运行

```bash
npm test        # node --test test/*.test.ts
npm run demo    # node examples/demo.ts
```

要求 Node ≥ 23.6（内置 TypeScript 类型擦除；无第三方依赖，无需构建）。
如需类型检查，联网后 `npm i -D typescript @types/node && npx tsc --noEmit`。

## 结构

- `src/scheduler.ts` — 可控调度器（手动 pump / auto 微任务）
- `src/ledger.ts` — 独立所有权账本（exactly-once 校验）
- `src/journal.ts` — 可回放 op 日志 + 原子提交 + 冻结快照
- `src/scope.ts` — 作用域：acquire / borrow / addDependency / fork / close
- `src/errors.ts` — 错误类型与聚合规则
- `test/` — Node 内置测试运行器
- `examples/demo.ts` — 运行样例

## 样例输出（`npm run demo`）

```
[borrow] worker borrowed: rows(select 1)
[borrow] after worker.close(), db status: owned
--- closing root ---
closed with aggregate: scope "root" closed with 1 error(s)
  - failed to release resource "root:r3": metrics sink unreachable
[late] slow-config waiter rejected: ScopeClosedError
release order: worker-pool -> flaky-metrics -> cache -> db -> slow-config(late-return)
replay matches snapshot: true
ledger: every resource released exactly once ✔
```

## 测试结果（`npm test`）

52 个测试全部通过（含 32 种调度交错枚举）：

```
ℹ tests 52
ℹ pass 52
ℹ fail 0
```
