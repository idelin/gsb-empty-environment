# snapshotstore — 多版本快照内存配置库

仅依赖 Go 标准库。写者经 `WriteBatch` 串行提交一组键更新；读者经
`BeginRead`/`Get`/`EndRead` 在单个版本上无锁读取。

## 设计

每个键一条不可变版本链（cons 链表，新版本在前）。每次提交把整批操作
作为同一个新版本原子发布；快照句柄固定在某个版本号上，`Get` 沿链取
`version <= snapVersion` 的最新节点，因此整个读取过程只见一个版本。
删除写入墓碑节点：新版本不可见，旧读者仍读到旧值。

## 线性化点

- `WriteBatch`：先整批校验（任一操作非法则整体拒绝，不发布半批结果）；
  成功提交的线性化点是根表指针的原子交换（`root.Store`），整批作为一个
  版本同时可见。
- `BeginRead`：在写者互斥锁内读取当前版本并登记为存活读者，与提交、
  回收互斥。
- `Snapshot.Get`：无锁。原子加载根表指针后沿不可变链读取；回收从不就
  地修改节点，只构建新链并换根，进行中的读取不受影响。
- `Snapshot.EndRead`：线性化点是关闭标志的 atomic CAS；仅首次 CAS 成功
  者注销读者，重复关闭幂等。关闭后 `Get` 返回 `ErrSnapshotClosed`。
- `Reclaim`：取「最新版本与所有存活读者版本」的最小值 `minNeeded`，
  仅丢弃任何存活读者都不可见的节点（保留 `> minNeeded` 的全部节点及
  `minNeeded` 处的 floor 节点；保留段全为墓碑时整键移除）。

## 内存统计

`Stats()` 的 `RetainedNodes` 统计实际保留的跨版本节点数（不是最新键
数），`PeakNodes` 为其水位峰值；`Reclaim` 返回本次释放的节点数。

## 测试

```sh
go test -race -v ./...
```

- `TestConcurrencyBarrier`：受控屏障——读者持有旧版，写者连续覆盖再删
  除同键，另一读者读新版；旧读者存活时回收必须释放 0 节点，关闭后才
  允许回收。
- `TestRandomHistoryVsSerialModel`：随机短历史对照串行版本模型，报告
  保留节点峰值与回收后数量。
- `TestConcurrentReadersWriterReclaimer`：写者每版本写入 `val-<v>`，
  读者严格校验所见值等于其固定版本，配合 `-race` 做竞争检查。
