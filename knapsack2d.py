"""二维容量 + 互斥组背包的精确求解器（仅标准库，不调用任何求解器）。

模型：每种物品消耗两种资源，可属于一个互斥组（每组至多选一个），
在两种容量限制下选择收益最大的子集。收益与容量为整数，物品权重
允许整数或有理数（Fraction、"p/q" 字符串或 (num, den) 元组），
内部按分母最小公倍数统一放大为整数后做动态规划。

算法：按互斥组分组的多选动态规划（每组取“不选”或“恰好选一个”），
状态为 (已用容量1, 已用容量2) -> (最大收益, 选择)，每轮做二维
帕累托剪枝（容量都不更大且收益不更低的状态被丢弃）。

结果状态分开三种：
- OPTIMAL   已在预算内完成全部 DP，解被证明最优；
- FEASIBLE  状态数预算耗尽，返回当前最优可行解，最优性未决；
- UNDECIDED 预算连初始状态都容纳不下，无法给出可信结论。
"""
from __future__ import annotations

from bisect import bisect_right
from dataclasses import dataclass
from enum import Enum
from fractions import Fraction
from math import gcd


class Status(str, Enum):
    OPTIMAL = "optimal"
    FEASIBLE = "feasible"
    UNDECIDED = "undecided"


def to_fraction(value) -> Fraction:
    """把 int / Fraction / (num, den) / "p/q" 统一解析为 Fraction。"""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, tuple) and len(value) == 2:
        return Fraction(value[0], value[1])
    if isinstance(value, str):
        return Fraction(value)
    raise TypeError(f"无法解析为有理数: {value!r}")


@dataclass(frozen=True)
class Item:
    """物品：name 唯一；profit 为整数（可为负）；weight 允许有理数；group 为互斥组名。"""

    name: str
    profit: int
    weight1: object = 0
    weight2: object = 0
    group: str | None = None


@dataclass
class SolveResult:
    status: Status
    total_profit: int | None
    used: tuple[Fraction, Fraction] | None
    capacity: tuple[Fraction, Fraction]
    selected: list[str]
    unselected_reasons: dict[str, str]
    states_explored: int
    state_budget: int

    @property
    def is_optimal(self) -> bool:
        return self.status is Status.OPTIMAL


@dataclass(frozen=True)
class _Norm:
    name: str
    profit: int
    w1: Fraction
    w2: Fraction
    group: str | None


def _normalize(item: Item) -> _Norm:
    profit = to_fraction(item.profit)
    if profit.denominator != 1:
        raise ValueError(f"收益必须为整数: {item.name} -> {item.profit!r}")
    w1 = to_fraction(item.weight1)
    w2 = to_fraction(item.weight2)
    if w1 < 0 or w2 < 0:
        raise ValueError(f"权重必须非负: {item.name}")
    return _Norm(item.name, int(profit), w1, w2, item.group)


def _lcm(a: int, b: int) -> int:
    return a // gcd(a, b) * b


def _common_scale(norm: list[_Norm], cap1: Fraction, cap2: Fraction) -> int:
    scale = _lcm(cap1.denominator, cap2.denominator)
    for it in norm:
        scale = _lcm(scale, _lcm(it.w1.denominator, it.w2.denominator))
    return scale


def _fw_query(tree: list, pos: int):
    best = tree[0]
    while pos > 0:
        if tree[pos] > best:
            best = tree[pos]
        pos -= pos & -pos
    return best


def _fw_update(tree: list, pos: int, value) -> None:
    n = len(tree) - 1
    while pos <= n:
        if value > tree[pos]:
            tree[pos] = value
        pos += pos & -pos


def _pareto_prune(states: dict) -> dict:
    """丢弃被支配状态：存在另一状态两种容量都不更大且收益不更小。"""
    if len(states) <= 1:
        return states
    entries = sorted((c1, c2, p, sel) for (c1, c2), (p, sel) in states.items())
    c2_values = sorted({c2 for _, c2, _, _ in entries})
    neg_inf = float("-inf")
    tree = [neg_inf] * (len(c2_values) + 1)
    kept = {}
    for c1, c2, p, sel in entries:
        pos = bisect_right(c2_values, c2)
        if _fw_query(tree, pos) >= p:
            continue
        _fw_update(tree, pos, p)
        kept[(c1, c2)] = (p, sel)
    return kept


def solve(items, capacity1, capacity2, state_budget: int = 100_000) -> SolveResult:
    """精确求解二维容量 + 互斥组背包，返回选择与未选原因。

    state_budget 为 DP 状态数预算：预算内完成则 OPTIMAL；预算耗尽则
    返回当前最优可行解并标记 FEASIBLE（最优性未决）；预算小于 1 时
    无法给出任何可信结论，标记 UNDECIDED。
    """
    cap1 = to_fraction(capacity1)
    cap2 = to_fraction(capacity2)
    if cap1 < 0 or cap2 < 0:
        raise ValueError("容量必须非负")
    if state_budget < 1:
        return SolveResult(Status.UNDECIDED, None, None, (cap1, cap2), [], {}, 0, state_budget)

    norm = [_normalize(it) for it in items]
    scale = _common_scale(norm, cap1, cap2)
    cap1_i = int(cap1 * scale)
    cap2_i = int(cap2 * scale)
    weighted = [
        _Norm(it.name, it.profit, Fraction(int(it.w1 * scale)), Fraction(int(it.w2 * scale)), it.group)
        for it in norm
    ]

    group_order: list = []
    group_map: dict = {}
    for idx, it in enumerate(weighted):
        key = it.group if it.group is not None else ("__solo__", idx)
        if key not in group_map:
            group_map[key] = []
            group_order.append(key)
        group_map[key].append(it)

    states = {(0, 0): (0, ())}
    explored = 0
    completed = True
    for key in group_order:
        candidates = dict(states)
        for (c1, c2), (profit, sel) in states.items():
            for it in group_map[key]:
                n1 = c1 + int(it.w1)
                n2 = c2 + int(it.w2)
                if n1 <= cap1_i and n2 <= cap2_i:
                    explored += 1
                    new_profit = profit + it.profit
                    prev = candidates.get((n1, n2))
                    if prev is None or prev[0] < new_profit:
                        candidates[(n1, n2)] = (new_profit, sel + (it.name,))
        states = _pareto_prune(candidates)
        if len(states) > state_budget:
            completed = False
            break

    (best_c1, best_c2), (best_profit, best_sel) = max(
        states.items(), key=lambda kv: (kv[1][0], -kv[0][0] - kv[0][1])
    )
    status = Status.OPTIMAL if completed else Status.FEASIBLE
    selected_set = set(best_sel)
    rem1 = cap1 - Fraction(best_c1, scale)
    rem2 = cap2 - Fraction(best_c2, scale)
    reasons = _build_reasons(norm, selected_set, rem1, rem2)
    return SolveResult(
        status=status,
        total_profit=best_profit,
        used=(Fraction(best_c1, scale), Fraction(best_c2, scale)),
        capacity=(cap1, cap2),
        selected=[name for name in best_sel],
        unselected_reasons=reasons,
        states_explored=explored,
        state_budget=state_budget,
    )


def _build_reasons(norm: list[_Norm], selected: set, rem1: Fraction, rem2: Fraction) -> dict:
    chosen_in_group: dict[str, str] = {}
    for it in norm:
        if it.name in selected and it.group is not None and it.group not in chosen_in_group:
            chosen_in_group[it.group] = it.name
    reasons = {}
    for it in norm:
        if it.name in selected:
            continue
        if it.group is not None and it.group in chosen_in_group:
            reasons[it.name] = f"互斥组 '{it.group}' 中已选择 '{chosen_in_group[it.group]}'"
        elif it.profit <= 0:
            reasons[it.name] = "收益非正，不选更优"
        elif it.w1 > rem1 or it.w2 > rem2:
            lacking = []
            if it.w1 > rem1:
                lacking.append("资源1")
            if it.w2 > rem2:
                lacking.append("资源2")
            reasons[it.name] = f"剩余容量不足（{'、'.join(lacking)}）"
        else:
            reasons[it.name] = "正收益但预算内未进入最优组合（最优性权衡）"
    return reasons
