# CAS-Based Token Bucket (Go)

原子无锁并发优化令牌桶：整个可变状态打包进**一个 uint64 原子字**，仅用
`CompareAndSwap` 更新，**实现中没有任何 Mutex**，可支撑百万级 goroutine 并发调用。

## 设计

```
64 位原子状态字:
[ 高 32 位: 可用令牌数 ][ 低 32 位: 上次刷新 tick ]
```

- **时间量化为 tick**：`tickNanos = 1e9 / rate`，每经过 1 个 tick 恰好补充 1 个令牌。
  补充量恒为整数 tick，不存在小数令牌舍入损耗，因此**总放行数量与理论值完全吻合**。
- **单次 CAS 原子更新**：令牌数与刷新时间在同一个 64 位字里一起打包、一起
  Compare-And-Swap，杜绝两个字段分别更新产生的脏数据/撕裂状态。
- **无锁重试**：CAS 失败说明有其它 goroutine 抢先更新，直接基于最新状态重试；
  每个令牌最终只会被一个竞争者赢得，无死锁、无活锁（系统级吞吐随核心数扩展）。
- **关键时序不变式**：`Acquire` 先 `Load` 状态、再读时钟。由于观察到的时间戳必然
  产生于本次 Load 之前，而后读的单调时钟保证 `nowTick >= lastTick`，
  刷新差值绝不会下溢（uint32 回绕安全窗口为 2^31 个 tick）。
- **单调时钟**：默认时钟基于进程启动时刻的 `time.Since`，不受系统墙上时钟回拨影响。

## API

```go
b := tokenbucket.New(rate, capacity) // rate 令牌/秒，初始满桶
ok := b.Acquire()                    // 非阻塞获取 1 个令牌
n  := b.Available()                  // 一致性快照（不改状态）
```

## 运行样例

```console
$ go run ./cmd/demo
workers:           100
duration:          1.045998541s
tokens granted:    1095520
theoretical max:   1145998 (capacity 100000 + 1000000/s x 1.045998541s)
within bound:      true
tokens remaining:  50445
```

100 个 Worker 高频空转争抢 1 秒：放行 1,095,520 + 剩余 50,445 ≈ 理论供给
1,145,998（差值为统计快照的微小时差），无任何超发。

## 测试结果

```console
$ go test -race -v -count=1 ./...
=== RUN   TestConcurrentAcquireExact
--- PASS: TestConcurrentAcquireExact (0.01s)      # 验收：100 Worker，放行数精确等于理论值
=== RUN   TestSingleTokenHighContention
--- PASS: TestSingleTokenHighContention (0.00s)   # 1 个令牌 100 人抢，恰好放行 1 次
=== RUN   TestRefillMatchesTheory
--- PASS: TestRefillMatchesTheory (0.00s)         # 注入式假时钟，逐令牌验证补充算术
=== RUN   TestConcurrentRealClock
    bucket_test.go:169: granted 22759 tokens in 200ms with 100 workers (max 44534)
--- PASS: TestConcurrentRealClock (0.20s)         # 真实时钟 + race detector，无死锁无超发
=== RUN   TestAcquireNeverBlocksOnEmpty
--- PASS: TestAcquireNeverBlocksOnEmpty (0.00s)   # 空桶失败调用不阻塞、不污染状态
PASS
ok  	tokenbucket	1.444s
```

另以 `-race -count=20` 重复执行全部并发用例，结果稳定通过。

### 验收边界对应

| 验收项 | 用例 | 结果 |
|---|---|---|
| 100 并发 Worker 高频 Acquire | `TestConcurrentAcquireExact` / `TestConcurrentRealClock` | PASS |
| 完全无 Mutex | 实现仅使用 `sync/atomic`（源码可检索验证） | PASS |
| 无死锁 | 真实时钟压测 + `-race` + `-count=20` | PASS |
| 无脏数据 | 单字 CAS 打包更新；race detector 无告警 | PASS |
| 总放行与理论值完全吻合 | 假时钟下 5000/2000/1500 等精确断言 | PASS |
