# reactive — 带事务批处理（Batch）的响应式系统

纯 Go 标准库实现，无第三方依赖。

## 核心概念

- `Scope`：响应式上下文，持有依赖图与批处理队列。
- `Signal[T]`：可写响应式值，`Set` 时通知订阅者。
- `Computed[T]`：惰性派生值，依赖变更后仅在下次 `Get` 时重算。
- `Effect`：副作用观察者，创建时立即运行一次建立依赖，之后依赖变更即重跑。
- `Scope.Batch(fn)`：事务批处理。`fn` 内部对所有 Signal 的赋值只会让
  Computed 标记为 dirty、Effect 进入去重队列，**全程静默**；当最外层
  batch 正常退出时统一 flush，每个 Effect 恰好运行一次并读到最新值。
  若 `fn` panic，则丢弃队列、不触发任何更新，panic 继续向上传播。

## 运行

```sh
go test ./... -v   # 运行测试
go run ./cmd/demo  # 运行示例
```

## 示例输出（go run ./cmd/demo）

```
[effect] x=0 y=0 sum=0
-- individual sets (no batch): effect fires per set --
[effect] x=1 y=0 sum=0
[effect] x=1 y=0 sum=1
[effect] x=1 y=2 sum=1
[effect] x=1 y=2 sum=3
-- batch: x=10, y=20 --
   (inside batch: effect silent)
[effect] x=10 y=20 sum=30
-- batch exited: effect fired exactly once with latest values --
```

非 batch 下的多次触发（含中间态）正是批处理要消除的 "glitch"；
batch 内 x=10、y=20 两次赋值期间 Effect 零次触发，退出时仅触发一次，
且同时读到最新的 x=10、y=20（sum=30）。

## 测试结果（go test ./... -v）

```
=== RUN   TestBatchFiresEffectOnceWithLatestValues   # 验收边界用例
--- PASS: TestBatchFiresEffectOnceWithLatestValues (0.00s)
=== RUN   TestImmediatePropagationOutsideBatch
--- PASS: TestImmediatePropagationOutsideBatch (0.00s)
=== RUN   TestComputedSilentDuringBatchAndLazyAfter
--- PASS: TestComputedSilentDuringBatchAndLazyAfter (0.00s)
=== RUN   TestEffectOverComputedInBatch
--- PASS: TestEffectOverComputedInBatch (0.00s)
=== RUN   TestRepeatedSetsInBatchDeduplicate
--- PASS: TestRepeatedSetsInBatchDeduplicate (0.00s)
=== RUN   TestNestedBatchFlushesAtOutermostExit
--- PASS: TestNestedBatchFlushesAtOutermostExit (0.00s)
=== RUN   TestNoChangeNoNotify
--- PASS: TestNoChangeNoNotify (0.00s)
=== RUN   TestStopEffect
--- PASS: TestStopEffect (0.00s)
=== RUN   TestBatchPanicSkipsFlush
--- PASS: TestBatchPanicSkipsFlush (0.00s)
=== RUN   Example
--- PASS: Example (0.00s)
PASS
ok  	reactive
```

## 文件结构

- `reactive.go` — Scope / Signal / Computed / Effect / Batch 实现
- `reactive_test.go` — 单元测试（含验收边界用例）
- `example_test.go` — 可验证输出的 Example
- `cmd/demo/main.go` — 运行样例
