# 多币种联合授信时态敞口监控器（Bitemporal Exposure Monitor）

集团授信额度与子公司敞口的**双时态**监控器：授信协议有效期与汇率波动双重影响敞口折算；
可重现历史任意时间点集团整体敞口是否击穿预警线，并追溯担保物价值变动引发的质押率重算。

纯 TypeScript、零依赖，使用 Node.js 内置测试运行器（`node:test`）验证。

## 运行环境

- Node.js >= 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，推荐 23.x；仓库含 `.nvmrc`）
- 无需 `npm install`（无任何第三方依赖）

```bash
nvm use            # 或自行切换到 Node 23
npm test           # node --experimental-strip-types --test
npm run demo       # 运行样例场景
```

## 双时态模型

每条事实记录携带两个时间维度（`src/bitemporal.ts`）：

- **valid time（业务时间）** `[validFrom, validTo)`：事实在业务世界中生效的区间，
  授信协议的 `validFrom/validTo` 即协议有效期；
- **transaction time（系统时间）** `recordedAt`：系统何时得知该事实。

追溯修正（如调整历史借款）不覆盖旧记录，而是以更晚的 `recordedAt` 追加新记录。
`evaluate(validTime, txTime)` 重现"txTime 时点系统所认知的、validTime 时点的集团敞口"：

- 各子公司借款按 **validTime 时点生效的汇率**折算为基准币种后汇总；
- 与 validTime 时点生效的授信协议预警线（额度 × warningRatio）比较，`>=` 即触碰告警；
- 担保物价值变动 / 借款变动 / 汇率变动触发质押率（借款 ÷ 担保物价值）重算，
  变化时追加 `PledgeTraceEntry` 追溯记录（含变动原因），超过上限输出 `PLEDGE_RATIO_EXCEEDED`。

## 文件结构

- `src/bitemporal.ts` — 双时态存储内核（追加式记录 + asOf 点查 + 履历审计）
- `src/types.ts` — 领域模型：CreditLine / Loan / FxRate / Collateral
- `src/monitor.ts` — `ExposureMonitor`：敞口折算、预警线校验、质押率重算追溯
- `src/demo.ts` — 运行样例（完整时间线场景）
- `test/monitor.test.ts` — `node:test` 验收与特性测试

## 验收边界

集团总授信 1000 万（预警线 70% = 700 万）；子公司 A 借款 600 万；
t=30 追溯上调 A 自 t=10 起的历史借款为 700 万；监控器重现 t=30 时总敞口触碰警戒线并输出告警。
对应测试 `test/monitor.test.ts` 第一条用例，关键断言：

```ts
monitor.recordLoan({ subsidiary: 'A', amount: 7_000_000, currency: 'CNY' }, 10, Infinity, 30);
const replay = monitor.evaluate(30, 30);
replay.totalExposure; // 7_000_000
replay.warningLine;   // 7_000_000
replay.breached;      // true，alerts 含 WARNING_LINE_BREACHED
monitor.evaluate(30, 29).totalExposure; // 6_000_000 —— 修正前的历史视图不受影响
```

## 测试结果

```
✔ 验收：时间30追溯上调A的历史借款为700万，重现时间30总敞口触碰警戒线并告警
✔ 多币种：汇率波动影响敞口折算，历史时点取当时生效的汇率
✔ 协议有效期：协议到期后评估输出 AGREEMENT_INACTIVE 告警
✔ 担保物价值变动引发质押率重算并留下追溯记录
✔ 缺失汇率时输出 FX_RATE_MISSING 且该笔敞口不计入总额
ℹ tests 5  ℹ pass 5  ℹ fail 0
```

## 样例输出（`npm run demo` 节选）

```
=== t=26 担保物重估下跌至 650 万 (validTime=26, txTime=26) ===
总敞口: 6,362,500 CNY | 利用率: 63.63% | 触碰预警线: 否
  质押 A: 借款 6,000,000 / 担保物 6,500,000 = 质押率 92.31%

--- t=30 追溯修正：A 自 t=10 起的历史借款 600 万 -> 700 万 ---

=== 重现 t=30（修正后系统认知） (validTime=30, txTime=30) ===
总敞口: 7,362,500 CNY | 利用率: 73.63% | 触碰预警线: 是
  [告警 WARNING_LINE_BREACHED] 集团总敞口 7362500.00 CNY 触碰预警线 7000000.00（授信额度 10000000.00）
  [告警 PLEDGE_RATIO_EXCEEDED] A 质押率 107.69% 超过上限 100.00%

=== 对照：t=30 时点但 txTime=29（修正前系统认知） ===
总敞口: 6,362,500 CNY | 利用率: 63.63% | 触碰预警线: 否

=== 质押率重算追溯日志 ===
  [validTime=12, txTime=12] A 质押率 N/A -> 75.00%（原因: 首次评估）
  [validTime=26, txTime=26] A 质押率 75.00% -> 92.31%（原因: 担保物价值变动）
  [validTime=30, txTime=30] A 质押率 92.31% -> 107.69%（原因: 借款敞口变动）
```

注：质押率追溯日志按评估调用顺序追加；用不同 txTime 重现历史时会如实记录各次重算，
完整呈现"系统认知随修正演化"的过程。
