# projection-store

零依赖（不使用任何现成响应式框架）的 TypeScript 选择器投影存储。订阅者只关心选择器投影；
事务内多次变更只发布最终差量；状态操作可回放；读者永远看不到事务中间态。
使用 Node.js 内置测试运行器（`node:test`）验证，Node ≥ 23.6 原生执行 TypeScript（类型擦除）。

## 运行

```bash
npm test          # node --test test/*.test.ts
npm run example   # node examples/demo.ts
```

## 设计要点与需求映射

- **订阅者只关心选择器投影**：`subscribe(selector, listener)` 只在该选择器投影变化时触发；
  无关分支的写入既不重算也不通知（按读取路径精确失效）。
- **可配置相等关系抑制无变化通知**：选择器与订阅两处都可传 `equality`；
  节点级相等判定为“未变”时不提升版本号，下游派生选择器与订阅者全部跳过。
- **缓存不混用不同参数选择器**：`createSelectorFamily` 的缓存身份 =
  `参数 | equalityVersion | 输入依赖节点 id`，三者共同组成键，不同参数/相等版本/输入依赖绝不共槽。
- **选择器共享参数化子图**：同一家族的实例共享输入选择器节点，共享输入每次提交最多重算一次。
- **分支切换后撤销旧依赖**：`fromState` 读取经 Proxy 动态记录属性路径，每次重算整体替换依赖集，
  旧分支的依赖随之撤销。
- **事务内多次变化只发布最终差量**：`transaction(tx => ...)` 内所有写操作合并为一次提交，
  订阅者只收到一次携带最终值的通知；派发队列按订阅去重合并。
- **自定义相等函数抛错不破坏其他订阅**：每次相等调用独立 try/catch，错误计入
  `stats.equalityErrors` 并回调 `onEqualityError`；默认策略视为“已变化”继续通知，
  返回 `'suppress'` 则仅跳过该次通知，其余订阅者不受影响。
- **独立全量投影参考核验**：`verify: true` 时每次提交后绕过全部记忆缓存、从已提交状态
  独立重算每个活跃选择器的完整投影并与缓存值比对，失配计入 `stats.verificationFailures`
  并记录到 `verificationLog`；重入写与取消交错场景下同样执行（测试断言零失配）。
- **状态操作可回放**：所有写操作记入 `actionLog`，`applyActions(initial, log)` 纯函数重放，
  `store.replay()` 从初始状态重放整个日志。
- **读者不能看到事务中间态**：事务在工作副本上应用，提交前 `getState`/`get` 始终返回
  上一个已提交状态。
- **根对象换引用但投影相等不通知**：结构 diff 发现无实际差异时直接跳过提交；
  仅无关分支变化时投影路径不相交，不重算不通知。
- **可变返回值冻结或快照**：`freeze: true` 深冻结状态与每个选择器结果；
  订阅选项 `snapshot: true` 向监听器交付 `structuredClone` 的隔离副本。
- **重入写与取消交错**：监听器内的写作为新事务提交，通知排队在当前派发之后；
  派发期间取消订阅会丢弃其队列中未投递的通知；`maxFlushCommits` 防止重入死循环。
- **重算与通知数量记录**：`store.stats`（commits / notifications / recomputes / suppressed /
  equalityErrors / verificationFailures）与 `store.nodeStats(selector)`。

## API 概览

```ts
import { createStore, applyActions } from './src/store.ts';
import { fromState, createSelector, createSelectorFamily } from './src/selector.ts';

const store = createStore(initial, { freeze: true, verify: true });
const todos = fromState((s: State) => s.todos);
const openCount = createSelector([todos], (all) => /* ... */ 0);
const byId = createSelectorFamily((id: string) =>
  createSelector([todos], (all) => all[id], { equality, equalityVersion: 'v1' }));

const unsub = store.subscribe(openCount, (v, prev) => {}, { equality, snapshot: true });
store.transaction((tx) => { tx.set(['a'], 1); tx.update(['b'], (b) => b + 1); tx.replace(next); });
store.replay();                 // 从初始状态重放 actionLog
applyActions(initial, store.actionLog); // 纯函数重放
```

## 运行样例输出（`npm run example`）

```
== subscriptions ==

== one transaction, three mutations, one notification ==
openCount: 2 -> 0
(reentrant) all done -> switching filter to "open"
filter: all -> open

== equality suppression: same title, new object ==

== root replaced by an equal projection: no notifications ==

== replaying the action log reproduces the state ==
replay matches: true

== stats (recomputes / notifications are recorded) ==
{
  commits: 2,
  notifications: 3,
  recomputes: 10,
  suppressed: 1,
  equalityErrors: 0,
  verificationFailures: 0
}
openCount recomputes: 2
verification failures: 0
```

## 测试结果（`npm test`，Node v23.9.0）

19 个测试全部通过，覆盖上述全部需求：

```
✔ subscribers only observe their selector projection
✔ configurable equality suppresses no-change notifications
✔ parameterized caches are never mixed across parameters
✔ family entries share the parameterized subgraph (inputs recompute once)
✔ cache identity = params + equality version + input dependencies
✔ branch switch revokes stale dependencies
✔ equal projection after recompute does not propagate downstream
✔ a throwing equality function does not break other subscribers
✔ equality error policy 'suppress' skips that notification only
✔ reentrant writes from listeners commit as new transactions and stay consistent
✔ unsubscribing during dispatch drops queued notifications (cancellation interleaving)
✔ reentrant commit loop is bounded by maxFlushCommits
✔ independent verification catches nothing in normal operation and counts recomputes
✔ multiple mutations in one transaction publish only the final diff
✔ readers never see intermediate transaction state
✔ replacing the root with an equal projection does not notify
✔ state operations are replayable from the action log
✔ freeze option protects mutable projections
✔ snapshot option delivers an isolated copy to listeners
ℹ tests 19
ℹ pass 19
ℹ fail 0
```

## 文件结构

- `src/paths.ts` — 路径工具：不可变更新、结构 diff、读取路径追踪（Proxy）、深冻结
- `src/selector.ts` — 选择器图：`fromState` / `createSelector` / `createSelectorFamily`
- `src/store.ts` — Store：事务、快照隔离、订阅派发、重入处理、回放、独立核验、统计
- `test/` — `node:test` 测试（19 个）
- `examples/demo.ts` — 可运行样例
