多粒度两阶段锁（`IS`、`IX`、`S`、`SIX`、`X`）在引入同事务锁升级（如持有 `S` 锁申请升级为 `X` 或 `SIX`）后，若等待队列采用简单 FIFO 或盲目放行后续读锁，就会陷入写者饥饿或自锁死锁；而当两事务同时持有同一行的 `S` 锁并相继申请升级为 `X` 锁时，必然形成即时升级死锁，此时若能仅回滚较年轻事务的最近保存点（Savepoint）释放其新申请的锁即可破环，就无需整事务中止。为数据库锁管理器交付可导入的 Go 多粒度意向锁与死锁仲裁包。层级资源为 `DB -> Table -> Page -> Row`，强制执行标准多粒度相容矩阵（含 `SIX` 与 `IS` 相容、与 `S`/`IX`/`SIX`/`X` 互斥！）与自顶向下意向锁前置校验。规则：1）队列调度优先处理锁升级等待者（位于新普通等待者之前，且当队列头存在不相容等待者时后续新请求须排队防饥饿，但同一事务已持有相容锁的重入申请直接成功）；2）维护精确等待图（Waiting-For Graph，区分多共享锁持有者产生多条出边，排除自边但包含两事务双向升级环），每次阻塞即时检测全部极小死锁环；3）死锁受害者选择优先评估各环内事务的最小代价局部回滚方案（每个事务维护保存点栈及各保存点后获取的锁集合；仅当回滚到某保存点能释放引发当前死锁边的目标锁或撤销当前阻塞申请时才算有效破环方案！按回滚代价低、后启动事务优先、事务 ID 大优先确定性选取受害者与目标保存点或整事务中止）；4）释放受害者对应锁后按优先级重新唤醒并授予队列中所有现已相容的等待者。验收覆盖 `S->X` 双向并发升级死锁、`SIX` 意向相容矩阵、升级队列防写饥饿、局部保存点回滚破环保留早期锁不重做整事务以及级联唤醒顺序。

## 交付内容

可导入包 `lockmgr`（模块 `prescreen.local/ps-a9cfa18e01`），无第三方依赖：

- `mode.go`：五种锁模式、标准相容矩阵、锁转换 `unionMode` 与祖先意向要求。
- `resource.go`：`DB -> Table -> Page -> Row` 资源层级与祖先链。
- `manager.go`：加锁/解锁、自顶向下意向校验、祖先意向自动升级、升级优先队列、防饥饿队头规则、级联唤醒泵。
- `wfg.go`：精确等待图（多共享持有者多出边、排除自边、升级者间双向环）与全部极小环枚举。
- `deadlock.go`：保存点/整事务回滚方案的有效性判定、代价模型与确定性受害者选择。
- `savepoint.go`：保存点栈、局部回滚（仅释放保存点之后的锁/升级并撤销阻塞申请）。
- `inspect.go`：队列、等待图、死锁环、仲裁记录等只读诊断 API。

## 快速上手

```go
m := lockmgr.NewManager()
db := lockmgr.NewDB("shop")
table := lockmgr.NewTable("shop", "orders")
page := lockmgr.NewPage("shop", "orders", "p1")
row := lockmgr.NewRow("shop", "orders", "p1", "order-1")

txn := m.Begin(0)
if err := m.Lock(txn, db, lockmgr.IS); err != nil { return err }
if err := m.Lock(txn, table, lockmgr.IS); err != nil { return err }
if err := m.Lock(txn, page, lockmgr.IS); err != nil { return err }
if err := m.Lock(txn, row, lockmgr.S); err != nil { return err }

sp, _ := m.CreateSavepoint(txn, "before-update")
// ... 升级到 X 后若成为死锁受害者，Lock 返回 ErrDeadlockRollback，
// 管理器可能仅回滚到 sp 并保留保存点之前的锁。
_ = sp
_ = m.Commit(txn)
```

## 测试

```bash
go test -race -count=5 ./...
```

验收用例覆盖双向 `S -> X` 升级死锁、`SIX` 相容矩阵、升级优先与防写饥饿、保存点局部回滚保留早期锁、等待图多持有者边以及跨资源级联唤醒顺序。
