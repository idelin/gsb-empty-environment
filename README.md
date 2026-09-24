# writeback — 有序脏队列回写缓存

纯 Go 标准库实现（`container/list` + `sync` + `context` + `time`），无任何远程缓存依赖。
缓存修改进入**有序脏队列**，后台 goroutine **批量刷源**，失败**保序重试**，
关闭时可**等待排空**或**显式返回未刷清单**。

## 设计

- `Set`/`Delete` 立即写入内存 map（`Get` 可见），同时把 op 追加到 FIFO 脏队列队尾。
- **同键合并**：键已在队列中时，新 op 合并进原槽位（保留原位与 `firstSeq`，只更新为最新
  的 kind/value/seq）。槽位始终携带该键的最新变更，因此**删除与写不可能乱序**。
- **批量刷源**：后台 flusher 在写入唤醒、定时器、失败重试退避三个事件上触发，
  每次取队首至多 `BatchSize` 个 op 调用 `Sink`。
- **失败保序重试**：`Sink` 返回错误时不出队、不清 dirty 标记，退避后整批重试
  （要求 `Sink` 幂等，SET/DELETE 天然幂等）。
- **水位**：`Watermark()` 表示所有 `Seq <= wm` 的 op 已持久化。每个槽位覆盖
  `[firstSeq, op.Seq]`，水位 = 队列中最小 `firstSeq` - 1（队空时为最新 seq），
  因此同键合并后水位依然精确，不会虚高。
- **关闭**：`Close(ctx)` 停掉后台 flusher 并等待其退出（资源释放，`Done()` 关闭），
  然后继续刷直到排空；`ctx` 超时则按序返回未刷清单。

## 可注入点（Options）

| 字段 | 作用 | 默认 |
|---|---|---|
| `Sink` | 数据源写函数；测试在此注入失败 | 必填 |
| `Now` | 时钟，给 op 打时间戳 | `time.Now` |
| `BatchSize` | 每批最大 op 数 | 64 |
| `FlushInterval` | 后台刷源周期 | 100ms |
| `RetryBackoff` | 失败重试退避 | 50ms |

## 运行样例

```
go run ./cmd/demo
```

实际输出：

```
=== phase 1: failures, ordered retry, drain on close ===
buffered: dirty=3 watermark=0
  sink call #1: batch of 3 -> INJECTED FAILURE (dirty kept, will retry)
  sink call #2: batch of 3 -> INJECTED FAILURE (dirty kept, will retry)
  sink call #3: flushed batch [#3 SET alpha=3 #4 DELETE beta #5 SET gamma=4]
after retries: dirty=0 watermark=5 source=map[alpha:3 gamma:4]
close: err=<nil> unflushed=[] watermark=5 source=map[alpha:3 gamma:4]
=== phase 2: source down, close returns the unflushed list ===
  sink call #1: batch of 3 -> INJECTED FAILURE (dirty kept, will retry)
  sink call #2: batch of 3 -> INJECTED FAILURE (dirty kept, will retry)
  sink call #3: batch of 3 -> INJECTED FAILURE (dirty kept, will retry)
close: err=context deadline exceeded
unflushed (still dirty, in order): [#1 SET x=1 #2 SET y=2 #3 DELETE z]
```

phase 1 中 5 次修改（含 alpha 两次写、beta 写后删）合并为 3 个槽位，
两次注入失败后按原序重试成功，水位推进到 5。

## 测试

```
go test ./... -race -count=1 -v
```

结果（全部通过，含 race 检测）：

```
--- PASS: TestFlushPreservesOrder (0.00s)              # FIFO 刷源顺序 + 水位
--- PASS: TestMergeSameKeyKeepsWatermark (0.00s)       # 同键合并后水位不虚高
--- PASS: TestDeleteAndWriteNeverReordered (0.00s)     # 删/写三种时序均不乱序
--- PASS: TestFlushFailureKeepsDirtyAndOrder (0.00s)   # 失败不清 dirty、保序重试
--- PASS: TestBatching (0.00s)                         # 批量大小 2,2,1
--- PASS: TestInjectedClock (0.00s)                    # 注入时钟生效
--- PASS: TestGetSeesUnflushedWrites (0.00s)           # 未刷写对读可见
--- PASS: TestCloseDrainsAndReleasesResources (0.00s)  # 关闭排空 + goroutine 退出
--- PASS: TestCloseReturnsUnflushed (0.05s)            # 超时返回未刷清单 + 资源释放
PASS
ok  	writeback	1.323s
```

资源释放通过 `Done()` 通道断言：flusher goroutine 退出后 `Close` 才返回，
成功与失败两条路径均验证。

## 取舍说明

- `Sink` 在持锁状态下调用（写入方在刷批期间短暂阻塞），换取失败时
  “原样保留、整批重试” 的简单强保证；`Sink` 不得回调 `Cache` 方法。
- 水位推进采用 O(队列长度) 扫描，教学级实现足够清晰；生产可换最小堆。
