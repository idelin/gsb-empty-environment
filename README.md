# 结算金额被改到了另一个商户

商户结算按终端号入账，攻击者替换终端号或商户号，把结算款打到自己的账户。请实现供Go结算服务调用的可导入结算归属组件。终端、商户、收单机构与结算账户在签约时绑定，结算前逐项核对；终端换绑需审核并生效延迟。跨商户、未签约终端与异常金额拒绝。用终端替换、商户改绑、跨机构入账、合法结算和审核验证款项只达签约账户。

## 实现说明

包 `example.org/netsec/q0465` 提供可直接导入的结算归属组件。组件只负责“这笔结算应该付给哪个签约账户”，不在内部发起银行转账。

核心规则：

- 签约时写入不可变的终端归属元组：`TerminalID + MerchantID + AcquirerID + AccountID + Currency`。
- 结算前重新检查终端、商户、收单机构、账户及其签约关系，拒绝未签约终端、跨商户、跨机构和账户替换。
- `Settlement.AccountID` 只从签约绑定派生；请求中的 `AccountID` 仅用于比对，比对不一致即拒绝，绝不作为付款目标。
- 非法金额、低于/超过限额的金额、币种不符和已停用合约均拒绝。
- 终端换绑不能直接覆盖。必须先提交完整的新归属元组，经审核后进入等待期，到期时才原子切换。
- 同一条结算的 `Reference` 可幂等重放；同一引用携带不同终端、商户、机构、账户或金额时拒绝。

## 使用示例

```go
svc := netsec.NewAttributor(
    netsec.WithRebindDelay(24*time.Hour),
    netsec.WithMaxAmount(50_000_000), // 以分为单位
)

_ = svc.RegisterAcquirer(netsec.Acquirer{ID: "acq-1"})
_ = svc.RegisterMerchant(netsec.Merchant{ID: "merchant-1"})
_ = svc.RegisterSettlementAccount(netsec.SettlementAccount{
    ID: "account-1", MerchantID: "merchant-1", AcquirerID: "acq-1", Currency: "CNY",
})
_ = svc.SignTerminal(netsec.TerminalBinding{
    TerminalID: "terminal-1", MerchantID: "merchant-1",
    AcquirerID: "acq-1", AccountID: "account-1", Currency: "CNY",
})

settlement, err := svc.Attribute(netsec.SettlementRequest{
    Reference: "batch-1-row-1", TerminalID: "terminal-1",
    MerchantID: "merchant-1", AcquirerID: "acq-1", AmountCents: 12_500,
})
if err != nil {
    // 拒绝入账。
}
payTo := settlement.AccountID // 唯一可信的入账账户
```

也可以把归属结果直接交给实现了 `PayoutGateway` 的付款适配器：

```go
settlement, err = svc.Settle(ctx, request, gateway)
```

付款适配器只能使用 `settlement.AccountID`，并应以 `settlement.Reference` 实现幂等。

## 终端换绑

```go
review, err := svc.RequestRebind("terminal-1", netsec.TerminalBinding{
    TerminalID: "terminal-1", MerchantID: "merchant-2",
    AcquirerID: "acq-2", AccountID: "account-2",
}, "operations", netsec.RebindOptions{Reason: "门店转让"})

approved, err := svc.ApproveRebind(review.ID, "reviewer-42")
// approved.EffectiveAt 前仍按旧签约账户结算；
// 到达 approved.EffectiveAt 后，完整的新归属元组才生效。
```

换绑的目标账户必须已签约给目标商户和收单机构。审核拒绝、等待期内取消都会保留旧绑定。

组件会记录审核人和审核时间，但不负责认证审核人身份；结算服务必须先把经过认证的操作员身份传给 `ApproveRebind`/`RejectRebind`。

## 验证

```sh
go test ./...
go test -race ./...
go vet ./...
```
