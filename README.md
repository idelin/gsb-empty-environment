# scoped-resource-ownership

异步资源归属作用域（TypeScript，零依赖，Node.js 内置测试运行器）。

- 节点订阅与异步资源取得；作用域树（父/子）归属管理
- 销毁按依赖图逆序清理（依赖者先释放），环按确定规则破环并聚合报告
- 清理失败不阻断其他资源，全部错误聚合为 `AggregateError`
- 重复销毁幂等（返回同一 Promise）；迟到取得立即归还
- 子作用域关闭只归还租约，绝不回收父仍借出的资源
- 可回放事务日志：读者只见已提交快照，看不到事务中间态
- 独立所有权账本：验证每资源最终恰好释放一次、无泄漏
- 可控调度器：确定性枚举取得/借用/取消/双重关闭的交错

## 运行

需要 Node.js >= 22.6（原生 TypeScript type-stripping）。本仓库在 Node v23.9.0 上验证。

```bash
# 若使用 nvm：
export PATH="$HOME/.nvm/versions/node/v23.9.0/bin:$PATH"

npm test     # 运行全部测试（node:test）
npm run demo # 运行样例 demo/main.ts
```

## 结构

- `src/scheduler.ts` — `ManualScheduler`：FIFO 任务队列 + 微任务检查点，所有异步续体经其调度，交错顺序确定；`drain()` 排空，`waitActivity()` 等待在途任务。
- `src/scope.ts` — `Scope`：`acquire`（异步取得 + 取消）、`borrow`（租约）、`link`（显式依赖边）、`createChild`、`destroy`（幂等）；`computeDisposalPlan`（Tarjan SCC + Kahn 拓扑，确定性破环）。
- `src/journal.ts` — 事务日志：状态变更全部为可序列化 Op，纯函数 `reduce` 原子提交，快照深冻结；`replay()` 可独立重建状态。
- `src/ledger.ts` — `OwnershipLedger`：以物理资源实例为键独立记账，`audit()` 校验“每资源恰好释放一次”。
- `src/errors.ts` — `ScopeDestroyedError` / `AlreadyCanceledError` / `DependencyCycleError` / `BorrowError`。

## 语义规则

**取得（acquire）**
- 注册同步进入依赖图，完成异步；`handle.promise` 交付资源值。
- 作用域 `destroying/destroyed` 后 acquire 同步抛 `ScopeDestroyedError`。
- `cancel()` 幂等性：重复取消抛 `AlreadyCanceledError`；取消后用户立即向用户结算，工厂若迟到完成，资源立即归还（dispose + 账本登记），不进入作用域。
- 作用域销毁后在途工厂完成（迟到取得）：同样立即归还，用户收到 `ScopeDestroyedError`。

**清理（destroy）**
- 幂等：重复调用返回同一 Promise。
- 顺序：先归还本作用域持有的全部租约 → 子作用域逆创建序销毁 → 等待在途 acquire 收敛 → 按依赖图逆序清理本作用域资源。
- 依赖图：`deps` 与 `link` 构成边；拓扑逆序 = 依赖者先释放；同层按注册序 LIFO（栈式）；环（含自环）按注册序破环，并聚合一个 `DependencyCycleError`（成员按 key 排序，确定）。
- 任一 destroyer 抛错：记录并继续其余资源；物理释放只登记一次。
- 全部错误（子作用域 + 清理异常 + 环 + 迟到归还异常）聚合为一个 `AggregateError` 抛出；无错误则正常完成。

**借用（borrow）**
- 沿父链查找已就绪资源，返回 `Lease`；`release()` 幂等。
- 借出方作用域销毁时自动归还其持有的全部租约。
- 子作用域销毁只清理自己拥有的资源；借来的资源本体绝不被回收。

**状态与审计**
- 每次状态变更为一次原子提交；读者持有的快照永不变化（深冻结）。
- `journal.verifyReplay()` / `replay()`：从 Op 序列确定性重建状态。
- `OwnershipLedger` 独立于作用域内部状态记账；`audit()` 发现泄漏/重复释放即抛错。

## 测试

`test/scope.test.ts`（node:test + node:assert，14 个用例）覆盖：
依赖逆序清理、清理失败聚合继续、双重销毁幂等、迟到取得立即归还、
取消与重复取消、父子借用隔离、依赖环确定性破环、销毁中/后拒绝新增、
快照隔离与日志重放、工厂失败、多级错误汇总无泄漏、
受控调度下取得/借用/取消/双重关闭的交错枚举（账本始终恰好一次释放）。
