# 多币种守恒结算图

仅用 Python 3.11 标准库（`fractions.Fraction`、`json`、`unittest`）。

## 处理流程

1. **精确换算**：每笔债权按有理汇率（如 `"6/5"`）换算为基准币种等值，
   全程使用 `Fraction` 精确有理数，无浮点误差。
2. **半偶舍入**：各账户精确净额按 round-half-even 舍入到基准分（整数）。
3. **舍入账户**：各账户舍入后净额之和的相反数显式记入舍入账户
   `__rounding__`，使含舍入账户在内的净额总和为零（总额守恒）。
4. **最少笔数结算**：对净额非零账户，用分支限界穷举"零和分组"的最大
   划分数 g；最少笔数 = 活跃账户数 n − g。每组内贪心结算，k 个账户
   恰好 k−1 笔，金额为正整数分，付款方向与净额符号一致（负净额付款、
   正净额收款）。净额为零的账户不参与，绝不产生零额交易。

## 可核验最优性界

任何结算方案的交易连通分量必须各自零和；大小为 k 的分量至少需要
k−1 笔交易，因此总笔数 ≥ n −（零和分组数）。分组数通过穷举所有零和
划分取得最大值（分支限界，上界 = 当前组数 + 剩余账户数/2），故输出
中的 `lower_bound` 不可再降，`optimal: true` 表示方案达到该下界。

注意：分组枚举为指数级复杂度，适合账户数较少（约 ≤ 20）的场景。

## 输入格式（JSON）

```json
{
  "base": "USD",
  "rates": {"USD": "1", "EUR": "6/5", "JPY": "1/150"},
  "claims": [
    {"from": "alice", "to": "bob", "amount": "10", "currency": "EUR"}
  ]
}
```

- `rates[c]`：1 单位币种 c 兑换的基准币种数量，字符串有理数（`"6/5"`
  或 `"10.5"`），禁止浮点字面量；基准币种汇率必须为 1。
- `claims[]`：`from` 为付款方（债务人），`to` 为收款方（债权人），
  `amount` 为字符串精确数量，`currency` 为计价币种。

## 输出格式（JSON）

- `exact_nets_base`：各账户基准币种精确净额（有理数字符串）。
- `rounded_nets_cents`：半偶舍入后的整数分净额（含 `__rounding__`）。
- `rounding_account`：舍入账户名及其净额（分），计入总额守恒。
- `settlement.transactions`：`{from, to, amount}`，金额为正整数分。
- `settlement.optimality`：`transactions`、`lower_bound`、`optimal`
  及最优性证明说明。
- `conservation_ok`：含舍入账户的净额总和是否为零。
- `verification_errors`：内置核验结果，空数组表示全部通过。

## 运行

```sh
python3 settle.py example.json     # 退出码 0 表示核验通过
python3 -m unittest test_settle -v
```

所有示例数据均为合成数据。
