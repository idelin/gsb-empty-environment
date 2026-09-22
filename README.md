# batcher

将并发提交的独立请求聚合成批，由用户注入的执行函数统一处理；每项独立拿回自己的结果。

## 触发规则

- **批量上限**：批内项数达到 `maxSize` 立即封存并执行。
- **首项等待时限**：批未满时，首项等待超过 `maxWait` 也会封存并执行。
- 批一旦封存，后续提交进入新批，绝不混入已封存批次。

## 取消与失败

- `Submit` 的 `ctx` 取消后，该项会从未封存的批中移除（已封存的批在执行前过滤）。
- 整批都被取消时，执行函数不会被调用。
- 执行函数返回 error 时，该 error 映射到批内每个存活请求。
- 执行函数返回的结果数与项数不一致时，每项收到 `ErrResultCountMismatch`。

## 可注入性

- 任务函数：`ExecFunc[I, O]`，泛型，输入一批 item，按序返回每项结果。
- 时间：`Clock` 接口（`NewTimer`），测试用手动推进的假时钟注入；传 `nil` 使用真实时间。
- 失败：由测试中的执行函数注入 error。

## 使用

```go
b := batcher.New(4, 80*time.Millisecond, exec, nil)
defer b.Close()
res, err := b.Submit(ctx, item) // 阻塞直到所在批执行完毕或 ctx 取消
```

## 运行

```sh
go test -race -v ./...   # 测试（含竞态检测）
go run ./cmd/demo        # 运行样例
```
