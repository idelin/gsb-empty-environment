"""双资源背包 + 互斥组的最大收益子集选择。

特点：
- 不调用任何外部求解器，使用标准库实现精确分支定界枚举。
- 收益与容量/消耗支持整数或有理数（fractions.Fraction），全程精确运算。
- 两种容量必须同时满足，只满足一种不算可行。
- 互斥组内最多选择一个物品。
- 零成本正收益物品会被选中；负收益/零收益物品可不选。
- 结果区分“已证明最优”与“预算未决”（节点预算耗尽，仅给出当前最优可行解）。
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from fractions import Fraction
from typing import Optional, Sequence, Union

Number = Union[int, Fraction]


class Status(Enum):
    """求解状态：已证明最优 / 预算未决（可行但未证明最优）。"""

    OPTIMAL = "optimal"
    BUDGET_UNDECIDED = "budget_undecided"


class Reason(Enum):
    """未选原因分类。"""

    EXCEEDS_CAPACITY = "exceeds_capacity"      # 单独一件就超出某资源容量
    NEGATIVE_PROFIT = "negative_profit"        # 负收益，可不选
    NO_BENEFIT = "no_benefit"                  # 零收益，选了不增加目标
    GROUP_CONFLICT = "group_conflict"          # 互斥组中已选其他物品
    INSUFFICIENT_CAPACITY = "insufficient_capacity"  # 剩余容量不足
    NOT_IN_OPTIMAL = "not_in_optimal"          # 其他情况（理论兜底）


@dataclass(frozen=True)
class Item:
    """物品：名称、收益、两种资源消耗、可选互斥组名。"""

    name: str
    profit: Number
    cost1: Number = 0
    cost2: Number = 0
    group: Optional[str] = None

    def __post_init__(self) -> None:
        object.__setattr__(self, "profit", Fraction(self.profit))
        object.__setattr__(self, "cost1", Fraction(self.cost1))
        object.__setattr__(self, "cost2", Fraction(self.cost2))
        if self.cost1 < 0 or self.cost2 < 0:
            raise ValueError(f"物品 {self.name!r} 的消耗不能为负")


@dataclass(frozen=True)
class UnselectedInfo:
    """未选物品的原因与说明。"""

    item: Item
    reason: Reason
    detail: str


@dataclass(frozen=True)
class Result:
    """求解结果。"""

    status: Status
    selected: tuple
    total_profit: Fraction
    used1: Fraction
    used2: Fraction
    capacity1: Fraction
    capacity2: Fraction
    unselected: tuple  # tuple[UnselectedInfo, ...]
    nodes_visited: int

    @property
    def feasible(self) -> bool:
        """返回的选择始终可行（空集恒可行）。"""
        return True

    @property
    def proven_optimal(self) -> bool:
        return self.status is Status.OPTIMAL


class _BudgetExhausted(Exception):
    """节点预算耗尽，用于中断搜索。"""


def _check_items(items: Sequence[Item]) -> None:
    names = [it.name for it in items]
    if len(set(names)) != len(names):
        raise ValueError("物品名称必须唯一")


class _Search:
    """分支定界精确搜索。"""

    def __init__(self, items, cap1, cap2, max_nodes):
        # 按“综合密度”降序排列，零成本物品排在最前，利于剪枝。
        def density_key(it):
            weight = it.cost1 / cap1 if cap1 else Fraction(0)
            weight += it.cost2 / cap2 if cap2 else Fraction(0)
            if weight == 0:
                return Fraction(10**30)
            return it.profit / weight

        self.items = sorted(items, key=density_key, reverse=True)
        self.cap1 = cap1
        self.cap2 = cap2
        self.max_nodes = max_nodes
        self.nodes = 0
        self.best_profit = Fraction(0)
        self.best_set: tuple = ()
        # 后缀正收益和，作为最宽松上界。
        self.suffix = [Fraction(0)] * (len(self.items) + 1)
        for i in range(len(self.items) - 1, -1, -1):
            self.suffix[i] = self.suffix[i + 1] + max(Fraction(0), self.items[i].profit)

    def _resource_bound(self, start, rem, attr):
        # 单资源分数背包松弛上界（对剩余物品按该资源密度贪心，精确分数运算）。
        rem_items = self.items[start:]
        free = sum((it.profit for it in rem_items if getattr(it, attr) == 0), Fraction(0))
        rest = [it for it in rem_items if getattr(it, attr) > 0]
        rest.sort(key=lambda it: it.profit / getattr(it, attr), reverse=True)
        bound = free
        left = rem
        for it in rest:
            if left <= 0:
                break
            cost = getattr(it, attr)
            take = min(Fraction(1), left / cost)
            bound += take * it.profit
            left -= take * cost
        return bound

    def _upper_bound(self, start, profit, rem1, rem2):
        bound = profit + self.suffix[start]
        bound = min(bound, profit + self._resource_bound(start, rem1, "cost1"))
        bound = min(bound, profit + self._resource_bound(start, rem2, "cost2"))
        return bound

    def run(self):
        try:
            self._dfs(0, Fraction(0), Fraction(0), Fraction(0), (), frozenset())
        except _BudgetExhausted:
            return False
        return True

    def _dfs(self, i, used1, used2, profit, chosen, banned_groups):
        self.nodes += 1
        if self.max_nodes is not None and self.nodes > self.max_nodes:
            raise _BudgetExhausted
        if profit > self.best_profit:
            self.best_profit = profit
            self.best_set = chosen
        if i >= len(self.items):
            return
        if self._upper_bound(i, profit, self.cap1 - used1, self.cap2 - used2) <= self.best_profit:
            return
        it = self.items[i]
        # 分支一：选择该物品（容量允许且互斥组未被占用）。
        if (
            it.group not in banned_groups
            and used1 + it.cost1 <= self.cap1
            and used2 + it.cost2 <= self.cap2
        ):
            banned = banned_groups | {it.group} if it.group else banned_groups
            self._dfs(i + 1, used1 + it.cost1, used2 + it.cost2,
                      profit + it.profit, chosen + (it,), banned)
        # 分支二：不选该物品。
        self._dfs(i + 1, used1, used2, profit, chosen, banned_groups)


def solve(
    items: Sequence[Item],
    capacity1: Number,
    capacity2: Number,
    max_nodes: Optional[int] = None,
) -> Result:
    """求解双资源背包 + 互斥组选择问题。

    参数：
        items: 物品列表（名称需唯一）。
        capacity1 / capacity2: 两种资源容量（整数或 Fraction，需非负）。
        max_nodes: 搜索节点预算；None 表示不限。预算耗尽时返回
            Status.BUDGET_UNDECIDED 及当前最优可行解。

    返回：
        Result，含状态、选中物品、总收益、资源占用及每个未选物品的原因。
    """
    cap1 = Fraction(capacity1)
    cap2 = Fraction(capacity2)
    if cap1 < 0 or cap2 < 0:
        raise ValueError("容量不能为负")
    _check_items(items)

    # 预处理：单独不可行或非正收益的物品直接排除并记录原因，不参与搜索。
    pre_reasons = {}
    candidates = []
    for it in items:
        if it.cost1 > cap1 or it.cost2 > cap2:
            pre_reasons[it.name] = (Reason.EXCEEDS_CAPACITY, _exceeds_detail(it, cap1, cap2))
        elif it.profit < 0:
            pre_reasons[it.name] = (Reason.NEGATIVE_PROFIT, "负收益物品，选择只会降低总收益")
        elif it.profit == 0:
            pre_reasons[it.name] = (Reason.NO_BENEFIT, "零收益物品，选择不增加总收益")
        else:
            candidates.append(it)

    search = _Search(candidates, cap1, cap2, max_nodes)
    completed = search.run()
    status = Status.OPTIMAL if completed else Status.BUDGET_UNDECIDED

    selected = tuple(it for it in items if it in set(search.best_set))
    used1 = sum((it.cost1 for it in selected), Fraction(0))
    used2 = sum((it.cost2 for it in selected), Fraction(0))

    unselected = _explain(items, selected, pre_reasons, cap1, cap2, used1, used2, status)
    return Result(
        status=status,
        selected=selected,
        total_profit=search.best_profit,
        used1=used1,
        used2=used2,
        capacity1=cap1,
        capacity2=cap2,
        unselected=tuple(unselected),
        nodes_visited=search.nodes,
    )


def _exceeds_detail(it, cap1, cap2):
    parts = []
    if it.cost1 > cap1:
        parts.append(f"资源1需要 {it.cost1} > 容量 {cap1}")
    if it.cost2 > cap2:
        parts.append(f"资源2需要 {it.cost2} > 容量 {cap2}")
    return "单件即超容量：" + "；".join(parts)


def _explain(items, selected, pre_reasons, cap1, cap2, used1, used2, status):
    selected_names = {it.name for it in selected}
    group_pick = {it.group: it.name for it in selected if it.group}
    out = []
    for it in items:
        if it.name in selected_names:
            continue
        if it.name in pre_reasons:
            reason, detail = pre_reasons[it.name]
        elif it.group and it.group in group_pick:
            reason = Reason.GROUP_CONFLICT
            detail = f"互斥组 {it.group!r} 已选择 {group_pick[it.group]!r}"
        else:
            over = []
            if used1 + it.cost1 > cap1:
                over.append(f"资源1剩余 {cap1 - used1} < 需要 {it.cost1}")
            if used2 + it.cost2 > cap2:
                over.append(f"资源2剩余 {cap2 - used2} < 需要 {it.cost2}")
            if over:
                reason = Reason.INSUFFICIENT_CAPACITY
                detail = "剩余容量不足：" + "；".join(over)
            else:
                reason = Reason.NOT_IN_OPTIMAL
                if status is Status.OPTIMAL:
                    detail = "加入它不优于已证明的最优选择"
                else:
                    detail = "不在当前预算内找到的最优可行选择中（最优性未决）"
        if status is Status.BUDGET_UNDECIDED and reason in (
            Reason.GROUP_CONFLICT,
            Reason.INSUFFICIENT_CAPACITY,
            Reason.NOT_IN_OPTIMAL,
        ):
            detail += "（基于预算内最优可行解，非已证明最优）"
        out.append(UnselectedInfo(item=it, reason=reason, detail=detail))
    return out
