# gencache — 代际任务缓存（generation-scoped task cache）

一个只使用 Go 标准库的小模块。任务在启动时读取“当前代际”，在**不持锁**的
情况下完成计算，然后发布结果；代际切换（`Switch`）与结果发布在同一把
`sync.Mutex` 上线性化。

## 正确性规则

- 任务启动时通过 `Start()` 快照当前代际 id。
- 计算函数、耗时、失败均由调用方（测试中：注入函数）决定，代码内部没有任何
  定时器或 `time.Sleep`。
- 计算在锁外进行；只有“发布还是拒绝”的判定在锁内，与 `Switch` 互斥。
- 旧代际任务的计算与切换同时完成时，结果只有两种命运：
  - **先于切换线性化** → 写入该任务所属代际的缓存，`StatusPublished`；
  - **后于切换线性化** → 被拒绝，`StatusRejected`，任何缓存（含新代际缓存）
    都不会被写入；任务返回的 `Cleanup` 在锁外执行，恰好一次。
- `Switch` 不等待旧任务或旧清理完成；新代际任务可以立即开始、立即发布，
  不会被旧任务的清理阻塞。
- 任务返回错误时为 `StatusFailed`，不写缓存、不调用清理。

## 同步原语

- `sync.Mutex`：线性化发布与切换，保护 `current` 指针、代际表和各代缓存。
- `sync.WaitGroup`：仅用于 `WaitInflight`，方便测试确定性地等待某一代的
  在途任务收尾；`Switch` 本身从不等待它。

## API

| 方法 | 作用 |
| --- | --- |
| `NewCoordinator()` | 从第 0 代开始 |
| `Start() int` | 任务启动时原子读取当前代际 id |
| `Execute(genID, key, task) Outcome` | 锁外计算，锁内线性化发布/拒绝 |
| `Switch() int` | 原子推进到新的空代际，不等待旧任务 |
| `Result(genID, key)` | 读取某代缓存 |
| `WaitInflight(genID)` | 测试辅助：等待某代在途任务完成 |

`Task` 签名：

```go
type Task func() (value interface{}, cleanup Cleanup, err error)
```

## 运行

```sh
go test -race -count=1 -v ./...
```

## 运行样例

```
=== RUN   TestPublishAndGet
--- PASS: TestPublishAndGet (0.00s)
=== RUN   TestFailureNotCached
--- PASS: TestFailureNotCached (0.00s)
=== RUN   TestOldGenerationRejectedRunsCleanup
--- PASS: TestOldGenerationRejectedRunsCleanup (0.00s)
=== RUN   TestPublishedBeforeSwitchLandsInOldGeneration
--- PASS: TestPublishedBeforeSwitchLandsInOldGeneration (0.00s)
=== RUN   TestPublishSwitchRace
    gencache_test.go:198: linearization samples: published=199 rejected=201
--- PASS: TestPublishSwitchRace (0.01s)
=== RUN   TestMultipleOldTasksAllRejected
--- PASS: TestMultipleOldTasksAllRejected (0.00s)
=== RUN   ExampleCoordinator
--- PASS: ExampleCoordinator (0.00s)
PASS
ok  	gencache	1.186s
```

`TestPublishSwitchRace` 用 channel 把旧任务“计算完成”与 `Switch` 同时放到锁
竞争点，重复 400 次；每次断言旧结果绝不出现在新代际缓存中，并要求两种线性化
顺序都被实际观测到。`-race` 下连续 10 次整跑及 30× 重复压力均通过。
