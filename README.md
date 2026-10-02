# logpipe — 带有序屏障的日志流管道

高吞吐顺序日志管道（Go，仅标准库）。多生产者并发投递下维持单调 LSN，
支持插入显式持久化屏障；屏障前的日志全部落盘（flush + fsync）之前，
禁止发布任何后续数据；持久化异常时回滚并释放全部未确认内存缓冲区，
保证内存计数守恒。

## 设计

- **单调 LSN**：`Append` 在互斥锁内递增序列号并入队，入队顺序即 LSN
  顺序；`Append` 不阻塞于磁盘 I/O，返回 `Handle` 供调用方等待确认。
- **单刷盘 goroutine**：唯一写 WAL 的执行体，严格按 LSN 顺序写入
  `bufio`，按 `BatchSize` 批量 `flush + fsync`；只有 fsync 成功的记录
  才会按序投递给订阅者（`Subscribe`）并确认其 `Handle`。
- **持久化屏障**：`InsertBarrier()` 在流尾插入屏障；`InsertBarrierAt(lsn)`
  把屏障钉在指定 LSN 之后。刷盘线程遇到屏障时先 `flush + fsync`，
  在屏障落盘前不会写入/发布任何后续记录。
- **异常回滚**：写入或 fsync 失败（含 `Options.FaultLSN` 故障注入）时，
  未 fsync 的缓冲数据直接丢弃，队列中及待确认的全部记录以错误回滚；
  每个未确认缓冲区都被释放，`Allocated == Released`、`Outstanding == 0`。
- **内存计数**：每次 `Append` 计一次分配；记录被确认（落盘发布）或回滚
  时计一次释放。`Outstanding()` 最终归零即计数守恒。

## 文件

- `pipeline.go` — 管道实现（`Pipeline` / `Handle` / `Barrier` / `Options`）
- `pipeline_test.go` — 验收测试与正常路径、尾屏障测试
- `cmd/demo/main.go` — 运行样例（正常运行 + 故障注入回滚各一次）

## 运行

```sh
go test ./... -v -race   # 验收测试
go run ./cmd/demo        # 运行样例
```

## 验收边界（pipeline_test.go）

`TestBarrierRollbackUnderFault`：10 个并发流写入 2000 条日志，在 LSN 500
与 1200 处插入刷盘屏障，注入第 1250 条写入失败。断言：

- 两个屏障均成功落盘；LSN ≤ 1200 的 `Handle` 全部确认成功；
- LSN ≥ 1250 的 `Handle` 全部以注入错误回滚；
- WAL 文件内容是流的无空洞连续前缀，长度 ∈ [1200, 1249]；
- 发布给订阅者的记录同样是连续前缀且被 WAL 覆盖；
- `Outstanding() == 0` 且 `Allocated == Released == 已投递数`（内存守恒）。

## 测试结果

```
=== RUN   TestBarrierRollbackUnderFault
--- PASS: TestBarrierRollbackUnderFault (0.82s)
=== RUN   TestOrderedDeliveryAndClose
--- PASS: TestOrderedDeliveryAndClose (0.61s)
=== RUN   TestTailBarrier
--- PASS: TestTailBarrier (0.06s)
PASS
ok  	logpipe	2.751s   # go test ./... -race，连跑 5 轮全部通过
```

## 样例输出（go run ./cmd/demo）

```
== run 1: 4 producers x 50 records, barriers @60/@140, no fault ==
  appended:              200
  acked durable:         200
  rolled back:           0
  published:             200
  durable LSN:           200
  records in WAL:        200
  buffers:               200 allocated / 200 released / 0 outstanding

== run 2: 4 producers x 50 records, barrier @120, fault injected @130 ==
  barrier@120 durable, fault: simulated disk failure
  appended:              200
  acked durable:         120
  rolled back:           80
  published:             120
  durable LSN:           120
  records in WAL:        120
  buffers:               200 allocated / 200 released / 0 outstanding

buffer accounting conserved in both runs (outstanding = 0)
```
