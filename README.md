# ss2pl — 严格两阶段锁（SS2PL）与收缩期管理器

纯 Go 标准库实现的严格两阶段锁内核，包含锁管理器、收缩期管理器、
事务管理器、锁状态监控器与崩溃恢复，测试基于 `go test`。

## 设计

```
TxnManager ── 创建/跟踪事务，维护提交日志（提交时刻时间戳）与全局操作日志
   │
   ├─ Txn ── 扩张期只加锁（Read→S 锁，Write→X 锁 + undo 日志）
   │          Commit/Abort 时进入收缩期
   │
   ├─ ShrinkingManager ── 收缩期管理器：唯一被允许释放锁的组件，
   │                      在提交/回滚时一次性原子释放事务的全部锁
   │
   ├─ LockManager ── 锁表 + FIFO 等待队列（公平、防饥饿）
   │                  只暴露 Acquire / ReleaseAll，结构上不存在
   │                  “提前释放单个行锁”的 API
   │                  等待图（wait-for）环检测 → 牺牲者返回 ErrDeadlock
   │
   └─ Store ── 就地写入的 KV 存储；undo 日志支持回滚（含崩溃恢复）
```

**严格性（SS2PL）由结构保证**：事务没有任何释放单个锁的接口；
`LockManager.ReleaseAll` 只被 `ShrinkingManager.Shrink` 调用，且每个事务
恰好调用一次（Commit 或 Abort 时）。因此扩张期只增不减，收缩期一次释放。

**可串行化**：SS2PL 下，冲突操作只能在前一个持锁者提交并释放锁之后发生，
因此冲突顺序与提交顺序一致 —— 调度等价于按提交时刻时间戳排序的串行调度。
`TxnManager.VerifyCommitOrderEquivalence` 从操作日志与提交日志出发对此做
经验性验证（测试中会实际执行）。

**崩溃恢复**：`TxnManager.Crash(id)` 注入异常崩溃，恢复路径按逆序回放
undo 日志撤销全部修改，并经收缩期管理器一次性释放该事务持有的所有锁；
若事务正阻塞在锁等待上，等待会被取消（`ErrWaitCancelled`）。

**监控**：`Monitor` 订阅锁事件与事务生命周期事件，校验核心不变式 ——
任何锁的 RELEASE 事件都不得出现在其事务的 COMMIT/ABORT 决策之前。

## 文件

| 文件 | 说明 |
|---|---|
| `lock.go` | 锁管理器：锁表、等待队列、死锁检测、一次性释放 |
| `txn.go` | 事务、事务管理器、收缩期管理器、崩溃恢复 |
| `store.go` | KV 存储（undo 日志所需的旧值返回） |
| `monitor.go` | 锁状态监控器（提交前零释放校验） |
| `verify.go` | 提交时刻时间戳序等价性验证 |
| `ss2pl_test.go` | 验收测试（8 个用例） |
| `cmd/demo/main.go` | 运行样例：并发转账 + 崩溃注入 |

## 运行

```sh
go test -v ./...    # 运行验收测试
go run ./cmd/demo   # 运行样例
```

## 验收测试覆盖

- `TestNoLockReleasedBeforeCommit` — 未发出 Commit 前，冲突事务必须阻塞，
  监控器断言零释放；提交后阻塞者读到已提交的值。
- `TestConcurrentContention` — 16 协程 × 25 轮多资源争抢转账：金额守恒
  （无丢失更新）、调度等价于提交时刻时间戳序、监控器零违规。
- `TestCrashRollback` — 持有 3 把锁且有脏写的事务崩溃：修改全部回滚、
  锁全部释放、后续事务立即获得锁。
- `TestCrashWhileWaiting` — 崩溃发生在锁等待中：等待被取消、事务清理。
- `TestDeadlockDetection` — AB-BA 死锁：恰好一个牺牲者回滚，幸存者提交。
- `TestSharedLocksAreCompatible` / `TestDoubleCommitRejected` /
  `TestLockTableSnapshot` — 共享锁兼容、生命周期单次转换、锁表快照。

## 测试结果

```
$ go test -race -v ./...
=== RUN   TestNoLockReleasedBeforeCommit
--- PASS: TestNoLockReleasedBeforeCommit (0.15s)
=== RUN   TestConcurrentContention
--- PASS: TestConcurrentContention (0.70s)
=== RUN   TestCrashRollback
--- PASS: TestCrashRollback (0.00s)
=== RUN   TestCrashWhileWaiting
--- PASS: TestCrashWhileWaiting (0.10s)
=== RUN   TestDeadlockDetection
--- PASS: TestDeadlockDetection (0.00s)
=== RUN   TestSharedLocksAreCompatible
--- PASS: TestSharedLocksAreCompatible (0.00s)
=== RUN   TestDoubleCommitRejected
--- PASS: TestDoubleCommitRejected (0.00s)
=== RUN   TestLockTableSnapshot
--- PASS: TestLockTableSnapshot (0.00s)
PASS
ok  	ss2pl	2.518s
```

## 样例运行输出（节选）

```
== phase 1: 8 goroutines x 10 concurrent transfers ==
balances after phase 1:
  alice   1000
  bob     1000
  carol   1000
  dave    1000
  TOTAL   4000

== phase 2: crash a transaction holding dirty writes ==
txn 902 wrote dirty values and holds locks [alice bob] -- now it crashes
after crash recovery (rollback + release all locks):
  alice   1000
  bob     1000
  ...
locks still held by crashed txn 902: []
a follow-up transaction locked alice instantly and set her balance to 1500

== verification ==
commit-order equivalence: OK (schedule equivalent to commit-time timestamp order)
strict-2PL monitor: OK (no lock released before commit/abort)
commit log (commit-time timestamp order): 81 commits
```
