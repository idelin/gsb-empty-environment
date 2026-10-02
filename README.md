# SS2PL Lock Manager

严格两阶段锁（Strict 2PL）内核 + 收缩期管理器，TypeScript 实现，
仅依赖 Node.js（>= 23.6，内置 TypeScript type-stripping 与 `node:test`）。

## 核心保证

- **扩张期只加锁不释放**：事务 `ACTIVE` 期间任何锁（包括局部行锁）都
  不会被释放；`LockManager` 根本不提供单锁释放接口，唯一出口是
  提交/回滚时的一次性 `releaseAll`。
- **收缩点原子释放**：`commit` / `abort` / 崩溃恢复时，在
  `ShrinkPhaseManager` 监护下一次性释放全部锁；任何提前释放都会
  抛出 `ShrinkPhaseViolation` 并记入 `violations`。
- **可串行化 = 提交时刻时间戳序**：每次提交分配单调递增的
  `commitTs`；`ScheduleHistory.verifyCommitOrderEquivalence()` 从
  交错操作历史中推导所有冲突方向，验证其与提交时间戳序一致。
- **崩溃安全**：`crash()` 注入故障后锁与脏数据保持原样（模拟宕机），
  `recover()` 按 undo 日志逆序回滚修改并释放全部锁。
- **死锁检测**：等待图（含队列前方阻塞边）环检测，牺牲最年轻事务。

## 结构

```
src/lockManager.ts         锁表：S/X 锁、FIFO 等待队列、锁升级、死锁检测
src/monitor.ts             收缩期管理器：监护锁事件，禁止提前释放
src/transactionManager.ts  事务内核：扩张/收缩点、undo 回滚、崩溃恢复
src/schedule.ts            历史记录与提交序等价性验证
src/store.ts               内存 KV 存储
tests/                     node:test 测试（12 个用例）
examples/demo.ts           运行样例
```

## 运行

```bash
npm test    # node --test "tests/*.test.ts"
npm run demo
```
