"""多目标任务方案组合的枚举与 Pareto 非支配解筛选。

模型
----
- 每个任务（Task）提供若干候选方案（Plan），每个方案带三维目标：
  成本 cost、时延 latency、能耗 energy，均为最小化目标。
- 一个组合（Combination）从每个任务中各选一个方案，目标值为各方案之和。
- 组合受资源预算（Budget）约束：任一维超过预算即视为超预算组合。
- 在所有可行组合中枚举 Pareto 非支配解；目标向量完全相同的组合
  只保留 combo_id 最小者作为代表。
- 结果分三组返回：optimal（非支配代表，受 max_results 上限截断）、
  feasible（全部可行组合）、over_budget（超预算组合）。

数值一律使用 int / Fraction 精确有理数，不调用任何求解器，
仅依赖 Python 3.11 标准库。
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from itertools import product
from typing import Iterator, Optional, Sequence, Union

Number = Union[int, Fraction, str]


def _to_fraction(value: Number) -> Fraction:
    """把 int / Fraction / 形如 "3/2" 的字符串统一转成 Fraction。"""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("bool 不是合法的目标数值")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, str):
        return Fraction(value)
    raise TypeError(f"不支持的数值类型 {type(value)!r}，请使用 int、Fraction 或分数字符串")


@dataclass(frozen=True)
class Plan:
    """单个任务的一个候选方案。"""

    plan_id: str
    cost: Fraction
    latency: Fraction
    energy: Fraction

    @classmethod
    def of(cls, plan_id: str, cost: Number, latency: Number, energy: Number) -> "Plan":
        return cls(plan_id, _to_fraction(cost), _to_fraction(latency), _to_fraction(energy))

    def vector(self) -> tuple[Fraction, Fraction, Fraction]:
        return (self.cost, self.latency, self.energy)


@dataclass(frozen=True)
class Task:
    """一个任务及其候选方案集合（至少一个，plan_id 不可重复）。"""

    task_id: str
    plans: tuple[Plan, ...]

    def __post_init__(self) -> None:
        if not self.plans:
            raise ValueError(f"任务 {self.task_id!r} 至少要提供一个候选方案")
        ids = [p.plan_id for p in self.plans]
        if len(set(ids)) != len(ids):
            raise ValueError(f"任务 {self.task_id!r} 内存在重复的 plan_id")


@dataclass(frozen=True)
class Budget:
    """三维资源预算；某一维为 None 表示该维不设限。"""

    max_cost: Optional[Fraction] = None
    max_latency: Optional[Fraction] = None
    max_energy: Optional[Fraction] = None

    @classmethod
    def of(
        cls,
        max_cost: Optional[Number] = None,
        max_latency: Optional[Number] = None,
        max_energy: Optional[Number] = None,
    ) -> "Budget":
        conv = lambda v: None if v is None else _to_fraction(v)
        return cls(conv(max_cost), conv(max_latency), conv(max_energy))

    def admits(self, cost: Fraction, latency: Fraction, energy: Fraction) -> bool:
        return (
            (self.max_cost is None or cost <= self.max_cost)
            and (self.max_latency is None or latency <= self.max_latency)
            and (self.max_energy is None or energy <= self.max_energy)
        )


@dataclass(frozen=True)
class Combination:
    """一个组合：每个任务各选一个方案，目标为各方案之和。"""

    combo_id: int
    choices: tuple[tuple[str, str], ...]  # ((task_id, plan_id), ...)
    cost: Fraction
    latency: Fraction
    energy: Fraction

    def vector(self) -> tuple[Fraction, Fraction, Fraction]:
        return (self.cost, self.latency, self.energy)

    def label(self) -> str:
        return "+".join(plan_id for _, plan_id in self.choices)


def enumerate_combinations(tasks: Sequence[Task]) -> Iterator[Combination]:
    """按笛卡尔积枚举所有组合，combo_id 从 0 开始按枚举顺序递增。"""
    task_list = list(tasks)
    if not task_list:
        return
    pools = [task.plans for task in task_list]
    for combo_id, picked in enumerate(product(*pools)):
        cost = sum((p.cost for p in picked), Fraction(0))
        latency = sum((p.latency for p in picked), Fraction(0))
        energy = sum((p.energy for p in picked), Fraction(0))
        choices = tuple((t.task_id, p.plan_id) for t, p in zip(task_list, picked))
        yield Combination(combo_id, choices, cost, latency, energy)


def dominates(a: Combination, b: Combination) -> bool:
    """a 支配 b：所有维不劣且至少一维严格更优。

    若 a 某一维更好、另一维更差，则两者互不支配，都不能删除对方。
    """
    av, bv = a.vector(), b.vector()
    return all(x <= y for x, y in zip(av, bv)) and any(x < y for x, y in zip(av, bv))


def pareto_representatives(feasible: Sequence[Combination]) -> list[Combination]:
    """在可行组合中求非支配代表：先去重（等值留最小 ID），再做支配筛选。"""
    best_by_vector: dict[tuple[Fraction, Fraction, Fraction], Combination] = {}
    for combo in feasible:
        key = combo.vector()
        current = best_by_vector.get(key)
        if current is None or combo.combo_id < current.combo_id:
            best_by_vector[key] = combo
    reps = list(best_by_vector.values())
    front = [c for c in reps if not any(dominates(other, c) for other in reps if other is not c)]
    front.sort(key=lambda c: (c.cost, c.latency, c.energy, c.combo_id))
    return front


@dataclass(frozen=True)
class SelectionResult:
    """筛选结果，三组分开存放。

    - optimal:     Pareto 非支配代表，按 (cost, latency, energy, id) 排序，
                   超过 max_results 时明确截断（truncated=True，omitted 为省略数）。
    - feasible:    全部可行组合（含被支配者），每个都独立满足预算。
    - over_budget: 超预算组合，不参与最优性判定。
    """

    optimal: tuple[Combination, ...]
    feasible: tuple[Combination, ...]
    over_budget: tuple[Combination, ...]
    truncated: bool
    omitted: int


def select_plans(
    tasks: Sequence[Task],
    budget: Budget,
    max_results: Optional[int] = None,
) -> SelectionResult:
    """枚举组合、按预算分组、求 Pareto 代表并按上限截断。"""
    if max_results is not None and max_results < 1:
        raise ValueError("max_results 必须是正整数或 None")
    feasible: list[Combination] = []
    over_budget: list[Combination] = []
    for combo in enumerate_combinations(tasks):
        if budget.admits(combo.cost, combo.latency, combo.energy):
            feasible.append(combo)
        else:
            over_budget.append(combo)
    front = pareto_representatives(feasible)
    truncated = False
    omitted = 0
    if max_results is not None and len(front) > max_results:
        omitted = len(front) - max_results
        front = front[:max_results]
        truncated = True
    return SelectionResult(
        optimal=tuple(front),
        feasible=tuple(feasible),
        over_budget=tuple(over_budget),
        truncated=truncated,
        omitted=omitted,
    )


def _fmt(frac: Fraction) -> str:
    return str(frac.numerator) if frac.denominator == 1 else str(frac)


def _demo() -> None:
    tasks = [
        Task("T1", (
            Plan.of("T1A", 4, 6, 3),
            Plan.of("T1B", 6, 3, 5),
            Plan.of("T1C", 3, 8, 2),
        )),
        Task("T2", (
            Plan.of("T2A", 5, 4, 6),
            Plan.of("T2B", 2, 7, Fraction(7, 2)),
            Plan.of("T2C", 7, 2, 4),
        )),
        Task("T3", (
            Plan.of("T3A", 3, 5, 5),
            Plan.of("T3B", 5, 3, 3),
        )),
    ]
    budget = Budget.of(max_cost=14, max_latency=16, max_energy=14)
    result = select_plans(tasks, budget, max_results=3)

    total = len(result.feasible) + len(result.over_budget)
    print(f"任务数: {len(tasks)}，组合总数: {total}")
    print(f"预算: cost<=14, latency<=16, energy<=14")
    print(f"可行组合: {len(result.feasible)}，超预算组合: {len(result.over_budget)}")
    print()
    print("Pareto 非支配代表（optimal）:")
    for c in result.optimal:
        print(f"  id={c.combo_id:2d} {c.label():<16} "
              f"cost={_fmt(c.cost):>3} latency={_fmt(c.latency):>3} energy={_fmt(c.energy):>3}")
    print(f"截断: {result.truncated}，因上限省略: {result.omitted}")
    print()
    print("超预算组合（over_budget，前 5 个）:")
    for c in result.over_budget[:5]:
        print(f"  id={c.combo_id:2d} {c.label():<16} "
              f"cost={_fmt(c.cost):>3} latency={_fmt(c.latency):>3} energy={_fmt(c.energy):>3}")


if __name__ == "__main__":
    _demo()
