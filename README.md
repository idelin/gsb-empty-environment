# aimd — 自适应并发限制器

基于显式延迟样本与超时信号，按 AIMD（加性增大 / 乘性减小）规则动态调整并发上限。仅依赖 Go 标准库。

## 规则

- **样本注入**：任务函数、耗时与失败由调用方注入（`Adjust([]Sample`），限制器本身不执行任务探测。
- **乘性减小**：批次内任一超时 → `limit = max(min, limit * Backoff)`，并记录减小时刻。
- **加性增大**：批次全部成功 → `limit = min(max, limit + Additive)`。
- **恢复防抖**：减小后 `Debounce` 窗口内的成功样本被抑制，窗口外才恢复增大。
- **空样本不调整**：`Adjust(nil)` / `Adjust([])` 不改变上限。
- **减小不强杀、不补位**：上限下调后已运行任务正常结束；仅当 `running < limit` 才放行新任务。

## 关键接口

- `NewLimiter(Options)`：`InitialLimit/MinLimit/MaxLimit/Additive/Backoff/Debounce/Clock`。
- `Adjust([]Sample) (old, new int)`：注入一批样本并应用 AIMD。
- `Acquire(ctx) (release func(), err error)` / `Run(ctx, fn)`：动态容量信号量。
- `Clock` / `ManualClock`：手动时钟使测试中的变化序列完全确定。

## 运行样例（`go run ./cmd/demo`）

```
[t=+  21ms] 4 tasks in flight, running=4 limit=4
[t=+  60ms] timeout sample -> limit 4 => 2 (running=3, no kill)
[t=+ 163ms] backfill correctly blocked while running=3 >= limit=2
[t=+ 163ms] success sample inside debounce window -> limit 2 => 2 (suppressed)
[t=+ 501ms] remaining tasks drained, backfill admitted, running=0
[t=+ 751ms] success sample after debounce -> limit 2 => 3 (recovered)
```

## 测试结果（`go test -race -v ./...`）

```
=== RUN   TestEmptySamplesNoAdjust
--- PASS: TestEmptySamplesNoAdjust (0.00s)
=== RUN   TestTimeoutMultiplicativeDecrease
--- PASS: TestTimeoutMultiplicativeDecrease (0.00s)
=== RUN   TestAdditiveIncreaseCappedAtMax
--- PASS: TestAdditiveIncreaseCappedAtMax (0.00s)
=== RUN   TestRecoveryDebounce
--- PASS: TestRecoveryDebounce (0.00s)
=== RUN   TestDeterministicSequence
--- PASS: TestDeterministicSequence (0.00s)
=== RUN   TestDecreaseDoesNotKillOrBackfill
--- PASS: TestDecreaseDoesNotKillOrBackfill (0.11s)
=== RUN   TestConcurrentStress
--- PASS: TestConcurrentStress (1.04s)
PASS
ok  	aimd	2.737s
```
