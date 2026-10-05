# retrybudget

跨层共享的重试尝试预算库。多层嵌套重试如果各自独立计数，请求量会按层数放大
（三层各重试 n 次就是 n³ 次请求）。`retrybudget` 让所有层共享同一个尝试预算，
把真实发出的请求总数硬性封顶。

## 核心概念

- **Budget**：共享的尝试预算（最大尝试次数 + 可选截止时间），goroutine 安全。
- **Token**：一次尝试的预留令牌。只有真正发出请求时才 `Spend` 扣费；
  若请求尚未发出就被放弃（如 context 取消），必须 `Refund` 退还给共享池。
- **截止时间传播**：`ContextWithBudget` 把预算挂到 context 上，并把预算截止时间
  与父 context 截止时间取较早者传播下去。

## API

```go
b := retrybudget.New(5, retrybudget.WithTimeout(time.Minute))
ctx := retrybudget.ContextWithBudget(context.Background(), b)

// 叶子：真正发请求的地方，用 Issue 扣费
leaf := func(ctx context.Context) error {
    return retrybudget.Issue(ctx, func(ctx context.Context) error {
        return httpCall(ctx)
    })
}

// 各层重试循环：Do 本身不扣费，只按错误是否可重试决定是否再来一次
err := retrybudget.Do(ctx, func(ctx context.Context) error {
    return retrybudget.Do(ctx, leaf) // 可任意嵌套
})
```

- `Issue`：申请令牌 → 检查 context → 扣费 → 发请求；取消时自动退还令牌。
- `Do`：遇到 `ErrBudgetExhausted`、`ErrDeadlineExceeded` 或 context 取消时停止，
  否则只要 fn 返回错误就重试。没有预算时 `Do` 只执行一次（重试必须纳入预算）。

## 核验

```sh
go test -race ./...        # 含三层失败、取消退还、并发不超上限等测试
go run ./examples/threelayer  # 三层全失败脚本：预算 5，实际请求恰好 5 次
```
