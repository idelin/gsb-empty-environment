# 对账记录被重复确认

对账服务按交易号核销，攻击者重放同一确认让一笔收款被核销两次，账面出现差额。请实现供Go对账服务调用的可导入核销组件。确认绑定交易号、金额、币种与账期，核销状态原子推进且不可回退；重复确认只返回原结果。跨账期与部分金额拒绝。用重复确认、金额不符、跨账期、合法核销和冲正验证每笔只核销一次。

## 核销组件 `writeoff`

可导入子包 `example.org/netsec/q0428/writeoff` 提供按交易号核销的幂等组件。

保证：

- 确认与冲正均绑定交易号、金额、币种、账期四要素，跨账期、跨币种、部分金额、超额金额一律拒绝。
- 状态机 `open -> written-off -> reversed` 单向原子推进，永不回退。
- 以 `ConfirmID` / `ReversalID` 作为幂等键，重复消息返回原结果（`Replayed=true`）且不二次记账；幂等键载荷变化返回 `ErrConflict`。
- 每笔收款最多核销一次，重复的不同确认返回 `ErrAlreadyWrittenOff`，冲正后返回 `ErrAlreadyReversed`。
- `Store` 端口封装读-校验-写事务，`MemoryStore` 用互斥锁保证进程内并发安全；数据库实现应在单事务内加行锁执行回调。

快速使用：

```go
import "example.org/netsec/q0428/writeoff"

svc := writeoff.NewService(writeoff.NewMemoryStore())
ctx := context.Background()

svc.Register(ctx, writeoff.Payment{TxnID: "TXN-1", Amount: 1000, Currency: "CNY", Period: "2026-11"})

confirm := writeoff.Confirmation{
    ConfirmID: "C-1",
    TxnID:     "TXN-1",
    Amount:    1000,
    Currency:  "CNY",
    Period:    "2026-11",
}
result, err := svc.Confirm(ctx, confirm) // 首次核销
result, err = svc.Confirm(ctx, confirm)  // 重放：Replayed=true，返回原结果

reversed, err := svc.Reverse(ctx, writeoff.Reversal{
    ReversalID: "R-1",
    TxnID:      "TXN-1",
    Amount:     1000,
    Currency:   "CNY",
    Period:     "2026-11",
}) // 冲正：written-off -> reversed
```

测试覆盖重复确认、金额不符、跨账期、合法核销、冲正及并发重放，运行：

```sh
go test -race -count=1 ./...
```
