# ordered

并发执行输入元素、严格按输入序输出的 Go 流水线（仅标准库）。

## 特性

- **并发执行，有序交付**：`Workers` 限制并发度；后项先完成也不会越过慢首项。
- **有界缓冲 + 背压**：`MaxBuffered` 限制"已完成未交付"的结果数量；首项慢时，
  缓冲占满后 worker 阻塞，进而暂停从输入通道取数（预取上界 = `Workers + MaxBuffered + 1`），
  无限输入流不会被无界预取。
- **两种失败策略**：
  - `FailFast`：任意任务失败立即输出错误、取消其余工作、回收未交付结果；
  - `FailInOrder`：按序交付，失败项的错误在其序号位置输出，之后终止并回收后续结果。
- 任务函数、耗时与失败均由调用方注入（`func(context.Context, I) (O, error)`），便于测试。

## 用法

```go
out := ordered.Process(ctx, in, fn, ordered.Options{
    Workers: 4, MaxBuffered: 2, Policy: ordered.FailInOrder,
})
for r := range out { /* r.Value / r.Err，严格按输入序 */ }
```

## 运行

```sh
go run ./cmd/demo        # 运行样例
go test -race -v ./...   # 竞态检测 + 全部测试
```
