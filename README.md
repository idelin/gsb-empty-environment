# ledger

Go 双重记账账本库：以整数分（`Money`，正借负贷）记录借贷平衡凭证。

## 规则

- **借贷平衡**：每张凭证的分录合计必须为零，否则整批拒绝。
- **批次原子性**：一个 `Batch` 全成全败，任一张凭证非法则整批不落账。
- **冲销**：`Voucher.Reverses` 引用原凭证 ID，分录必须逐行相反（同账户、金额取反，
  行序无关）。二次冲销、对冲销凭证再冲销均被拒绝。
- **冲销可先到**：原凭证尚未到账时冲销先挂起（pending），原凭证提交的同一次
  提交中释放校验；届时不合法的挂起冲销单独拒绝，不影响原凭证落账。
- **幂等**：`RequestID` 相同且内容一致的重复提交直接返回首次结果、不重复记账；
  同 ID 异内容返回 `ErrRequestConflict`。只有成功落账的批次才占用请求 ID。

## 不变量验证

`Ledger.Verify()` 做两项检查：

1. **余额守恒**：所有账户余额之和恒为零。
2. **重放一致**：从空账本重放已提交批次日志，得到的余额、冲销标记、
   挂起队列必须与实时状态完全一致。

## 示例

```go
l := ledger.New()
l.Submit(ledger.Batch{RequestID: "r1", Vouchers: []ledger.Voucher{
    {ID: "v1", Lines: []ledger.Line{
        {Account: "cash", Amount: 10000},
        {Account: "revenue", Amount: -10000},
    }},
}})
// 冲销 v1
l.Submit(ledger.Batch{RequestID: "r2", Vouchers: []ledger.Voucher{
    {ID: "v1-rev", Reverses: "v1", Lines: []ledger.Line{
        {Account: "cash", Amount: -10000},
        {Account: "revenue", Amount: 10000},
    }},
}})
if err := l.Verify(); err != nil { ... }
```

## 测试

```sh
go test -race ./...
```
