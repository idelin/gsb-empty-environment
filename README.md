# installment-calc

分期计算库（TypeScript）。按题内规则结算：**月初计息、按分舍入、先息后本**，支持提前还款（缩期 / 减额）、费率在指定期变更、计划生成与结清金额反算，最后一期自动吸收尾差。

## 精度保证

- 所有金额以**分**为单位的整数表示与输出；
- 费率解析为有理数（分子/分母），利息与年金月供的中间计算全部使用 `BigInt`，仅在最终结果按分舍入（四舍五入 half-up）；
- 全程无浮点近似累计。

## 规则说明

- **月初计息**：每期利息 = 上期末剩余本金 × 当期月费率，按分舍入；
- **先息后本**：月供先冲抵当期利息，剩余部分冲抵本金；
- **尾差吸收**：最后一期月供 = 剩余本金 + 当期利息；
- **费率变更**：`rateChanges` 指定生效期次，自该期起按剩余本金与剩余期数重算月供；
- **提前还款**：随指定期月供之后全额冲抵本金，
  - `reduce`（减额）：期数不变，重算后续月供；
  - `shorten`（缩期）：月供不变，本金提前清零即结清；
- **结清反算**：`settleAmount(input, atPeriod)` 返回在第 `atPeriod` 期月供到期前结清所需的金额 = 剩余本金 + 当期已计提利息。

## API

```ts
import { generateSchedule, settleAmount } from './src/index.ts';

const schedule = generateSchedule({
  principal: 1_000_000,        // 本金（分）
  periods: 12,                 // 期数
  monthlyRate: '0.005',        // 月费率（十进制字符串，避免浮点）
  rateChanges: [{ fromPeriod: 7, rate: '0.004' }],
  prepayments: [{ period: 4, amount: 100_000, mode: 'reduce' }],
});
// schedule.rows: [{ period, rate, payment, interest, principal, prepaidPrincipal, balance }]

const settlement = settleAmount(input, 6);
// { atPeriod, principal, interest, payoff }
```

## 运行

需要 Node.js >= 22.6（使用 `--experimental-strip-types` 直接运行 TS，无需构建）：

```sh
npm test                                        # 运行测试
node --experimental-strip-types examples/demo.ts # 运行示例
```
