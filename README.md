# 阶梯计费库（tiered-billing）

按月累计用量的阶梯计费，支持事件迟到与撤回；已出账单不可修改，只能通过追加调整账单更正。

## 模型约定

- **账期**：自然月（UTC），格式 `YYYY-MM`。
- **阶梯定价**：边际定价。累计用量落入哪个阶梯，该部分按该阶梯单价计费。
- **事件**：`UsageEvent { id, customerId, effectiveAt, quantity, recordedAt }`。
  - `effectiveAt` 决定所属账期与费率版本；`recordedAt` 是到达时间，可晚于账期关闭（迟到）。
  - 撤回通过 `retractEvent(eventId)` 完成，幂等；不物理删除事件。
- **费率版本**：`RateVersion { version, effectiveFrom, tiers }`。
  事件适用哪个版本由 `effectiveAt` 决定，与到达时间无关。
  账期中途切换费率时，每个事件用自己版本的阶梯表，
  但落在第几阶梯由全账期按 `effectiveAt` 排序后的累计量决定（排序确定，与到达顺序无关）。
- **账单**：`closeMonth` 开出原始账单（ORIGINAL）后不可变；
  `recalculate` 重算并与「已出账单净额」比对，有差额则追加一张调整账单（ADJUSTMENT，明细可为负），无差额返回 `null`。
- **金额**：整数分，避免浮点误差。

## 核心不变式

> 任意时刻，某账期全部账单（原始 + 所有调整）的净额 == 基于当前有效事件的一次性正确结算。

`test/verify.ts` 验证：

1. **跨阶梯撤回**：140 单位（跨档）出账 1800 分，撤回 80 单位事件后累计回落到第一阶梯，调整 -1200，净额 600。
2. **零用量**：空账期开出 0 元账单；出账后全部撤回，调整回 0。
3. **多次更正**：迟到事件 → 撤回 → 再迟到，连续 3 张调整账单，净额始终等于一次性结算。
4. **费率版本**：2 月才到达的 1/10 事件仍按 v1 费率计价。
5. **随机不变式**：50 轮混合迟到/撤回/重算，每步校验净额 == 一次性结算。
6. **边界**：未出账重算、重复事件 id、撤回未知事件、重复关账均报错；重复撤回幂等。

## 运行

```sh
# 需要任意 tsc（本仓库未锁定依赖）
tsc -p tsconfig.json && node dist/test/verify.js
```

## API 一览（src/billing.ts）

- `settle(events, month, rateVersions)` — 纯函数，一次性正确结算（重算与差额共用的事实来源）。
- `diffItems(newItems, baseItems)` — 按 (费率版本, 阶梯, 单价) 聚合的明细差额。
- `BillingEngine`
  - `recordEvent(event)` / `retractEvent(eventId, recordedAt)`
  - `closeMonth(customerId, month, issuedAt)` → 原始账单
  - `recalculate(customerId, month, issuedAt, reason?)` → 调整账单或 `null`
  - `netBilledCents` / `correctTotalCents` / `listBills`
