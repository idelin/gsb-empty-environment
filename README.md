# batcher

并发安全的请求批处理器（仅 Go 标准库）。

## 语义

- **触发**：攒满 `MaxSize` 项，或自首项入队起等待 `MaxWait` 超时，先到先触发。
- **封存**：触发时当前待处理请求封存成批并异步执行；执行期间新提交的请求进入下一批次，绝不混入已封存批次。
- **取消**：封存前已 `cancel` 的请求从批中移除并收到 `ctx.Err()`；整批全取消时不调用执行函数。
- **失败**：执行函数返回 error 时，该错误映射给批内每个存活请求；返回结果数与批大小不符时每个请求收到 `ErrResultCountMismatch`。
- **注入**：任务函数（`Exec`）、时间（`NewTimer`）与失败均由调用方/测试注入，默认使用真实时钟。

## 使用

```go
b := batcher.New(batcher.Options{
    MaxSize: 64,
    MaxWait: 10 * time.Millisecond,
    Exec: func(ctx context.Context, items []any) ([]any, error) {
        // 批量处理，返回与 items 等长的结果
        return doBatch(ctx, items)
    },
})
ch := b.Submit(ctx, item) // <-chan batcher.Result，缓冲为 1，必收一个结果
r := <-ch
```

## 运行

```sh
go test -race ./...   # 测试（含竞态检测）
go run ./cmd/demo     # 运行样例
```
