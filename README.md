# reactivestore — 持久化响应式存储与增量 Hydration 恢复器

纯 Go 标准库实现。将内存 Signal 状态树持久化为 JSON 检查点；仅带 `@persist`
注解的 Signal 被序列化；进程重启后加载检查点，持久化状态精确复原，所有派生
Effect 沿依赖链自动重新求值，临时状态安全重置。

## 结构

- `reactive/store.go` — `Store` / `Signal` / `Effect` / `Computed`，自动依赖追踪
- `reactive/checkpoint.go` — JSON 检查点序列化与增量 Hydration 恢复器
- `reactive/store_test.go` — 验收测试（`go test`）
- `cmd/demo/main.go` — 跨真实进程重启的运行样例

## 核心概念

| 规范概念 | Go 映射 |
|---|---|
| Signal 状态树 | `store.Signal(name, initial)` 注册的命名状态单元 |
| `@persist` 注解 | 注册选项 `reactive.Persist()`（`signal.Persistent()` 可查询） |
| 派生 Effect | `store.Effect(fn)`，读取即订阅，依赖变化自动重跑 |
| JSON 检查点 | `SaveCheckpointFile(path)`，仅含 `@persist` Signal |
| 增量 Hydration | `LoadCheckpointFile(path)`，按确定顺序逐个 `Set`，每步触发依赖链重对齐 |

```go
st := reactive.NewStore()
count := st.Signal("count", 0, reactive.Persist()) // 持久化
draft := st.Signal("draft", "")                    // 临时，重启后重置
doubled := st.Computed("doubled", func() any { return count.Get().(int) * 2 })
st.Effect(func() { fmt.Println("doubled =", doubled.Get()) })

st.SaveCheckpointFile("checkpoint.json") // 进程 1：写快照
st.LoadCheckpointFile("checkpoint.json") // 进程 2：增量恢复，Effect 自动重对齐
```

Hydration 返回 `HydrationReport{Restored, SkippedUnknown, SkippedTransient}`：
检查点中指向临时 Signal 或不存在的 Signal 的条目会被安全跳过。

## 运行

```sh
go test ./... -v        # 验收测试
go run ./cmd/demo save     # 进程 1：修改状态并写 checkpoint.json
go run ./cmd/demo restore  # 进程 2：加载检查点，观察依赖链重对齐
```

## 测试结果

```
=== RUN   TestCheckpointContainsOnlyPersistedSignals
--- PASS: TestCheckpointContainsOnlyPersistedSignals (0.00s)
=== RUN   TestRestartRestoresPersistedAndResetsTransient
--- PASS: TestRestartRestoresPersistedAndResetsTransient (0.00s)
=== RUN   TestDependencyChainRealignsAfterHydration
--- PASS: TestDependencyChainRealignsAfterHydration (0.00s)
=== RUN   TestHydrationSkipsUnknownAndTransientEntries
--- PASS: TestHydrationSkipsUnknownAndTransientEntries (0.00s)
=== RUN   TestHydrationIsIncremental
--- PASS: TestHydrationIsIncremental (0.00s)
=== RUN   TestUnsupportedCheckpointVersion
--- PASS: TestUnsupportedCheckpointVersion (0.00s)
=== RUN   TestCheckpointFileIsValidJSONOnDisk
--- PASS: TestCheckpointFileIsValidJSONOnDisk (0.00s)
PASS
ok  	reactivestore/reactive
```

## 样例输出（demo restore，进程 2）

```
== process #2: fresh boot, before hydration ==
Store{
  count:           @persist   0
  doubled:         transient  0
  draft:           transient
  user:            @persist   anonymous
}
== hydrated: restored=[count user] skippedUnknown=[] skippedTransient=[] ==
effect: user=anonymous doubled=0     <- 注册时初始求值
effect: user=anonymous doubled=84    <- 增量恢复 count=42，链式重算
effect: user=alice doubled=84        <- 增量恢复 user=alice
Store{
  count:           @persist   42
  doubled:         transient  84
  draft:           transient           <- 临时状态安全重置
  user:            @persist   alice
}
OK: persisted state restored, transient state reset, effects re-aligned
```
