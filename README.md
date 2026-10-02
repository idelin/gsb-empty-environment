# mvcc — 内存快照隔离 MVCC 存储引擎

单进程、纯 Go 标准库实现的 MVCC（多版本并发控制）存储引擎。

## 核心语义

- **单调自增事务 ID**：`Begin()` 从 1 开始为每个事务分配单调自增 ID。
- **快照隔离读**：事务开启时固定读快照（全局提交序号），`Get` 只能看到
  开启时已提交的最新版本，以及本事务自己的未提交写入（read-your-writes）。
- **首写获胜（First-Committer-Wins）**：
  - 写入时：若其他事务持有同一键的未提交写入，后写者立即被 Abort；
  - 提交时：若写集中的键在本事务快照之后被其他事务提交，本事务被 Abort。

## 结构

- `mvcc.go` — 引擎实现（`Engine` / `Txn` / 版本链）
- `mvcc_test.go` — 单元测试（含验收场景）
- `cmd/demo/main.go` — 可运行的验收场景演示

## 运行测试

```sh
go test -race -v ./...
```

测试结果（Go 1.24, darwin/arm64）：

```
=== RUN   TestAcceptanceScenario
--- PASS: TestAcceptanceScenario (0.00s)
=== RUN   TestSnapshotIsolation
--- PASS: TestSnapshotIsolation (0.00s)
=== RUN   TestReadYourWrites
--- PASS: TestReadYourWrites (0.00s)
=== RUN   TestFirstCommitterWinsAtCommit
--- PASS: TestFirstCommitterWinsAtCommit (0.00s)
=== RUN   TestNonConflictingWrites
--- PASS: TestNonConflictingWrites (0.00s)
=== RUN   TestDelete
--- PASS: TestDelete (0.00s)
=== RUN   TestAbortReleasesPending
--- PASS: TestAbortReleasesPending (0.00s)
=== RUN   TestMonotonicTxnIDs
--- PASS: TestMonotonicTxnIDs (0.00s)
=== RUN   TestConcurrentTransactions
--- PASS: TestConcurrentTransactions (0.00s)
PASS
ok      mvcc    1.557s
```

## 运行样例

```sh
go run ./cmd/demo
```

输出：

```
[seed]  txn=9   K1 <- "v0" (committed)
[T1]    txn=10  K1 <- "v1" (未提交)
[T2]    txn=11  Get(K1) = "v0"   // 看不到 T1 的未提交写入
[T2]    txn=11  Put(K1) -> mvcc: transaction aborted: write-write conflict on key "K1" with txn 10
[T1]    txn=10  Commit 成功
[T3]    txn=12  Get(K1) = "v1"   // 读到 T1 提交的最新版本
```
