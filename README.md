# signalguard

响应式环路死锁保护器（Go，仅标准库）。在 `Signal` / `Effect` / `Computed`
响应式内核中内置守护器，检测并强制阻断「在 Effect/Computed 中直接或间接
写入自身依赖」的恶意逻辑，并输出详细的循环因果链报告。

## 守护规则

1. **调用栈环路**：observer（Effect/Computed）在执行中被同步重入
   （如两个 Computed 互相读取），立即 panic `*CircularSignalUpdate`。
2. **迭代阈值**：同一 observer 在一次 flush 代内被执行超过
   `maxIterations`（默认 50，可用 `WithMaxIterations` 调整）次时，
   第 51 次调度被拦截，panic `*CircularSignalUpdate`。

异常报告包含：触发源（Source）、迭代次数/阈值、因果链（谁写入了哪个
Signal 触发了谁）、以及触发源当前读取的依赖列表。

## 运行

需要 Go ≥ 1.18（使用泛型；开发环境为 go1.24.2）。

```sh
go test ./... -v   # 运行测试
go run ./cmd/demo  # 运行样例
```

## 样例输出（go run ./cmd/demo）

```
=== 1. healthy reactive graph ===
count=0 double=0
count=1 double=2
count=2 double=4
count=3 double=6

=== 2. malicious self-write loop (guard demo) ===
guard intercepted the dead loop:
CircularSignalUpdate: iteration limit exceeded: Effect[inc] keeps re-triggering itself (iterations: 51, limit: 50)
trigger source: Effect[inc]
causal chain:
  1. Effect[inc] triggered by Effect[inc] writing Signal[A]
  2. Effect[inc] forms a self-loop through Signal[A]
  3. Effect[inc] reads its own dependencies: Signal[A]
```

## 测试结果（go test ./... -v）

```
=== RUN   TestSelfWriteLoopInterceptedAtIterationLimit   # 验收场景：第 50 次迭代后拦截
--- PASS: TestSelfWriteLoopInterceptedAtIterationLimit (0.00s)
=== RUN   TestIndirectLoopAcrossEffects                  # 两个 Effect 经 A/B 互相触发
--- PASS: TestIndirectLoopAcrossEffects (0.00s)
=== RUN   TestComputedCallStackCycle                     # Computed 同步互读，调用栈环路
--- PASS: TestComputedCallStackCycle (0.00s)
=== RUN   TestHealthyUsageNoFalsePositive                # 健康用法无误报
--- PASS: TestHealthyUsageNoFalsePositive (0.00s)
=== RUN   TestBoundedSelfWriteBelowLimit                 # 阈值内的有限自增不拦截
--- PASS: TestBoundedSelfWriteBelowLimit (0.00s)
=== RUN   TestCustomIterationLimit                       # 自定义阈值生效
--- PASS: TestCustomIterationLimit (0.00s)
PASS
ok  	signalguard
```

## 验收场景对应

`TestSelfWriteLoopInterceptedAtIterationLimit`：Effect 内部读取 Signal A
并无条件写回 Signal A；守护器在阈值 50 处拦截（Effect 恰好执行 50 次，
第 51 次被阻断），抛出 `CircularSignalUpdate`，触发源指向 `Effect[inc]`，
因果链指出其通过 `Signal[A]` 形成自环。

## 文件结构

- `graph.go` — Graph 核心：依赖追踪、调度/flush、两条守护规则、因果链重建
- `signal.go` / `effect.go` / `computed.go` — 响应式原语
- `errors.go` — `CircularSignalUpdate` 异常与报告格式
- `guard_test.go` — 全部测试
- `cmd/demo/main.go` — 运行样例
