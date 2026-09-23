# tx-store

零依赖的事务式状态存储（TypeScript，Node.js 内置测试运行器验证，不使用任何响应式框架）。

## 运行

```sh
npm test     # node --test（Node >= 22.18，原生类型擦除，无需编译/安装）
npm run demo # 运行 examples/demo.ts 样例
```

## 语义

- **事务提交**：`store.transact(label, tx => ...)` 收集 `tx.set(path, value)` 操作，函数返回后一次性不可变应用并递增版本；函数抛错则不提交、不通知。空事务不产生版本。
- **读者隔离**：`store.getState()` 永远只返回已提交状态；事务中间态仅通过 `tx.get()` 在事务内部可见。
- **确定顺序通知**：每轮按订阅 id（订阅先后顺序）逐个通知。
- **明确轮次**：通知回调内的重入写立即提交（版本递增），但其通知进入下一轮队列，不会插入当前轮。
- **快照迭代**：每轮开始时对活跃订阅者拍快照；轮内新增/取消订阅不影响本轮迭代。自取消只影响后续轮次；已取消的订阅者不会被滞留队列复活（队列只持有提交记录，不持有订阅者引用）。
- **动态选择器**：`handle.setSelector(sel, eq?)` / `handle.setEquality(eq)` 可动态更换依赖与相等策略；基线值为“最近一次应通知的值”。
- **背压合并**：下一轮开始前若积压多个提交，合并为一轮通知，并记录版本范围证据 `getMergeEvidence()`（`{round, fromVersion, toVersion, mergedCommits}`）。
- **错误聚合**：订阅者抛错被捕获并记入 `getSubscriberErrors()`，不阻断其他订阅者。
- **循环预算**：递归更新受 `roundBudget` 限制，超限后丢弃积压并记录 `cycle-budget` 错误。
- **可回放**：所有操作记入 `getCommitLog()`，`store.replay()` 从初始状态重放，可核验无重复版本、最终状态不丢失。

## 文件

- `src/store.ts` — 核心实现（Store / Transaction / applyOp）
- `src/index.ts` — 导出入口
- `tests/store.test.ts` — 16 个 node:test 用例
- `examples/demo.ts` — 运行样例
