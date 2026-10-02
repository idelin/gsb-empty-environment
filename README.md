# reactive：带事务批处理（Batch）的响应式系统

纯 Go 标准库实现的迷你响应式内核，支持 `Signal` / `Computed` / `Effect` / `Batch`。

## 核心语义

- **Signal**：可写数据源。`Set` 时若值变化则通知订阅者。
- **Computed**：派生只读值。依赖变化时仅标记为脏，读取时惰性重算并缓存。
- **Effect**：副作用。依赖变化时自动重跑；依赖在每次运行时自动重新收集（支持动态依赖）。
- **Batch**：事务批处理。块内所有 `Set` 引发的下游更新**静默挂起**：
  Computed 只标脏不重算，Effect 去重后挂入队列；最外层 batch **正常退出**时
  一次性刷新，每个 Effect 至多执行一次，且读到全部最新值。`panic` 时不刷新，
  深度正确恢复。

## 运行

```sh
go test ./... -v   # 测试
go run examples/main.go   # 样例
```

## 示例

```go
x := reactive.NewSignal(0)
y := reactive.NewSignal(0)

reactive.NewEffect(func() {
    fmt.Println(x.Get().(int), y.Get().(int))
}) // 立即打印: 0 0

reactive.Batch(func() {
    x.Set(10) // 不触发
    y.Set(20) // 不触发
}) // 此处仅触发一次，打印: 10 20
```
