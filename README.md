# groupbreaker

按分组维护的熔断器（Go，仅标准库）。每个分组独立维护
**关闭（closed）→ 打开（open）→ 半开（half-open）** 状态机，
半开探测数量有上限，探测结果与过期回调均使用**代际（generation）校验**，
旧代的失败无法重开已恢复的新代。

## 设计要点

- **分组隔离**：`Breaker` 内部按组名懒创建独立 `group`，A 组熔断不影响 B 组。
- **代际校验**：每次状态迁移 `gen++`。探测许可携带 `gen`，过期回调闭包捕获
  `gen`；回调/结果落地前比对当前代，不匹配即丢弃——旧失败不能重开新代，
  重复或迟到的过期回调不能篡改新代。
- **半开额度**：`HalfOpenMaxProbes` 限制并发探测数，超额快速失败
  （`ErrProbeLimit`）；代际切换时重置探测计数，旧代探测不占用新代额度。
- **快速失败**：打开状态直接返回包装了 `ErrOpen` 的错误，任务函数不会执行。
- **依赖注入**：任务函数（`Do` 的参数）、时间（`Clock` 接口：`Now` +
  `AfterFunc`）、失败判定（`IsFailure`）全部可注入，测试用假时钟驱动。
- **同步原语**：仅使用 `sync.Mutex` 保护状态，无第三方依赖。

## API 摘要

```go
b := groupbreaker.New(groupbreaker.Options{
    FailureThreshold:  2,                // 连续失败多少次后打开
    OpenTimeout:       10 * time.Second, // 打开多久后转入半开
    HalfOpenMaxProbes: 1,                // 半开并发探测上限
    IsFailure:         nil,              // 失败分类器，nil = 任何非 nil error
    Clock:             nil,              // nil = 真实时钟
})
err := b.Do("group-a", func() error { return callDownstream() })
errors.Is(err, groupbreaker.ErrOpen)       // 打开期快速失败
errors.Is(err, groupbreaker.ErrProbeLimit) // 半开探测额度已满
```

## 运行样例

```
$ go run ./cmd/demo
A call 1: err=boom -> state=closed
A call 2: err=boom -> state=open
A while open: err=group "A": groupbreaker: circuit is open (ErrOpen=true)
B while A open: err=<nil> -> state=closed
A after timeout: state=half-open
A probe: err=<nil> -> state=closed
```

## 测试结果

`go test -race -v ./...`（竞态检测开启，全部通过）：

```
=== RUN   TestTripsOpenAfterThresholdAndRejectsFast
--- PASS: TestTripsOpenAfterThresholdAndRejectsFast (0.00s)
=== RUN   TestExpiryMovesToHalfOpenAndProbeSuccessCloses
--- PASS: TestExpiryMovesToHalfOpenAndProbeSuccessCloses (0.00s)
=== RUN   TestHalfOpenProbeQuotaUnderConcurrency
--- PASS: TestHalfOpenProbeQuotaUnderConcurrency (0.00s)
=== RUN   TestStaleProbeFailureCannotReopenRecoveredGeneration
--- PASS: TestStaleProbeFailureCannotReopenRecoveredGeneration (0.00s)
=== RUN   TestStaleExpiryCallbackCannotMutateNewGeneration
--- PASS: TestStaleExpiryCallbackCannotMutateNewGeneration (0.00s)
=== RUN   TestGroupsAreIndependent
--- PASS: TestGroupsAreIndependent (0.00s)
=== RUN   TestFailureClassifierIsInjectable
--- PASS: TestFailureClassifierIsInjectable (0.00s)
=== RUN   TestConcurrentMixedLoad
--- PASS: TestConcurrentMixedLoad (1.36s)
PASS
ok  	groupbreaker	1.369s
```

## 文件结构

- `breaker.go` — 状态机、代际校验、分组管理
- `clock.go` — `Clock`/`Timer` 抽象与真实时钟实现
- `breaker_test.go` — 假时钟 + 注入失败的全量测试
- `cmd/demo/main.go` — 真实时钟下的运行样例
