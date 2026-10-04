# retrybudget

跨层重试的共享尝试预算库。多层重试（网关 → 服务 → 传输）如果各自独立重试，
请求数会按层数相乘放大（如每层 4 次尝试 → 最多 64 次请求）。本库把总尝试预算
放入 `context`，所有层共享，保证**真正发出的请求数**不超过预算。

## 核心概念

- `Budget`：线程安全的尝试令牌池，可带截止时间（`WithDeadline`）。
- `Token`：一次尝试的预留。状态机：`reserved → spent`（扣费）或
  `reserved → released`（退还）。只有真正发出请求时才 `Spend` 扣费；
  预留后未使用（如退避期间被取消）则 `Release` 自动退还。
- `NewContext`：把预算挂到 context 上；预算截止时间会与父 context 截止时间
  取较早者向下传播，到期自动取消所有层。
- `Do(ctx, policy, op)`：单层重试执行器。令牌沿 context 传递，内层复用外层
  为同一次逻辑尝试预留的令牌，不会每层各扣一份；已消耗的令牌才会触发内层
  重新 `Acquire`。预算错误（`ErrExhausted` / `ErrDeadlineExceeded`）不可重试，
  会沿层快速向上传播。

## 用法

```go
b := budget.New(5).WithDeadline(time.Now().Add(2 * time.Second))
ctx, stop := budget.NewContext(context.Background(), b)
defer stop()

err := budget.Do(ctx, budget.Policy{MaxRetries: 3}, func(ctx context.Context, tok *budget.Token) error {
    // 中间层直接调用内层 budget.Do，忽略 tok（预留会自动退还）
    // 最内层在真正发出请求前调用：
    if !tok.Spend() {
        return errors.New("reservation no longer valid")
    }
    return sendRequest(ctx)
})
```

## 验证

```sh
go test ./...        # 单元测试：分配/退还/截止传播/取消退还/三层上限
go run ./cmd/threelayer  # 三层全失败脚本：核验请求数被预算封顶（5 而非 64）
```
