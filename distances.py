"""分布距离：离散总变差（TV）与输运距离。

定义（p、q 为经验分布的概率质量函数，均为精确有理数 Fraction）：

* 总变差  TV(p, q) = 1/2 * Σ_v |p(v) - q(v)|
* 有序一维输运（Wasserstein-1，单位距离成本 c(x, y) = |x - y|）
      W1(p, q) = Σ_i |CDF_p(x_i) - CDF_q(x_i)| * (x_{i+1} - x_i)
  其中 x_i 为排序后支撑集的相邻点。同时用双指针贪心构造最优输运流。
* 一般成本矩阵模式：在缩放后的整数供给/需求上求解真正的最小费用流
  （见 mincostflow.py），不得沿用一维累计公式。

所有模式都输出逐桶（或逐区间）贡献与样本基数，便于审计。
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Callable, Dict, Hashable, List, Optional, Sequence, Tuple

from histogram import Histogram
from mincostflow import min_cost_transport

Number = "int | Fraction"


def _require_non_empty(p: Histogram, q: Histogram) -> None:
    if p.is_empty:
        raise ValueError("class histogram is empty: distance undefined (no division by zero)")
    if q.is_empty:
        raise ValueError("population histogram is empty: distance undefined")


def _union_support(p: Histogram, q: Histogram) -> List[Hashable]:
    return sorted(set(p.counts) | set(q.counts), key=repr)


# ---------------------------------------------------------------- 总变差

@dataclass(frozen=True)
class TvContribution:
    value: Hashable
    class_count: int
    population_count: int
    class_prob: Fraction
    population_prob: Fraction
    contribution: Fraction  # |p(v) - q(v)| / 2


def tv_contributions(p: Histogram, q: Histogram) -> Tuple[TvContribution, ...]:
    """逐桶贡献；所有桶贡献之和即 TV。"""
    _require_non_empty(p, q)
    out: List[TvContribution] = []
    for v in _union_support(p, q):
        pc, qc = p.counts.get(v, 0), q.counts.get(v, 0)
        pp, qp = Fraction(pc, p.total), Fraction(qc, q.total)
        out.append(TvContribution(v, pc, qc, pp, qp, abs(pp - qp) / 2))
    return tuple(out)


def total_variation(p: Histogram, q: Histogram) -> Fraction:
    """精确有理数 TV ∈ [0, 1]；分布完全相同则为 0。"""
    return sum((c.contribution for c in tv_contributions(p, q)), Fraction(0))


# ---------------------------------------------------------------- 输运流

@dataclass(frozen=True)
class FlowEdge:
    source: Hashable
    target: Hashable
    amount: Fraction  # 归一化质量，全体之和为 1


@dataclass(frozen=True)
class IntervalContribution:
    """有序模式逐区间贡献：|累计差| * 区间长度。"""

    left: Hashable
    right: Hashable
    cum_diff: Fraction
    gap: object
    contribution: object


@dataclass(frozen=True)
class TransportResult:
    distance: object  # Fraction（一般模式）或数值（有序模式，gap 为数值时仍为 Fraction）
    flow: Tuple[FlowEdge, ...]
    intervals: Tuple[IntervalContribution, ...]  # 一般模式为空


def ordered_transport(
    p: Histogram,
    q: Histogram,
    ordered_values: Optional[Sequence[Hashable]] = None,
) -> TransportResult:
    """有序一维 Wasserstein-1。

    ordered_values：全序取值序列（缺省为并支撑集排序）。取值须支持减法，
    相邻差（gap）须为非负数值。完全相同分布距离为 0。
    """
    _require_non_empty(p, q)
    values = list(ordered_values) if ordered_values is not None else _union_support(p, q)
    if len(values) < 2 and (p.counts or q.counts):
        only = values[0] if values else next(iter(set(p.counts) | set(q.counts)))
        return TransportResult(Fraction(0), (FlowEdge(only, only, Fraction(1)),), ())

    intervals: List[IntervalContribution] = []
    cum = Fraction(0)
    distance: object = Fraction(0)
    for i in range(len(values) - 1):
        v = values[i]
        cum += Fraction(p.counts.get(v, 0), p.total) - Fraction(q.counts.get(v, 0), q.total)
        gap = values[i + 1] - v
        contrib = abs(cum) * gap
        intervals.append(IntervalContribution(v, values[i + 1], cum, gap, contrib))
        distance += contrib

    # 双指针贪心构造最优输运流（一维 Monge 结构下贪心即最优）。
    ps = [(v, Fraction(p.counts[v], p.total)) for v in values if p.counts.get(v)]
    qs = [(v, Fraction(q.counts[v], q.total)) for v in values if q.counts.get(v)]
    flow: List[FlowEdge] = []
    i = j = 0
    while i < len(ps) and j < len(qs):
        amount = min(ps[i][1], qs[j][1])
        flow.append(FlowEdge(ps[i][0], qs[j][0], amount))
        ps[i] = (ps[i][0], ps[i][1] - amount)
        qs[j] = (qs[j][0], qs[j][1] - amount)
        if ps[i][1] == 0:
            i += 1
        if qs[j][1] == 0:
            j += 1
    return TransportResult(distance, tuple(flow), tuple(intervals))


def general_transport(
    p: Histogram,
    q: Histogram,
    cost_matrix: Optional[Dict[Tuple[Hashable, Hashable], int]] = None,
    cost_fn: Optional[Callable[[Hashable, Hashable], int]] = None,
) -> TransportResult:
    """一般成本矩阵模式：真正的最小费用流，而非一维累计公式。

    供给/需求缩放到共同总量 p.total * q.total 的整数后求解，
    成本与流量再除以该缩放因子，结果仍为精确有理数。
    """
    _require_non_empty(p, q)
    if cost_fn is None:
        if cost_matrix is None:
            raise ValueError("general mode requires cost_matrix or cost_fn")
        cost_fn = lambda a, b: cost_matrix[(a, b)]  # noqa: E731
    scale = p.total * q.total
    supplies = {v: c * q.total for v, c in p.counts.items()}
    demands = {v: c * p.total for v, c in q.counts.items()}
    costs = {(a, b): cost_fn(a, b) for a in supplies for b in demands}
    int_flow, int_cost = min_cost_transport(supplies, demands, costs)
    flow = tuple(
        FlowEdge(a, b, Fraction(amount, scale)) for (a, b), amount in sorted(int_flow.items(), key=repr)
    )
    return TransportResult(Fraction(int_cost, scale), flow, ())


# ---------------------------------------------------------------- 成本验证器

@dataclass(frozen=True)
class FlowCheck:
    feasible: bool
    problems: Tuple[str, ...]
    cost: object
    expected_cost: Optional[object]
    optimal: Optional[bool]  # 仅当给出 expected_cost 且流可行时有意义


def verify_transport_flow(
    p: Histogram,
    q: Histogram,
    flow: Sequence[FlowEdge],
    cost_fn: Callable[[Hashable, Hashable], object],
    expected_cost: Optional[object] = None,
) -> FlowCheck:
    """验证输运流：非负、行和=类分布、列和=总体分布，并核算成本与最优性。"""
    problems: List[str] = []
    outflow: Dict[Hashable, Fraction] = {}
    inflow: Dict[Hashable, Fraction] = {}
    cost: object = Fraction(0)
    for edge in flow:
        if edge.amount < 0:
            problems.append(f"negative amount on {edge.source!r}->{edge.target!r}")
        if edge.source not in p.counts:
            problems.append(f"flow from {edge.source!r} not in class support")
        if edge.target not in q.counts:
            problems.append(f"flow to {edge.target!r} not in population support")
        outflow[edge.source] = outflow.get(edge.source, Fraction(0)) + edge.amount
        inflow[edge.target] = inflow.get(edge.target, Fraction(0)) + edge.amount
        cost += edge.amount * cost_fn(edge.source, edge.target)
    for v, c in p.counts.items():
        want = Fraction(c, p.total)
        if outflow.get(v, Fraction(0)) != want:
            problems.append(f"row sum at {v!r}: got {outflow.get(v, 0)}, want {want}")
    for v, c in q.counts.items():
        want = Fraction(c, q.total)
        if inflow.get(v, Fraction(0)) != want:
            problems.append(f"col sum at {v!r}: got {inflow.get(v, 0)}, want {want}")
    feasible = not problems
    optimal = None
    if expected_cost is not None:
        optimal = feasible and cost == expected_cost
    return FlowCheck(feasible, tuple(problems), cost, expected_cost, optimal)
