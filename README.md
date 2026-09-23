# tx-store

事务化版本状态存储。零依赖 TypeScript（Node.js 原生类型擦除直接运行），
不借助任何现成响应式框架，使用 Node.js 内置测试运行器验证。

## 核心保证

- **原子事务**：`commit(label, updater)` 在 `structuredClone` 出的草稿上执行，
  全部成功才一次性提交并递增版本；updater 抛错则整体回滚——版本不变、无通知。
- **读者隔离**：`getState()` 永远只返回已提交状态（深冻结），
  事务进行中读到的仍是上一个已提交状态，绝无中间态。
- **确定性通知顺序**：每轮按订阅 id 升序遍历订阅者快照。
- **轮次语义**：通知回调内再次 `commit` 只入队，进入下一轮；
  通知期间新增/取消订阅不影响当前轮快照迭代；自取消只影响后续轮。
- **动态订阅**：`setSelector` / `setEquality` 随时更换依赖与相等策略，
  下一轮生效；换选择器时用当前已提交状态重定基线，避免伪通知。
- **背压合并证据**：积存的多个已提交版本合并为一轮通知，
  `Notification` 携带 `fromVersion` / `toVersion` / `mergedCommits` 版本范围证据；
  `store.pendingCount` 可观察积存深度。
- **错误聚合**：订阅者抛错不阻断其他订阅者，全部通知完成后抛出
  `NotificationAggregateError`（或交给 `onNotificationError` 回调）；
  抛错的订阅者基线已推进，不会被无限重试。
- **循环预算**：递归更新超过 `maxRounds` 轮抛出 `LoopBudgetExceededError`，
  附未投递版本范围证据，队列保留可继续 `flush()`。
- **可回放**：`opLog()` 记录全部已提交操作，`replay()` 从初始状态重建最终状态。

## 运行

```sh
npm test    # node --test test/*.test.ts（Node >= 23.6，原生类型擦除）
npm run demo
```

## 快速上手

```ts
import { Store } from './src/store.ts';

const store = new Store({ initialState: { count: 0 }, maxRounds: 32 });

const sub = store.subscribe(
  (s) => s.count,                    // 选择器：声明依赖
  (n) => console.log(n.round, n.version, n.value),
  { equality: Object.is },           // 相等策略（可省略）
);

store.commit('inc', (d) => void (d.count += 1)); // 提交后按序通知
sub.setEquality((a, b) => a === b);  // 动态更换相等策略
sub.unsubscribe();                   // 只影响后续轮次
```

背压合并（手动 flush）：

```ts
const batch = new Store({ initialState: { n: 0 }, autoFlush: false });
batch.subscribe((s) => s.n, (n) => {
  console.log(n.fromVersion, n.toVersion, n.mergedCommits); // 1 5 5
});
for (let i = 1; i <= 5; i++) batch.commit((d) => void (d.n = i));
batch.flush(); // 5 个提交合并为一轮一次通知，只投递最新值
```

## 测试结构

- `test/store.test.ts` — 逐条需求的单元测试（顺序、轮次、快照迭代、
  动态选择器/相等策略、原子回滚、读者隔离、错误聚合、背压证据、
  循环预算、回放、深冻结）。
- `test/exploration.test.ts` — 状态探索核验：120 个确定性种子 × 120 个随机操作
  （提交/失败事务/订阅/取消/换选择器/换相等策略/重入写/随机抛错），
  全程断言：版本严格递增无重复、通知只引用已提交版本、同轮同订阅者不重复、
  取消后不被旧队列复活、无丢最终状态、回放一致。
