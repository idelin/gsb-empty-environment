# OCC Engine — 无阻塞自旋乐观并发控制引擎

TypeScript 实现的经典三阶段 OCC（Optimistic Concurrency Control）执行内核，
使用 Node.js 内置测试运行器（`node:test`）验证，零第三方依赖。

## 核心设计

经典 OCC 三阶段（`src/occ.ts`）：

1. **读取阶段（Read）**：`OccTransaction` 在私有 `ReadSet`（key → 读取时版本）与
   `WriteSet`（key → 新值）上工作；写操作只进缓冲区，不触碰共享存储。
2. **验证阶段（Validate）**：`commit()` 时逐个校验 `ReadSet` 中键的当前版本是否仍等于
   读取时版本——即检测 ReadSet 是否被其他已提交事务修改；任一版本过期即中止。
3. **写入阶段（Write）**：验证通过则将 `WriteSet` 一次性批量应用到存储，所有键共享
   同一个全局提交版本号（原子批量提交）。

**无阻塞自旋**：验证 + 写入是纯同步临界区（JS run-to-completion 保证原子性），
不持有任何锁、不等待其他事务；冲突事务由 `runOptimistic()` 以自旋方式重试
（前 `spinBudget` 次紧循环立即重试，之后让出事件循环并可加退避），全程无阻塞等待。

**干净回滚**：写操作在提交前始终停留在事务私有缓冲区，中止时直接丢弃，
存储中绝不会出现半提交状态。

对照组：`src/lock.ts` 提供粗粒度两阶段锁（全局公平 FIFO 互斥锁，持锁到提交）基线。

## 运行要求

Node.js ≥ 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译）。
本仓库在 Node v25.5.0 上验证。

```bash
npm test    # node --test "test/*.test.ts"
npm run demo
```

## 文件结构

- `src/occ.ts` — OCC 引擎：`OccStore` / `OccTransaction` / `runOptimistic` 自旋重试执行器
- `src/lock.ts` — 锁方案基线：`LockStore`（全局互斥锁 + 等锁统计）
- `test/occ.test.ts` — 11 个 Node 内置测试用例
- `examples/demo.ts` — 三个验收场景的运行样例

## 测试结果（实测）

```
✔ 读取阶段：read 记录 ReadSet 版本，write 仅缓冲到 WriteSet
✔ 写入阶段：验证通过后 WriteSet 一次性批量提交
✔ 验证阶段：ReadSet 版本被其他已提交事务修改时精准检出并干净回滚
✔ 验证阶段无假阳性：ReadSet 之外的键被修改不影响提交
✔ read-your-own-writes：读取阶段能读到自己缓冲的写
✔ 重叠写入：并发增量只有一个先提交，另一个验证失败并干净回滚
    [hot-key] commits=1280 aborts=224 conflictRate=14.9%
✔ 高并发热点键：无丢失更新，冲突率被统计
    [read-heavy] OCC 49829 tx/s vs Lock 509 tx/s (speedup x97.9)
✔ 读多写少场景：OCC 吞吐大幅领先锁方案
✔ 自旋重试执行器：冲突后自动重试并最终成功
✔ 重试耗尽：达到 maxRetries 后抛出 OccRetryExhaustedError
✔ 已结束事务拒绝继续操作
ℹ tests 11  pass 11  fail 0
```

## 运行样例输出（`npm run demo`）

```
=== 场景 1：高并发热点键读写（64 工作线程 x 25 次增量，8 个热点键）===
  提交事务: 1600  中止事务: 8642  冲突率: 84.4%
  计数器总和: 1600（期望 1600，无丢失更新）
  吞吐: 99166 tx/s

=== 场景 2：读多写少（90% 读 / 10% 写，300 并发事务）OCC vs 锁 ===
  OCC : 0.3 ms, 874105 tx/s, 冲突率 0.0%
  锁  : 5.3 ms, 56716 tx/s, 等锁次数 299
  OCC 加速比: x15.4

=== 场景 3：重叠写入 -> 版本过期验证 -> 干净回滚 ===
  Alice 提交: true  -> balance = 70
  Bob   提交: false（ReadSet 中 balance@v1 已被 Alice 的提交改为 v2）
  Bob 状态: aborted，回滚后 balance 仍为 70（未被覆盖写）
```

## 验收边界覆盖

| 验收项 | 实现 / 验证 |
| --- | --- |
| 高并发热点键读写 | `test/occ.test.ts`「高并发热点键」+ demo 场景 1，64 并发 x 8 热点键 |
| 统计冲突率 | `OccStore.stats`：`commits` / `aborts` / `conflictRate` |
| 读多写少 OCC 吞吐大幅领先锁 | 「读多写少场景」测试断言 OCC 吞吐 > 2x 锁（实测约 15x–98x） |
| 重叠写入精准验证 + 干净回滚 | 「验证阶段」「重叠写入」测试 + demo 场景 3 |
