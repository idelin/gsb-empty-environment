# logpipe

高吞吐顺序日志管道（纯 Go 标准库）。多生产者并发投递下维持单调 LSN，
支持插入显式持久化屏障；屏障前所有日志落盘前禁止发布后续数据；
异常回滚时释放全部未确认内存缓冲区。

## 设计

- **单调 LSN**：`Append` 在互斥锁下分配 LSN（从 1 开始严格递增），
  记录进入内存 `pending` 队列并计入内存统计（条数 + 字节数）。
- **单写者刷盘**：后台 flusher 按 LSN 顺序消费 `pending`，批量写入 WAL
  文件并 `fsync`，之后才推进 `durableLSN` 并释放对应缓冲内存。
- **持久化屏障**：`InsertBarrierAt(lsn)` / `InsertBarrier()` 在流中登记
  屏障位点。屏障未落盘期间，任何位于屏障之后的记录禁止发布
  （`Append` 阻塞），直到 `durableLSN` 越过屏障位点。
- **回滚**：写盘失败时，失败点之前的记录已增量落盘并 fsync；
  失败记录及其后所有未确认缓冲被丢弃，内存计数归零（守恒），
  所有阻塞的生产者与屏障以 `ErrPipelineFailed` 唤醒。

## API

```go
p, _ := logpipe.Open("wal.log")                       // 或 OpenWithOptions 注入故障
lsn, err := p.Append([]byte("..."))                   // 单调 LSN
err = p.InsertBarrierAt(1200)                         // 屏障：阻塞至 LSN<=1200 全部落盘
err = p.InsertBarrier()                               // 屏障：当前流末端
entries, bytes := p.MemStats()                        // 未确认缓冲统计
p.DurableLSN(); p.Err(); <-p.Done(); p.Close()
```

## 运行

```sh
go test -race -v ./...   # 验收测试
go run ./cmd/demo        # 运行样例（10 流 x 2000 条，屏障 @500/@1200，故障 @1250）
```

## 验收场景

10 个并发流写入 2000 条日志，在第 500 与第 1200 条插入刷盘屏障，
模拟第 1250 条写入失败：

- 屏障 @500、@1200 均完成（其前日志全部 fsync）；
- 故障后 WAL 中恰好持久化 LSN 1..1249（严格递增），LSN 1250 及其后缺失；
- 回滚释放全部未确认缓冲，`MemStats()` 归零；
- 故障后的 `Append` 返回 `ErrPipelineFailed`。

## 样例输出

```
scenario: 10 streams x 200 records, barriers @500/@1200, injected failure @1250

records accepted:            2000
appends rejected post-fail:  0
barrier @500:                durable
barrier @1200:               durable
pipeline error:              logpipe: write failed at LSN 1250
durable LSN:                 1249
unconfirmed buffers:         entries=0 bytes=0
records persisted in WAL:    1249

verification: all records with LSN <= 1200 durable, LSN 1250 absent, memory conserved
```

## 测试结果

```
=== RUN   TestBarrierRollbackConsistency
--- PASS: TestBarrierRollbackConsistency (0.05s)
=== RUN   TestBarrierDurabilityHappyPath
--- PASS: TestBarrierDurabilityHappyPath (0.04s)
=== RUN   TestInsertBarrierBlocksPublishers
--- PASS: TestInsertBarrierBlocksPublishers (0.01s)
=== RUN   TestRollbackWakesBlockedProducers
--- PASS: TestRollbackWakesBlockedProducers (0.01s)
PASS
ok  	logpipe
```
