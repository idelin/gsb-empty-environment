# 多币种守恒结算图

纯 Python 3.11 标准库实现（`fractions.Fraction` 精确有理数，无第三方依赖）。

## 运行

```bash
python3 settle.py input.json     # 从文件读取
cat input.json | python3 settle.py   # 或从 stdin 读取（也可用 `-`）
python3 -m unittest test_settle -v   # 运行测试
```

## 输入格式（JSON）

```json
{
  "base_currency": "USD",
  "rates": {"USD": "1", "EUR": "11/10", "JPY": "0.007"},
  "positions": [
    {"account": "alice", "currency": "EUR", "amount": "-4.545"}
  ]
}
```

- `rates[c]`：1 单位币种 c 兑换的基准币种数量，字符串，支持精确十进制或 `"p/q"` 分数；基准币种汇率必须为 `"1"`。
- `amount`：该账户在该币种下的头寸（正=应收，负=应付），字符串精确解析；同一账户可多笔，按账户汇总。

## 处理流程

1. **精确折算**：每账户 `Σ amount × rate × 100` 得到基准币种“分”的精确有理数值。
2. **半偶舍入**：精确分值按 round-half-to-even 舍入为整数分净额。
3. **舍入账户**：`ROUNDING` 账户净额 = −Σ(各账户整数分净额)，使含舍入账户的总和严格为 0（总额守恒）；`ROUNDING` 非零时作为普通账户参与结算。
4. **最少笔数结算**：对 n 个净额非零账户，精确枚举全部 2ⁿ 个子集（子集 DP），求零和划分的最大块数 k；最少笔数 = n − k。每个零和组内贪心撮合产生实际转账。

## 输出格式（JSON）

- `positions[]`：每账户 `exact_cents`（精确分，整数或 `"p/q"`）、`rounded_cents`、`rounding_delta_cents`。
- `rounding_account`：舍入账户净额（分）。
- `conservation`：含舍入账户的净额总和及 `ok` 标志。
- `settlement`：
  - `transactions[]`：`{from, to, amount_cents}`，金额为正整数分，付款方净额恒为负、收款方恒为正，无零额交易；
  - `transaction_count` / `minimum_transaction_count` / `optimal`；
  - `zero_sum_partition`：达成最优的零和分组；
  - `certificate`：可核验最优性界——任意 t 笔结算的连通分量必须零和且 s 人分量至少需 s−1 笔，故 t ≥ n − k；方案恰达此下界即为最优。

## 复杂度与适用范围

子集 DP 为 O(3ⁿ) 精确枚举，适用于中小规模账户集（n ≲ 20）。示例数据均为合成数据。
