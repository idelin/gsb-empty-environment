# signalstore — 持久化响应式存储与增量 Hydration 恢复器

纯 Go 标准库实现。将内存 Signal 状态树持久化为本地 JSON 检查点；
仅序列化带 `@persist` 注解的 Signal；进程重启后加载快照，持久化状态
完美复原，所有派生 Effect / Computed 自动重新对齐，临时状态安全重置。

## 结构

- `reactive/reactive.go` — Signal / Effect / Computed，自动依赖追踪
- `reactive/store.go` — Store、`@persist` 注解解析、JSON 检查点读写（原子写）
- `reactive/reactive_test.go` — `go test` 验收测试
- `cmd/demo/main.go` — 两阶段运行样例（真实进程重启）

## 核心 API

```go
store := reactive.NewStore()
counter := reactive.NewSignal(store, "@persist counter", 0) // 持久化
session := reactive.NewSignal(store, "session", "<none>")   // 临时，不出现在检查点
doubled := reactive.NewComputed(store, "doubled", func() int { return counter.Get() * 2 })
reactive.NewEffect(func() { fmt.Println(counter.Get(), doubled.Get()) })

store.SaveCheckpoint("checkpoint.json") // 仅含 @persist 信号
store.LoadCheckpoint("checkpoint.json") // 增量 hydration：逐个 Set，依赖链自动重对齐
```

## 运行

```sh
go test ./... -v        # 验收测试
go run ./cmd/demo       # 第 1 次：写检查点
go run ./cmd/demo       # 第 2 次：模拟重启，hydration 恢复
```

## 测试结果（go1.24.2）

```
=== RUN   TestEffectRerunsOnDependencyChange
--- PASS: TestEffectRerunsOnDependencyChange (0.00s)
=== RUN   TestComputedChainReevaluates
--- PASS: TestComputedChainReevaluates (0.00s)
=== RUN   TestCheckpointContainsOnlyPersistedSignals
--- PASS: TestCheckpointContainsOnlyPersistedSignals (0.05s)
=== RUN   TestHydrationRestoresStateAndRealignsEffects
--- PASS: TestHydrationRestoresStateAndRealignsEffects (0.00s)
=== RUN   TestHydrationIgnoresUnknownSignals
--- PASS: TestHydrationIgnoresUnknownSignals (0.00s)
=== RUN   TestHydrationRejectsBadVersion
--- PASS: TestHydrationRejectsBadVersion (0.00s)
=== RUN   TestStructSignalRoundTrip
--- PASS: TestStructSignalRoundTrip (0.00s)
PASS
ok  	signalstore/reactive	0.376s
```

## 样例输出

第 1 次运行（写检查点，注意检查点中只有 `counter` 和 `user`，临时 `session` 被排除）：

```
== phase 1: live process ==
  [effect] counter=0 doubled=0 user="anonymous" session="<none>"
-- mutating state --
  [effect] counter=21 doubled=0 user="anonymous" session="<none>"
  [effect] counter=21 doubled=42 user="anonymous" session="<none>"
  [effect] counter=21 doubled=42 user="alice" session="<none>"
  [effect] counter=21 doubled=42 user="alice" session="sess-7f3c (transient)"
-- checkpoint written to checkpoint.json --
{
  "version": 1,
  "created_at": "2026-10-02T03:08:21.721389Z",
  "signals": {
    "counter": 21,
    "user": "alice"
  }
}
```

第 2 次运行（进程重启后 hydration，派生链重对齐，临时状态重置）：

```
== phase 2: restarted process (fresh memory) ==
  [effect] counter=0 doubled=0 user="anonymous" session="<none>"
-- incremental hydration from checkpoint --
  [effect] counter=21 doubled=42 user="anonymous" session="<none>"
  [effect] counter=21 doubled=42 user="alice" session="<none>"
-- final state --
  persisted counter = 21 (restored)
  persisted user    = "alice" (restored)
  derived  doubled  = 42 (re-aligned by dependency chain)
  transient session = "<none>" (safely reset)
```
