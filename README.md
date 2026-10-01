# reactiveguard — 响应式环路死锁保护器

一个仅依赖 Go 标准库的响应式循环更新守护器。提供 `Signal` / `Computed` / `Effect`
响应式原语，并在运行时检测两类恶意/失控的响应式环路：

1. **调用栈环路**：`Computed`（或 `Effect`）在求值过程中直接或间接读取自身，
   立即阻断。
2. **迭代风暴**：`Effect` 直接或间接写入自身依赖（例如读取 Signal A 又写回
   Signal A），导致级联更新失控；当某节点在单次级联中的运行次数达到阈值
   （默认第 **50** 次迭代）时强制阻断。

阻断时抛出 `*CircularSignalUpdate`（panic），其中包含触发源与详细的循环因果链
报告；可用 `CatchCircular` 捕获为普通错误。

## 使用

```go
guard := reactiveguard.NewGuard(50)          // 迭代阈值 50
a := reactiveguard.NewSignal(guard, "A", 0)

err, caught := reactiveguard.CatchCircular(func() {
    guard.Effect("self-increment", func() {
        v := a.Get().(int) // 读取依赖 A
        a.Set(v + 1)       // 恶意逻辑：写回自身依赖
    })
})
if caught {
    fmt.Print(err.Error()) // CircularSignalUpdate 因果链报告
}
```

## 运行样例

```sh
GO111MODULE=on go run ./examples/deadlock
```

## 测试

```sh
GO111MODULE=on go test ./... -v
```
