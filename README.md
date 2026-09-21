# fixedsched

固定频率任务调度器（仅 Go 标准库）。计划时刻锚定基准时刻 `Base`：
第 `n` 轮在 `Base + n*Period` 触发，与上一轮何时结束无关，因此慢任务不会造成节奏漂移。

## 特性

- **锚定计划**：轮次 ID = `floor((now - Base) / Period)`，下一轮时刻由基准时刻推算，而非“结束时间 + Period”。
- **错过轮次策略**：运行超时/超周期时，`SkipMissed` 直接跳过错过的轮次；`MergeMissed` 把错过的轮次合并为一次运行（携带最新错过的轮次 ID）。
- **时钟回拨防护**：轮次 ID 单调递增（`lastRound+1` 起算），回拨后同轮不会重复执行。
- **取消清理**：`Stop` 取消调度循环并释放未触发的计时器。
- **依赖注入**：任务函数、时钟（`Clock`/`Timer` 接口）与失败回调均由调用方注入，测试用 `FakeClock` 手动推进时间。
- **同步原语**：`sync.Mutex` / `sync.WaitGroup` / `context`，测试通过 `go test -race`。

## 用法

```go
s := scheduler.New(scheduler.RealClock{}, task, scheduler.Options{
    Base:        time.Now(),
    Period:      10 * time.Second,
    ExecTimeout: 5 * time.Second, // 单次运行超时，0 表示不限制
    Policy:      scheduler.SkipMissed,
    OnError:     func(round int64, err error) { log.Printf("round %d: %v", round, err) },
})
s.Start()
defer s.Stop()
```

示例：周期 10、一次运行 25 时，`SkipMissed` 下一轮是锚定的第 3 轮（Base+30），
而不是 Base+25+10；`MergeMissed` 则先补跑一次合并轮（ID 2），再回到 Base+30。

## 运行

```sh
go test -race ./...   # 测试（含竞态检测）
go run ./cmd/demo     # 运行样例：周期 200ms、任务 500ms，观察轮次跳跃（0,4,7,...）
```
