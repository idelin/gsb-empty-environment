"""半开时间区间任务的最少同质资源分配（纯标准库，不调用求解器）。

模型：
- 每个任务占据半开区间 [start, end)，因此 [0,5) 与 [5,8) 可共用同一资源；
- 无额外约束时，最少资源数等于峰值重叠数（区间图为完美图），贪心即可达到；
- 额外约束：指定资源（pinned，资源编号绝对固定）与不可同资源（cannot_share）。
  加上这些约束后问题等价于图着色，转为带节点预算的回溯搜索，并报告下界/上界。

结果状态分为四类：最优(OPTIMAL)、可行(FEASIBLE)、预算未决(UNDECIDED)、

不可行(INFEASIBLE，如指定资源冲突，搜索前即可发现）。
"""
from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from fractions import Fraction

__all__ = ["Task", "Status", "Result", "overlaps", "peak_overlap", "solve"]


class Status(Enum):
    OPTIMAL = "optimal"        # 找到分配且已证明资源数最少
    FEASIBLE = "feasible"      # 找到分配，但预算耗尽，最优性未证明
    UNDECIDED = "undecided"    # 预算耗尽，连一份完整分配都未构造出来
    INFEASIBLE = "infeasible"  # 约束矛盾（指定资源冲突，提前发现）


def _as_fraction(value: object, field: str) -> Fraction:
    if isinstance(value, bool):
        raise TypeError(f"{field} 不接受 bool")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, Fraction):
        return value
    raise TypeError(f"{field} 只接受 int 或 Fraction，得到 {type(value).__name__}")


@dataclass(frozen=True)
class Task:
    """占据半开区间 [start, end) 的任务；pinned 为指定资源编号（可选）。"""

    name: str
    start: int | Fraction
    end: int | Fraction
    pinned: int | None = None

    def __post_init__(self) -> None:
        object.__setattr__(self, "start", _as_fraction(self.start, "start"))
        object.__setattr__(self, "end", _as_fraction(self.end, "end"))
        if not self.start < self.end:
            raise ValueError(f"任务 {self.name!r} 需满足 start < end（半开区间）")
        if self.pinned is not None:
            if isinstance(self.pinned, bool) or not isinstance(self.pinned, int):
                raise TypeError("pinned 只接受 int 或 None")
            if self.pinned < 0:
                raise ValueError("pinned 必须 >= 0")


@dataclass(frozen=True)
class Result:
    status: Status
    assignment: dict[str, int] | None  # 任务名 -> 资源编号
    num_resources: int | None          # 资源池大小（最大编号 + 1）
    lower_bound: int | None            # 下界：峰值重叠 / 团 / 指定资源编号
    upper_bound: int | None            # 上界：当前最好分配的资源数
    nodes: int                         # 已消耗的尝试着色次数
    budget: int                        # 节点预算
    detail: str = ""


def overlaps(a: Task, b: Task) -> bool:
    """半开区间相交判定：[0,5) 与 [5,8) 不算重叠。"""
    return a.start < b.end and b.start < a.end


def peak_overlap(tasks: list[Task]) -> int:
    """峰值重叠数：同一时刻最多有多少个任务并存（端点相接不计）。"""
    events: list[tuple[Fraction, int]] = []
    for t in tasks:
        events.append((t.start, 1))
        events.append((t.end, -1))
    events.sort(key=lambda e: (e[0], e[1]))  # 同一时刻先处理结束(-1)再处理开始(+1)
    peak = current = 0
    for _, delta in events:
        current += delta
        peak = max(peak, current)
    return peak


class _BudgetExhausted(Exception):
    pass


class _Counter:
    """节点预算：每次尝试给任务着色消耗 1 个节点。"""

    def __init__(self, budget: int) -> None:
        self.budget = budget
        self.nodes = 0

    def spend(self) -> None:
        self.nodes += 1
        if self.nodes > self.budget:
            raise _BudgetExhausted


def _greedy_clique_size(adj: list[set[int]]) -> int:
    """按度数降序贪心找一个团，作为下界（不保证最大）。"""
    clique: list[int] = []
    for v in sorted(range(len(adj)), key=lambda v: -len(adj[v])):
        if all(u in adj[v] for u in clique):
            clique.append(v)
    return len(clique)


def _greedy_color(
    adj: list[set[int]], pinned: list[int | None], order: list[int], counter: _Counter
) -> list[int]:
    """按给定顺序首次适配着色；pinned 任务先用其指定编号。"""
    colors: list[int | None] = [None] * len(adj)
    for i, p in enumerate(pinned):
        if p is not None:
            counter.spend()
            colors[i] = p
    for i in order:
        if colors[i] is not None:
            continue
        used = {colors[j] for j in adj[i] if colors[j] is not None}
        c = 0
        while c in used:
            c += 1
        counter.spend()
        colors[i] = c
    return colors  # type: ignore


def _colorable(
    adj: list[set[int]], pinned: list[int | None], k: int, counter: _Counter
) -> list[int] | None:
    """回溯判定 k 色可行性（DSATUR 选点）；返回着色或 None（已证明不可行）。"""
    n = len(adj)
    colors: list[int | None] = [None] * n
    for i, p in enumerate(pinned):
        if p is not None:
            if p >= k:
                return None  # 指定资源编号超出池大小，该 k 必然不可行
            colors[i] = p

    def select() -> int | None:
        best_v = None
        best_key = (-1, -1)
        for v in range(n):
            if colors[v] is None:
                saturation = len({colors[u] for u in adj[v] if colors[u] is not None})
                key = (saturation, len(adj[v]))
                if key > best_key:
                    best_key = key
                    best_v = v
        return best_v

    def search() -> bool:
        v = select()
        if v is None:
            return True
        used = {colors[u] for u in adj[v] if colors[u] is not None}
        for c in range(k):
            if c in used:
                continue
            counter.spend()
            colors[v] = c
            if search():
                return True
            colors[v] = None
        return False

    return colors if search() else None  # type: ignore


def solve(
    tasks: list[Task],
    cannot_share: list[tuple[str, str]] | tuple[tuple[str, str], ...] = (),
    budget: int = 100_000,
) -> Result:
    """求最少同质资源分配。

    budget 为节点预算（每次尝试着色计 1 个节点，含初始贪心构造）。
    """
    if isinstance(budget, bool) or not isinstance(budget, int):
        raise TypeError("budget 只接受 int")
    if budget < 0:
        raise ValueError("budget 必须 >= 0")
    tasks = list(tasks)
    names = [t.name for t in tasks]
    if len(set(names)) != len(names):
        raise ValueError("任务名重复")
    index = {name: i for i, name in enumerate(names)}
    n = len(tasks)

    pairs: list[tuple[int, int]] = []
    for pair in cannot_share:
        a, b = pair
        if a not in index or b not in index:
            raise ValueError(f"cannot_share 引用了未知任务: {pair!r}")
        if a == b:
            raise ValueError(f"cannot_share 不能引用同一任务: {pair!r}")
        pairs.append((index[a], index[b]))

    if n == 0:
        return Result(Status.OPTIMAL, {}, 0, 0, 0, 0, budget, "空任务集")

    # 冲突图：区间重叠 或 不可同资源
    adj: list[set[int]] = [set() for _ in range(n)]
    for i in range(n):
        for j in range(i + 1, n):
            if overlaps(tasks[i], tasks[j]):
                adj[i].add(j)
                adj[j].add(i)
    for i, j in pairs:
        adj[i].add(j)
        adj[j].add(i)
    pinned = [t.pinned for t in tasks]

    # 指定资源冲突：进入搜索前提前发现
    for i in range(n):
        for j in range(i + 1, n):
            if pinned[i] is not None and pinned[i] == pinned[j] and j in adj[i]:
                return Result(
                    Status.INFEASIBLE, None, None, None, None, 0, budget,
                    f"任务 {names[i]!r} 与 {names[j]!r} 同指定资源 {pinned[i]}，"
                    f"但区间重叠或被声明不可同资源",
                )

    lower = max(
        peak_overlap(tasks),
        _greedy_clique_size(adj),
        max((p for p in pinned if p is not None), default=-1) + 1,
    )

    counter = _Counter(budget)
    order = sorted(range(n), key=lambda i: (tasks[i].start, tasks[i].end, names[i]))
    try:
        best = _greedy_color(adj, pinned, order, counter)
    except _BudgetExhausted:
        return Result(
            Status.UNDECIDED, None, None, lower, None, counter.nodes, budget,
            "预算在初始贪心构造中耗尽，未得到任何完整分配",
        )
    upper = max(best) + 1

    # 从 upper-1 向下尝试更小的资源池：找到则改进上界，证明不可行则当前解最优
    if upper > lower:
        for k in range(upper - 1, lower - 1, -1):
            try:
                alt = _colorable(adj, pinned, k, counter)
            except _BudgetExhausted:
                assignment = dict(zip(names, best))
                return Result(
                    Status.FEASIBLE, assignment, upper, lower, upper,
                    counter.nodes, budget,
                    f"预算耗尽：已找到 {upper} 资源的分配，下界为 {lower}，最优性未证明",
                )
            if alt is None:
                break  # k 色已证明不可行，当前 best 即最优
            best = alt
            upper = k
            if upper == lower:
                break

    return Result(
        Status.OPTIMAL, dict(zip(names, best)), upper, lower, upper,
        counter.nodes, budget,
        f"已证明最少资源数为 {upper}（下界 {lower}）",
    )
