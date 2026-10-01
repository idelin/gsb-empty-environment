# mvcc

单进程内存快照隔离（Snapshot Isolation）MVCC 键值存储引擎，仅使用 Go 标准库。

## 设计

- **单调自增事务 ID**：`Store` 内维护计数器，`Begin()` 分配 ID。
- **快照可见性**：事务开始时复制已提交事务 ID 集合作为快照；`Get` 返回快照内已提交的最新版本，本事务的未提交写优先（read-your-writes）。
- **首写获胜（First-Committer-Wins）**：
  - `Put` 时若发现其他活跃事务在同一 key 上有未提交版本，本事务立即中止；
  - `Commit` 时若某 key 在快照之后被其他事务提交，本事务中止。
- **中止回滚**：`Abort`/冲突中止会清除该事务的未提交版本。
- 所有共享状态由一把互斥锁保护，`-race` 下并发测试通过。

## 运行示例

```sh
go run ./cmd/demo
```

输出：

```
seed txn 9 committed K1="old"
T10 wrote K1="new" (uncommitted)
T11 read K1="old" (sees old committed version)
T11 write K1 -> conflict, aborted: mvcc: write-write conflict, transaction aborted: key "K1" has a pending write by txn 10
T10 committed
T12 read K1="new" (sees T1's committed value)
```

## 测试

```sh
go test ./... -race -v
```

`TestAcceptanceScenario` 完整覆盖验收边界：T1(10) 未提交修改 K1 → T2(11) 读到旧版本 → T2 写 K1 冲突被 Abort → T1 提交后 T3(12) 读到最新值。
