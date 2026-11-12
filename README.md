# 隐私阈值发布原子性

计数达到阈值才可发布的安全服务，在并发撤回时可能泄露低于阈值的人群。写供Go发布宿主调用的快照组件，查询固定记录版本，阈值判断、发布值和预算扣除绑定同一版本；撤回发生后不再发布旧快照的新结果，已发布结果保留审计。用事务存储和同步屏障验证阈值边界、并发发布与撤回。

## 组件

- `snapshot.go`：`Store` 事务存储与 `Snapshot` 固定版本快照（无第三方依赖，仅用标准库）。
- `snapshot_test.go`：阈值边界、并发发布/撤回同步屏障、预算竞争、回滚与版本固定测试。

## 模型

- 每条记录是一条只追加的版本链：`Create` 产生版本 1；`Withdraw` 在一个事务内把
  当前版本标记为 `withdrawn` 并安装新版本（仅允许收缩人数与预算）。旧版本不删除，
  始终可通过 `Snapshot.View` 查询，供审计与排查。
- `Snapshot` 固定一个记录版本。`Publish` 在单条记录的单事务内依次完成：
  当前版本校验（非撤回、仍是最新）→ 阈值判断 → 预算判断 → 发布值与预算扣除 →
  追加审计，全部成功才提交；任一步失败则深快照回滚，不留预算或审计痕迹。
- 撤回先于发布提交时，旧快照的 `Publish` 返回 `ErrVersionWithdrawn`，
  不会释放低于阈值人群的新结果；已提交的发布审计永久保留。
- 记录级互斥锁使同一记录的事务严格串行（严格可串行化），不同记录互不阻塞。
- `Hooks.BeforePublishCommit` / `BeforeWithdrawCommit` 在事务持锁区内、写操作前执行，
  作为确定性同步屏障：可让事务停在提交边界以验证并发顺序，返回错误即中止事务。

## 宿主接入

```go
store := netsec.NewStore(nil)
snap, _ := store.Create(ctx, "group-a", 100, netsec.RecordConfig{
    Threshold: 50,
    Budget:    1000,
})

// 长期持有 snap；所有查询与发布都绑定它创建时的版本。
view, _ := snap.View(ctx)
entry, err := snap.Publish(ctx, result, 7) // 阈值、预算、审计同一事务

// 成员撤回：旧 snap 被封存，返回的新快照绑定新版本。
next, _ := store.Withdraw(ctx, "group-a", 30, 993)
_, err = snap.Publish(ctx, stale, 0) // netsec.ErrVersionWithdrawn
trail, _ := store.Audit(ctx, "group-a") // 含撤回前的已发布结果
```

## 测试

```sh
go test -race -count=1 ./...
```
