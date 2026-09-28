#!/usr/bin/env python3
"""多币种守恒结算图（multi-currency conservation settlement）。

- 用精确有理数（fractions.Fraction）汇率把各币种债权折算为基准币种的“分”。
- 对每账户的精确分值做半偶舍入（round half to even）得到整数分净额。
- 舍入差额显式记入 ROUNDING 账户，使全部整数分净额之和严格为 0（守恒）。
- 对净额非零账户求最少笔数结算方案：
    最少笔数 = n -（把账户划分为零和子集的最大块数）
  用子集 DP 精确枚举全部 2^n 个子集，给出可核验的最优性界。
- 每个零和组内贪心撮合产生实际转账：金额为正整数分，方向与净额符号一致
  （净额为负者付款，净额为正者收款），绝不产生零额交易。

输入（JSON，文件路径作为参数，或从 stdin 读取）：
{
  "base_currency": "USD",
  "rates":  {"USD": "1", "EUR": "11/10", "JPY": "0.007"},   # 1 单位币种 = rate 基准币种
  "positions": [
    {"account": "alice", "currency": "EUR", "amount": "100.25"},
    ...
  ]
}
amount / rate 均为字符串，按精确十进制或 "p/q" 分数解析，杜绝浮点误差。
同一账户可出现多笔头寸，按账户汇总。

输出（JSON 到 stdout）：见 main() 末尾结构，含每笔精确分、舍入增量、
ROUNDING 账户、守恒校验、结算交易列表、笔数及最优性证明信息。
"""

from __future__ import annotations

import json
import sys
from fractions import Fraction

ROUNDING_ACCOUNT = "ROUNDING"


def parse_number(text) -> Fraction:
    """把字符串精确解析为 Fraction。支持 "p/q" 与十进制（含科学计数法）。"""
    if isinstance(text, (int,)):
        return Fraction(text)
    if not isinstance(text, str):
        raise ValueError(f"数字必须是字符串，得到: {text!r}")
    s = text.strip()
    if "/" in s:
        num, den = s.split("/", 1)
        return Fraction(int(num.strip()), int(den.strip()))
    # Fraction 可直接精确解析十进制/科学计数字符串
    return Fraction(s)


def round_half_even(x: Fraction) -> int:
    """对精确有理数做半偶舍入（round half to even）到整数。"""
    floor = x.numerator // x.denominator  # 数学向下取整（负数也正确）
    rem = x - floor
    half = Fraction(1, 2)
    if rem < half:
        return floor
    if rem > half:
        return floor + 1
    # 恰好一半：取偶数
    return floor if floor % 2 == 0 else floor + 1


def frac_str(x: Fraction) -> str:
    """Fraction 的可读表示：整数或 "p/q"。"""
    return str(x.numerator) if x.denominator == 1 else f"{x.numerator}/{x.denominator}"


def build_net_positions(spec: dict) -> dict:
    """折算 + 半偶舍入 + ROUNDING 账户。返回完整中间结果字典。"""
    base = spec["base_currency"]
    rates = {c: parse_number(r) for c, r in spec["rates"].items()}
    if rates.get(base) != 1:
        raise ValueError(f"基准币种 {base} 的汇率必须为 1")

    exact_cents: dict[str, Fraction] = {}
    for pos in spec["positions"]:
        acct = pos["account"]
        cur = pos["currency"]
        if cur not in rates:
            raise ValueError(f"缺少币种 {cur} 的汇率")
        amount = parse_number(pos["amount"])
        exact_cents[acct] = exact_cents.get(acct, Fraction(0)) + amount * rates[cur] * 100

    positions_out = []
    nets: dict[str, int] = {}
    for acct in sorted(exact_cents):
        exact = exact_cents[acct]
        rounded = round_half_even(exact)
        nets[acct] = rounded
        positions_out.append({
            "account": acct,
            "exact_cents": frac_str(exact),
            "rounded_cents": rounded,
            "rounding_delta_cents": frac_str(Fraction(rounded) - exact),
        })

    # 舍入差额显式记入 ROUNDING 账户，使整数分净额总和严格为 0
    rounding_net = -sum(nets.values())
    if rounding_net != 0:
        nets[ROUNDING_ACCOUNT] = rounding_net

    return {
        "base_currency": base,
        "positions": positions_out,
        "rounding_account": {"account": ROUNDING_ACCOUNT, "net_cents": rounding_net},
        "nets": nets,
    }


def max_zero_sum_partition(nets_list: list[int]) -> tuple[int, list[list[int]]]:
    """精确枚举全部子集（子集 DP），求零和划分的最大块数及一个最优划分。

    返回 (最大块数, 划分)。划分用索引列表的列表表示。
    复杂度 O(3^n)，适用于精确枚举规模（n 较小）。
    """
    n = len(nets_list)
    full = (1 << n) - 1
    subset_sum = [0] * (1 << n)
    for mask in range(1, 1 << n):
        low = mask & (-mask)
        i = low.bit_length() - 1
        subset_sum[mask] = subset_sum[mask ^ low] + nets_list[i]
    zero_sum = [subset_sum[mask] == 0 for mask in range(1 << n)]

    # best[mask] = mask 能划分出的零和块数的最大值；choice[mask] = 选中的子块
    NEG = -10**9
    best = [NEG] * (1 << n)
    choice = [0] * (1 << n)
    best[0] = 0
    for mask in range(1, 1 << n):
        sub = mask
        while sub:
            if zero_sum[sub] and best[mask ^ sub] + 1 > best[mask]:
                best[mask] = best[mask ^ sub] + 1
                choice[mask] = sub
            sub = (sub - 1) & mask

    groups = []
    mask = full
    while mask:
        sub = choice[mask]
        groups.append([i for i in range(n) if sub >> i & 1])
        mask ^= sub
    return best[full], groups


def settle_group(group: list[tuple[str, int]]) -> list[dict]:
    """组内贪心撮合：每次让最大债务方付给最大债权方 min(欠, 收)。

    金额为正整数分；每笔至少清零一方，故 s 人组至多 s-1 笔。
    """
    debtors = sorted(((a, -v) for a, v in group if v < 0), key=lambda t: -t[1])
    creditors = sorted(((a, v) for a, v in group if v > 0), key=lambda t: -t[1])
    txs = []
    i = j = 0
    while i < len(debtors) and j < len(creditors):
        d_acct, d_amt = debtors[i]
        c_acct, c_amt = creditors[j]
        pay = min(d_amt, c_amt)
        if pay <= 0:  # 防御：绝不产生零额/负额交易
            raise AssertionError("内部错误：出现非正金额交易")
        txs.append({"from": d_acct, "to": c_acct, "amount_cents": pay})
        d_amt -= pay
        c_amt -= pay
        debtors[i] = (d_acct, d_amt)
        creditors[j] = (c_acct, c_amt)
        if d_amt == 0:
            i += 1
        if c_amt == 0:
            j += 1
    return txs


def settle(nets: dict[str, int]) -> dict:
    """最少笔数结算 + 可核验最优性界。"""
    active = sorted((a, v) for a, v in nets.items() if v != 0)
    n = len(active)
    if n == 0:
        return {
            "transactions": [],
            "transaction_count": 0,
            "minimum_transaction_count": 0,
            "optimal": True,
            "certificate": "所有净额均为 0，无需任何交易。",
        }

    accounts = [a for a, _ in active]
    values = [v for _, v in active]
    max_groups, groups = max_zero_sum_partition(values)
    min_tx = n - max_groups

    txs = []
    for g in groups:
        txs.extend(settle_group([(accounts[i], values[i]) for i in g]))

    # 运行时不变量校验（也是可核验性的一部分）
    for t in txs:
        assert t["amount_cents"] > 0, "禁止零额交易"
        assert nets[t["from"]] < 0 < nets[t["to"]], "付款方向必须与净额符号一致"
    balance = {a: 0 for a in accounts}
    for t in txs:
        balance[t["from"]] += t["amount_cents"]
        balance[t["to"]] -= t["amount_cents"]
    for a, v in active:
        assert balance[a] == -v, f"账户 {a} 结算后未清零"

    certificate = (
        f"精确枚举了全部 2^{n} = {1 << n} 个账户子集：任意 t 笔交易的结算图把 "
        f"{n} 个净额非零账户划分为若干连通分量，每个分量必须零和，且 s 个账户的分量"
        f"至少需要 s-1 笔交易，故 t >= n - (零和划分的最大块数)。"
        f"枚举得最大块数 = {max_groups}，下界 = {n} - {max_groups} = {min_tx}；"
        f"本方案恰用 {len(txs)} 笔达到该下界，故为最优。"
    )
    return {
        "transactions": txs,
        "transaction_count": len(txs),
        "minimum_transaction_count": min_tx,
        "optimal": len(txs) == min_tx,
        "zero_sum_partition": [[accounts[i] for i in g] for g in groups],
        "certificate": certificate,
    }


def run(spec: dict) -> dict:
    built = build_net_positions(spec)
    nets = built.pop("nets")
    settlement = settle(nets)
    total = sum(nets.values())
    return {
        **built,
        "conservation": {
            "sum_net_cents_incl_rounding_account": total,
            "ok": total == 0,
        },
        "settlement": settlement,
    }


def main(argv: list[str]) -> int:
    if len(argv) > 2:
        print(__doc__, file=sys.stderr)
        return 2
    if len(argv) == 2 and argv[1] != "-":
        with open(argv[1], "r", encoding="utf-8") as f:
            spec = json.load(f)
    else:
        spec = json.load(sys.stdin)
    json.dump(run(spec), sys.stdout, ensure_ascii=False, indent=2)
    print()
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
