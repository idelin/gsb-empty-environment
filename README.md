# ss2pl — 严格两阶段锁（SS2PL）内核与收缩期管理器

纯 Go 标准库实现的两阶段锁（2PL）协议内核，采用**严格两阶段加锁（SS2PL）**：
事务在扩张期（growing phase）只加锁、不释放；提交或回滚时进入收缩期
（shrinking phase），**一次性原子释放全部锁资源**。由于所有锁都持有到
提交点，产生的冲突可串行化调度**等价于按事务提交时刻的时间戳排序**
（commit-order equivalence）。

## 结构

- `lock.go` — 锁管理器内核
  - 行级 S/X 锁，支持 S→X 升级与可重入
  - FIFO 等待队列，防 barging（插队）
  - wait-for 图**死锁检测**：加锁会成环时返回 `ErrDeadlock`，请求方回滚
  - 监控视图：`Snapshot()`（各资源的持有者/等待者）、`HeldBy(txnID)`
- `txn.go` — 事务层与收缩期管理
  - `Begin / Read / Write / Commit / Rollback`
  - 阶段机：`Growing → Shrinking → Done`；离开扩张期后任何加锁/读写
    均返回 `ErrTxnNotGrowing`
  - `Commit` 分配提交时间戳并调用 `ReleaseAll` 一次性释放全部锁
  - `Rollback` 按逆序应用 undo 日志（before-image）后一次性释放全部锁
  - `Exec(fn)`：崩溃安全执行——`fn` panic（注入故障）或出错时自动回滚
    并释放该事务持有的所有锁

## 运行样例

```sh
go test -run ExampleTxn -v .
```

输出：

```
=== RUN   ExampleTxn
--- PASS: ExampleTxn (0.00s)
PASS
ok      ss2pl
```

样例逻辑：转账事务 alice→bob 提交成功；随后一个事务写入后 `panic`
（模拟断电），`Exec` 自动回滚，最终 `alice=70 bob=80`，崩溃事务残留
锁数量为 0。

## 测试结果

```sh
go test -race -v ./...
```

```
=== RUN   TestConcurrentContention      # 16 goroutine × 25 次递增争抢同一计数器，无丢失更新
--- PASS: TestConcurrentContention (0.26s)
=== RUN   TestNoEarlyRelease            # 监控断言：Commit 前两个行锁都不得释放，竞争者排队等待
--- PASS: TestNoEarlyRelease (0.18s)
=== RUN   TestShrinkPhaseEnforcement    # 提交后任何读写/再加锁均返回 ErrTxnNotGrowing
--- PASS: TestShrinkPhaseEnforcement (0.00s)
=== RUN   TestCrashRollback             # 注入 panic：全部写撤销、全部锁释放、锁管理器可继续服务
--- PASS: TestCrashRollback (0.00s)
=== RUN   TestExplicitRollback          # 显式回滚路径
--- PASS: TestExplicitRollback (0.00s)
=== RUN   TestDeadlockDetection         # AB-BA 死锁：检测器精确否决一个请求者，幸存者完成
--- PASS: TestDeadlockDetection (0.12s)
=== RUN   TestCommitOrderEquivalence    # 终态 == 按提交时间戳顺序串行执行的结果
--- PASS: TestCommitOrderEquivalence (0.00s)
=== RUN   ExampleTxn
--- PASS: ExampleTxn (0.00s)
PASS
ok      ss2pl   2.409s
```

`go test -race -count=5 ./...` 连续 5 轮全部通过。

## 验收边界对应

| 验收项 | 实现/测试 |
| --- | --- |
| 多事务并发争抢多资源 | `TestConcurrentContention`、锁管理器 FIFO 队列 |
| Commit 前严禁提前释放局部行锁 | 内核只在 `ReleaseAll` 释放；`TestNoEarlyRelease` 用监控快照验证 |
| 崩溃时安全撤销锁并回滚修改 | `Txn.Exec` 的 recover + undo 日志；`TestCrashRollback` |
| 调度等价于提交时刻时间戳排序 | SS2PL 持有锁至提交点；`TestCommitOrderEquivalence` 验证 |
