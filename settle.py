"""多币种守恒结算图。

流程：
1. 用精确有理汇率（Fraction）把每笔债权换算为基准币种等值；
2. 汇总每个账户的精确净额，再对净额做半偶舍入（round-half-even）到基准分；
3. 舍入造成的总差额显式记入舍入账户，保证含舍入账户在内的总额守恒（净额和为零）；
4. 对净额非零账户用分支限界枚举"零和分组"的最大划分数，
   从而得到最少笔数结算方案（笔数 = 账户数 - 分组数），
   该界可核验：任何结算方案的交易连通分量必须各自零和，
   大小为 k 的分量至少需要 k-1 笔交易。

仅使用 Python 3.11 标准库。
"""

from __future__ import annotations

import json
import sys
from fractions import Fraction

ROUNDING_ACCOUNT = "__rounding__"


def parse_rational(text) -> Fraction:
    """把字符串/数字解析为精确有理数，如 "6/5"、"10.5"、3。"""
    if isinstance(text, (int,)):
        return Fraction(text)
    if isinstance(text, float):
        raise ValueError("浮点数不允许，请用字符串表示精确值，如 \"10.5\" 或 \"6/5\"")
    s = str(text).strip()
    if "/" in s:
        num, den = s.split("/", 1)
        return Fraction(int(num.strip()), int(den.strip()))
    return Fraction(s)


def round_half_even_cents(x: Fraction) -> int:
    """把基准币种金额 x 半偶舍入到分，返回整数分。"""
    scaled = x * 100
    floor = scaled.numerator // scaled.denominator
    rem = scaled - floor
    half = Fraction(1, 2)
    if rem < half:
        return floor
    if rem > half:
        return floor + 1
    return floor if floor % 2 == 0 else floor + 1


def compute_exact_nets(spec: dict) -> dict[str, Fraction]:
    """按精确有理汇率汇总每个账户的基准币种净额（正=应收，负=应付）。"""
    rates = {cur: parse_rational(r) for cur, r in spec["rates"].items()}
    base = spec["base"]
    if base not in rates:
        rates[base] = Fraction(1)
    if rates[base] != 1:
        raise ValueError("基准币种汇率必须为 1")
    nets: dict[str, Fraction] = {}
    for claim in spec["claims"]:
        rate = rates[claim["currency"]]
        value = parse_rational(claim["amount"]) * rate
        debtor, creditor = claim["from"], claim["to"]
        nets[debtor] = nets.get(debtor, Fraction(0)) - value
        nets[creditor] = nets.get(creditor, Fraction(0)) + value
    return nets


def round_nets(exact_nets: dict[str, Fraction]) -> tuple[dict[str, int], int, dict[str, int]]:
    """半偶舍入各账户净额到分；差额记入舍入账户。

    返回 (舍入后净额[含舍入账户], 舍入账户净额, 每账户舍入残差)。
    守恒性：sum(舍入后净额) == 0。
    """
    rounded: dict[str, int] = {}
    residuals: dict[str, int] = {}
    total = 0
    for name, exact in exact_nets.items():
        cents = round_half_even_cents(exact)
        rounded[name] = cents
        residuals[name] = cents  # 精确值*100 与 cents 的差仅为展示用途时另算
        total += cents
    rounding_net = -total
    if rounding_net != 0 or ROUNDING_ACCOUNT in rounded:
        rounded[ROUNDING_ACCOUNT] = rounded.get(ROUNDING_ACCOUNT, 0) + rounding_net
    return rounded, rounding_net, residuals


def max_zero_sum_groups(nets: dict[str, int]) -> list[list[tuple[str, int]]]:
    """分支限界：把净额非零账户划分为最多个数的零和组。

    上界：当前组数 + 剩余账户数 // 2（每组至少 2 个账户，净额为零的账户
    不参与结算，已预先剔除）。穷举所有分组可能，故结果可证最优。
    """
    items = sorted(
        ((name, v) for name, v in nets.items() if v != 0),
        key=lambda kv: kv[1],
    )
    best: list[list[tuple[str, int]]] = []

    def rec(remaining: list[tuple[str, int]], groups: list[list[tuple[str, int]]]) -> None:
        nonlocal best
        if len(groups) + len(remaining) // 2 <= len(best):
            return  # 上界剪枝：即使剩余全部两两配对也无法超过当前最优
        if not remaining:
            if len(groups) > len(best):
                best = [list(g) for g in groups]
            return
        first = remaining[0]
        rest = remaining[1:]
        target = -first[1]
        m = len(rest)

        def enum_subsets(idx: int, chosen_idx: list[int], s: int) -> None:
            if s == target and chosen_idx:
                chosen_set = set(chosen_idx)
                group = [first] + [rest[j] for j in chosen_idx]
                nxt = [rest[j] for j in range(m) if j not in chosen_set]
                rec(nxt, groups + [group])
            if idx == m:
                return
            # 剪枝：剩余元素全加上也无法达到 target 时跳过（仅对同号情形有效，
            # 这里不做符号假设，保持简单，靠上界剪枝保证正确性）
            enum_subsets(idx + 1, chosen_idx, s)
            enum_subsets(idx + 1, chosen_idx + [idx], s + rest[idx][1])

        enum_subsets(0, [], 0)

    rec(items, [])
    return best


def settle_group(group: list[tuple[str, int]]) -> list[tuple[str, str, int]]:
    """组内贪心结算：k 个账户至多 k-1 笔，金额为正整数，方向与净额一致。"""
    debtors = sorted((x for x in group if x[1] < 0), key=lambda x: x[1])
    creditors = sorted((x for x in group if x[1] > 0), key=lambda x: -x[1])
    txns: list[tuple[str, str, int]] = []
    i = j = 0
    while i < len(debtors) and j < len(creditors):
        d_name, d = debtors[i]
        c_name, c = creditors[j]
        amount = min(-d, c)
        if amount <= 0:  # 防御：绝不产生零额或负额交易
            raise AssertionError("非正交易金额")
        txns.append((d_name, c_name, amount))
        debtors[i] = (d_name, d + amount)
        creditors[j] = (c_name, c - amount)
        if debtors[i][1] == 0:
            i += 1
        if creditors[j][1] == 0:
            j += 1
    return txns


def build_settlement(nets: dict[str, int]) -> dict:
    """由整数分净额（含舍入账户，和为零）生成最少笔数结算方案。"""
    if sum(nets.values()) != 0:
        raise ValueError("净额总和不为零，无法结算")
    groups = max_zero_sum_groups(nets)
    txns: list[tuple[str, str, int]] = []
    for group in groups:
        txns.extend(settle_group(group))
    active = sum(1 for v in nets.values() if v != 0)
    lower_bound = active - len(groups)
    return {
        "transactions": [
            {"from": f, "to": t, "amount": a} for f, t, a in txns
        ],
        "groups": [[name for name, _ in g] for g in groups],
        "optimality": {
            "active_accounts": active,
            "zero_sum_groups": len(groups),
            "transactions": len(txns),
            "lower_bound": lower_bound,
            "optimal": len(txns) == lower_bound,
            "method": "exhaustive zero-sum partition (branch and bound)",
            "certificate": (
                "任何结算方案的交易连通分量必须各自零和；大小为 k 的分量至少 "
                "需要 k-1 笔交易，故总笔数 >= 活跃账户数 - 零和分组数。"
                "分组数已经穷举所有零和划分取得最大值，因此该下界不可再降。"
            ),
        },
    }


def verify_settlement(nets: dict[str, int], settlement: dict) -> list[str]:
    """核验结算方案；返回错误列表，空列表表示通过。"""
    errors: list[str] = []
    balance = {name: 0 for name in nets}
    for tx in settlement["transactions"]:
        f, t, a = tx["from"], tx["to"], tx["amount"]
        if not isinstance(a, int) or a <= 0:
            errors.append(f"非法交易金额（必须为正整数分）: {tx}")
            continue
        if f not in nets or t not in nets:
            errors.append(f"未知账户: {tx}")
            continue
        if nets[f] >= 0:
            errors.append(f"付款方向与净额不一致（付款方净额非负）: {tx}")
        if nets[t] <= 0:
            errors.append(f"付款方向与净额不一致（收款方净额非正）: {tx}")
        balance[f] -= a
        balance[t] += a
    for name, net in nets.items():
        if balance.get(name, 0) != net:
            errors.append(
                f"账户 {name} 未结清：净额 {net}，交易净流 {balance.get(name, 0)}"
            )
    if sum(nets.values()) != 0:
        errors.append("总额不守恒：含舍入账户的净额和不为零")
    return errors


def run(spec: dict) -> dict:
    exact_nets = compute_exact_nets(spec)
    rounded, rounding_net, _ = round_nets(exact_nets)
    settlement = build_settlement(rounded)
    errors = verify_settlement(rounded, settlement)
    return {
        "base": spec["base"],
        "exact_nets_base": {k: str(v) for k, v in exact_nets.items()},
        "rounded_nets_cents": rounded,
        "rounding_account": {"name": ROUNDING_ACCOUNT, "net_cents": rounding_net},
        "settlement": settlement,
        "conservation_ok": sum(rounded.values()) == 0,
        "verification_errors": errors,
    }


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        print("用法: python settle.py <input.json>", file=sys.stderr)
        return 2
    with open(argv[1], "r", encoding="utf-8") as fh:
        spec = json.load(fh)
    result = run(spec)
    json.dump(result, sys.stdout, ensure_ascii=False, indent=2)
    print()
    return 0 if not result["verification_errors"] else 1


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
