# netsec — 借贷池闪贷约束组件

`example.org/netsec/q0455` 提供可被 Go 借贷池直接导入的闪贷约束组件。它不持有资金，而是在池修改余额、抵押物或价格之前校验操作，并把同一账户、同一区块内的借款、还款、抵押、价格更新和清算聚合到同一个上下文中。

## 防护语义

- **同块隔离**：账户在同一区块内只能开一笔闪贷；同一区块内若已有闪贷、价格更新、清算或抵押操作，后续组合操作会被拒绝。借款、还款后再尝试抵押、改价或清算同样会被拒绝。
- **价格冷却**：同一资产的价格更新受 `PriceCooldownBlocks` 限制；清算不能使用冷却窗口内刚更新的价格。可选的 `PriceDeviationBps` 限制单次价格跳变。
- **清算冷却**：同一仓位在 `LiquidationCooldownBlocks` 内不能再次清算；同块重复清算返回 `ErrDuplicateLiquidation` 并记录标记。
- **异常利润**：还款时可申报 `Profit`，超过 `MaxProfitBps` 的利润返回 `ErrAbnormalProfit` 并记录标记；未申报时，超出应还金额的部分也会按利润检查。还款不得低于本金加费用，且默认必须同块归还。
- **失败全额回滚**：`RunAtomic`、`AtBlock` 和 `RunFlashLoan` 在回调返回错误或 panic 时恢复完整协议状态。单次操作也先完成全部校验再写入状态。
- **区块不可伪造**：操作请求中的 `Block` 只能与当前区块一致，不能通过传入更大的区块号跳过冷却；推进区块只能由池调用 `BeginBlock`。
- **风险标记**：拒绝操作返回带 `Code`/`Flags` 的 `*Violation`，同时写入追加式检测账本。检测账本属于安全遥测，不随协议状态回滚，便于事后告警。

## 快速接入

```go
import (
	"math/big"

	"example.org/netsec/q0455"
)

guard := netsec.NewGuard(
	netsec.WithMaxFeeBps(10),
	netsec.WithPriceCooldownBlocks(3),
	netsec.WithLiquidationCooldownBlocks(3),
)

// 由共识/执行层在每个区块调用一次。
if err := guard.BeginBlock(blockNumber); err != nil {
	return err
}

// 整个交易使用一个原子作用域；任一步失败都会恢复所有 guard 状态。
err := guard.RunAtomic(func() error {
	loan, err := guard.BeginFlashLoan(netsec.FlashLoanRequest{
		Account: msgSender,
		Asset:   "USD",
		Amount:  big.NewInt(1_000_000),
		FeeBps:  5,
	})
	if err != nil {
		return err
	}

	// 在这里执行池的借贷/兑换逻辑。若调用价格更新、清算或抵押操作，
	// guard 会根据同一账户同一区块的上下文拒绝组合攻击。

	return guard.RepayFlashLoan(netsec.RepayRequest{
		LoanID: loan.ID,
		Amount: loan.Owed(),
		Profit: nil, // 可选；超过 MaxProfitBps 会被拒绝
	})
})
```

`NewGuard(Config{...})`、`NewGuardWithConfig(Config{...})` 也可用；`Config` 中的零值字段会保留安全默认值，必须用对应的 `With...` 选项才能显式关闭某项保护（例如 `WithPriceCooldownBlocks(0)`）。

也提供 `BorrowFlashLoan`、`DepositCollateral`、`WithdrawCollateral`、`RecordPrice`、`SetPrice` 和 `Liquidate` 等便捷入口。

## 上下文与状态查询

`Context(account)` 返回当前区块的账户上下文，包含：

- `Loans` / `Repayments` / `Collateral`
- `PriceUpdates` / `Liquidations`
- `Flags`（包含追加式检测账本中的风险标记）

`Context(account, block)` 可查询指定历史区块；`ContextsAt(block)` 返回整个区块的账户上下文。`ActiveLoan`、`Price`、`Detections` 和 `Flags(account, block)` 提供只读快照。

池应传入规范的资产标识和稳定的仓位标识。重复清算同时按 `PositionID` 与 `borrower + collateralAsset + debtAsset` 复合键校验，调用方改名不能绕过同块重复清算检测。池还应把实际实现的利润填入 `RepayRequest.Profit`；组件看不到池外余额，未申报时只能以超额还款近似利润。

## 回滚与快照

```go
before := guard.Snapshot()
if err := executeTransaction(); err != nil {
	// AtBlock/RunAtomic 已自动回滚；自定义外层事务边界时可显式 Restore。
	_ = guard.Restore(before)
}
```

`Restore` 只恢复协议状态；检测账本保持追加，避免攻击尝试因回滚而消失。

## 验证

`go test ./...` 覆盖：

- 合法同块闪贷（借出、计费、归还、关闭）
- 同块借出后改价/清算的组合攻击及全额回滚
- 跨账户改价后利用新价格清算
- 价格更新冷却与清算价格成熟窗口
- 重复清算标记与清算冷却
- 异常利润、超额利润边界
- 跨块还款、伪造未来区块、重复闪贷、闪贷期间抵押
- panic 回滚和账户/区块上下文隔离
