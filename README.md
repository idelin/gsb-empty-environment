# suspense — 异步资源加载与响应式 Suspense 状态机

纯 Go 标准库实现，无第三方依赖。

## 核心概念

- **`Signal[T]`**：响应式数据源（如查询参数），`Set` 时通知订阅者。
- **`Resource[P, T]`**：响应式资源绑定器，把异步 `fetch(ctx, P) (T, error)` 绑定到参数信号。
- **`Snapshot[T]`**：向下游传播的不可变状态快照，状态机为 `Pending → Success | Error`。

## 行为保证

1. **Pending 信标**：每次请求发出时，立即向下游广播 `Pending`，数据就绪前下游渲染加载态。
2. **竞争作废**：每次新请求使序号 `seq` 单调递增，并 `cancel` 旧请求的 `context`；
   旧 Promise 迟到返回时通过序号比对被丢弃（`Discarded()` 计数），绝不污染下游。
3. **有序传播**：下游订阅回调按 `Seq` 严格递增顺序同步触发。

## 使用

```go
params := suspense.NewSignal("A")
res := suspense.NewResource(searchAPI) // func(ctx, string) (string, error)

res.Subscribe(func(s suspense.Snapshot[string]) { /* 渲染 Pending/Success/Error */ })
res.Track(params)   // 立即加载，并跟随参数变化自动重新加载

params.Set("B")     // 自动作废在飞的旧请求
snap, _ := res.Await(context.Background()) // 阻塞至 Success/Error
```

## 运行

```sh
go test ./... -v -count=1   # 测试（含竞争验收用例）
go test ./... -race         # 竞态检测
go run ./cmd/example        # 运行样例
```

## 测试覆盖（suspense_test.go）

| 用例 | 验证点 |
|---|---|
| `TestPendingThenSuccess` | 状态机 Pending → Success |
| `TestErrorState` | 错误传播为 Error 状态 |
| `TestPendingBeaconPropagates` | 数据就绪前向下游传播 Pending 信标 |
| `TestRaceDiscardsStalePromise` | **验收边界**：A→B 快速切换，旧 Promise 作废，下游稳定呈现 B |
| `TestOldPromiseResolvingLastIsDiscarded` | 忽略 ctx 取消的顽固旧 Promise 也被序号检查兜底作废 |
| `TestAwaitContextCancel` | Await 尊重调用方 context |
