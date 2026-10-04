# memsnap — Go 多版本快照内存配置库

仅依赖 Go 标准库。公开 API（题目中 `beginRead/get/endRead/writeBatch` 的 Go 导出形式）：

- `s := memsnap.NewStore()`
- `snap := s.BeginRead()` / `snap.Get(key)` / `snap.EndRead()`
- `s.WriteBatch([]memsnap.Op{memsnap.Put(k, v), memsnap.Del(k2)})`
- `s.Stats()` → `Version / Retained / Peak / Snapshots`

## 设计

- 每个键一条版本链（新→旧，版本号非递增），节点发布后不可变，`next` 为原子指针。
- 写者持 `Store.mu` 串行提交：整批校验先于加锁，失败则什么都不发布（版本号都不递增）；
  成功时版本号 +1，本批所有节点共享同一版本号，原子挂到链头。
- 读者 `BeginRead` 在同一互斥锁下钉住当前版本并注册；`Get` 全程无锁，
  沿链找第一个 `version <= 快照版本` 的节点，删除标记对该版本表现为 `ErrNotFound`。
- `EndRead` 用 CAS 关闭句柄（重复关闭幂等），首次关闭时注销快照并触发回收。

## 线性化点

- `WriteBatch`：持锁期间版本号递增 + 链头原子发布（整批为一个线性化点）。
- `BeginRead`：持锁读取当前版本号并注册，与提交全序。
- `Get`：无锁；快照逻辑状态在钉住后不可变，可在 begin/end 之间任意点线性化，
  具体落在链头的原子加载上。
- `EndRead`：`closed` 的 CAS。

## 回收规则

节点 `n`（链上前驱 `p`）对存活快照 `sv` 可见当且仅当 `n.version <= sv < p.version`。
最新节点永远保留（供未来读者，含删除墓碑）。其余无可见快照的节点被摘链，
由 Go GC 释放。`Stats().Retained` 统计的是实际保留的版本节点数（含墓碑），
不是最新键数。

## 测试

```sh
go test ./... -v -count=1        # 功能 + 受控屏障 + 随机串行模型
go test -race ./... -count=1     # race 检查
MEMSNAP_SEED=42 go test -run TestRandomHistoryAgainstSerialModel -v
```

- `TestControlledConcurrencyBarrier`：channel 屏障编排——旧读者持有 v1，
  写者连续覆盖再删除同键（v2、v3），新读者读新版；旧读者存活时保留 3 个节点，
  关闭后回收至 1 个（v3 墓碑）。
- `TestRandomHistoryAgainstSerialModel`：随机短历史对照串行版本模型逐次比对，
  并报告保留节点峰值与最终回收后数量。
