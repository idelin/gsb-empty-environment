"""校正主流程：最少删锚点精确搜索 + 由全部可行映射推出查询上下界。

流程：

1. 对保留锚点集合的可行性用精确有理 LP 判定；
2. 自预算 0 起逐级加深，分支由"极小冲突核"指导（核中至少删一个），
   第一个产生可行方案的预算即最少删除数——这是最优性证据的一部分，
   另一部分是上一级预算搜索树全部叶节点不可行的记录；
3. 预算受限且无法判定可行时，状态记为 undecided_within_budget，
   与已确定的最优结果分开报告（预算未决分开）；
4. 查询时对所有最优方案（最少删除的可行映射族）分别求
   min/max 线性规划，上下界取并集；外推超过允许距离拒绝。
"""

from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from math import comb
from typing import Optional, Sequence, Union

from . import lp
from .anchors import Anchor
from .enumcheck import enum_bounds
from .model import PiecewiseModel
from .system import build_system, query_objective
from .time_model import Duration, Instant, WallTime, to_fraction


class ExtrapolationError(ValueError):
    """查询点超出所有可行方案的允许外推距离。"""


class JunctionSideError(ValueError):
    """查询点落在声明不连续的交界上，但未指明取哪一侧。"""


class UndecidedError(RuntimeError):
    """预算内未决，无法给出校正结果。"""


@dataclass(frozen=True)
class Scenario:
    """一个最优方案：删除集合 + 保留集合 + 一个代表可行映射。"""

    removed: frozenset[int]
    kept: frozenset[int]
    representative: tuple[tuple[Fraction, Fraction], ...]

    def value_at(self, segment: int, x: Fraction) -> Fraction:
        alpha, beta = self.representative[segment]
        return alpha * x + beta


@dataclass
class Reconciliation:
    """reconcile 的完整输出。"""

    model: PiecewiseModel
    anchors: list[Anchor]
    extrapolation_margin: Fraction
    status: str  # "optimal" | "undecided_within_budget"
    budget: Optional[int]
    min_removed: Optional[int]
    scenarios: list[Scenario] = field(default_factory=list)
    conflict_cores: list[list[int]] = field(default_factory=list)
    optimality_proof: str = ""
    trace: list[str] = field(default_factory=list)
    cross_check: bool = False

    def removed_names(self) -> list[str]:
        if not self.scenarios:
            return []
        return [self.anchors[i].label() for i in sorted(self.scenarios[0].removed)]


@dataclass(frozen=True)
class Correction:
    """单次查询的校正值与不确定范围。"""

    local: WallTime
    segment: int
    lower: Instant
    upper: Instant
    scenarios_used: int

    @property
    def mid(self) -> Instant:
        return Instant((self.lower.seconds + self.upper.seconds) / 2)

    @property
    def uncertainty(self) -> Duration:
        """半宽（±）。"""
        return Duration((self.upper.seconds - self.lower.seconds) / 2)

    def describe(self) -> str:
        return (
            f"{self.local.exact()} -> [{self.lower.exact()}, {self.upper.exact()}] "
            f"(mid {self.mid.approx()}, ±{self.uncertainty.approx()}, "
            f"段 {self.segment}, {self.scenarios_used} 个方案)"
        )


def _system_for(model: PiecewiseModel, anchors: Sequence[Anchor], kept: frozenset[int]):
    kept_sorted = sorted(kept)
    sub = [anchors[i] for i in kept_sorted]
    system = build_system(model, sub)
    system.anchor_rows = {
        kept_sorted[pos]: rows for pos, rows in system.anchor_rows.items()
    }
    return system


def reconcile(
    model: PiecewiseModel,
    anchors: Sequence[Anchor],
    *,
    budget: Optional[int] = None,
    extrapolation_margin: Union[int, str, Fraction] = 0,
    cross_check: bool = False,
) -> Reconciliation:
    """求最少删锚点的可行分段仿射映射族。

    budget=None 时精确求最少删除数；否则只在预算内判定，
    无法判定时 status="undecided_within_budget"（预算未决）。
    """
    anchors = list(anchors)
    n = len(anchors)
    margin = to_fraction(extrapolation_margin)
    if margin < 0:
        raise ValueError("外推允许距离必须非负")

    # 同一本地时间不同参考值：区间不相交即冲突——这天然体现为 LP 不可行，
    # 冲突核会精确指认；这里不做任何阈值式特判。

    trace: list[str] = []
    cores: list[frozenset[int]] = []
    feas_cache: dict[frozenset[int], bool] = {}

    def is_feasible(kept: frozenset[int]) -> bool:
        if kept in feas_cache:
            return feas_cache[kept]
        system = _system_for(model, anchors, kept)
        res = lp.feasibility(
            system.a_eq, system.b_eq, system.a_ub, system.b_ub,
            system.free_vars, system.n_vars,
        )
        feas_cache[kept] = res.feasible
        return res.feasible

    def extract_core(kept: frozenset[int]) -> list[int]:
        return lp.minimize_core(is_feasible, sorted(kept))

    def representative(kept: frozenset[int]) -> tuple[tuple[Fraction, Fraction], ...]:
        system = _system_for(model, anchors, kept)
        res = lp.solve_lp(
            [Fraction(0)] * system.n_vars,
            a_eq=system.a_eq, b_eq=system.b_eq,
            a_ub=system.a_ub, b_ub=system.b_ub,
            free_vars=system.free_vars,
        )
        assert res.feasible and res.x is not None
        return tuple(
            (res.x[2 * i], res.x[2 * i + 1]) for i in range(model.n_segments)
        )

    max_level = budget if budget is not None else n
    scenarios: list[Scenario] = []
    level_stats: list[tuple[int, int]] = []  # (预算, 访问节点数)
    found_level: Optional[int] = None

    for level in range(max_level + 1):
        level_scenarios: list[Scenario] = []
        seen: set[frozenset[int]] = set()
        nodes = 0

        def dfs(kept: frozenset[int], removed: frozenset[int]) -> None:
            nonlocal nodes
            nodes += 1
            if is_feasible(kept):
                if removed not in seen:
                    seen.add(removed)
                    level_scenarios.append(
                        Scenario(
                            removed=removed,
                            kept=kept,
                            representative=representative(kept),
                        )
                    )
                return
            core = extract_core(kept)
            core_fs = frozenset(core)
            if core_fs not in cores:
                cores.append(core_fs)
            if len(removed) >= level:
                trace.append(
                    f"预算 {level} 不可行叶: 已删 {sorted(removed)}, "
                    f"冲突核 {core}"
                )
                return
            for idx in core:
                dfs(kept - {idx}, removed | {idx})

        dfs(frozenset(range(n)), frozenset())
        level_stats.append((level, nodes))
        if level_scenarios:
            scenarios = level_scenarios
            found_level = level
            break

    if found_level is None:
        proof = (
            f"预算 {max_level} 内未决：搜索树叶节点均不可行，"
            f"最少删除数 > {max_level}；"
            f"已收集 {len(cores)} 个极小冲突核作为证据。"
        )
        return Reconciliation(
            model=model,
            anchors=anchors,
            extrapolation_margin=margin,
            status="undecided_within_budget",
            budget=budget,
            min_removed=None,
            scenarios=[],
            conflict_cores=[sorted(c) for c in cores],
            optimality_proof=proof,
            trace=trace,
            cross_check=cross_check,
        )

    if found_level == 0:
        proof = "预算 0 即可行：全部锚点保留，无需删除。"
    else:
        prev_nodes = level_stats[found_level - 1][1]
        proof = (
            f"最优性证据：预算 {found_level - 1} 的搜索树"
            f"（{prev_nodes} 个节点）全部叶节点不可行，"
            f"故最少删除数 >= {found_level}；"
            f"预算 {found_level} 找到 {len(scenarios)} 个可行方案，"
            f"故最少删除数 = {found_level}。"
        )

    return Reconciliation(
        model=model,
        anchors=anchors,
        extrapolation_margin=margin,
        status="optimal",
        budget=budget,
        min_removed=found_level,
        scenarios=scenarios,
        conflict_cores=[sorted(c) for c in cores],
        optimality_proof=proof,
        trace=trace,
        cross_check=cross_check,
    )


def _resolve_segment(model: PiecewiseModel, x: Fraction, side: Optional[str]) -> int:
    for i, bp in enumerate(model.breakpoints):
        if x < bp:
            return i
        if x == bp:
            if model.junctions[i].continuous:
                return i
            if side == "left":
                return i
            if side == "right":
                return i + 1
            raise JunctionSideError(
                f"本地时间 {x} 位于声明不连续的交界点，"
                "请用 side='left' 或 side='right' 指明取哪一侧"
            )
    return model.n_segments - 1


_ENUM_ROW_LIMIT = 24  # 对照枚举的行数上限（组合爆炸保护）


def correct(
    recon: Reconciliation,
    local: WallTime,
    *,
    side: Optional[str] = None,
) -> Correction:
    """把本地墙钟时间校正为参考时间点区间。

    上下界由所有最优方案的可行映射推出（精确 LP），
    可选地用有理约束枚举对照。外推超过允许距离抛 ExtrapolationError。
    """
    if recon.status != "optimal":
        raise UndecidedError(recon.optimality_proof)
    model = recon.model
    x = local.seconds
    segment = _resolve_segment(model, x, side)

    lower: Optional[Fraction] = None
    upper: Optional[Fraction] = None
    used = 0
    for scenario in recon.scenarios:
        kept_xs = [recon.anchors[i].x for i in sorted(scenario.kept)]
        if not kept_xs:
            continue
        cover_lo = min(kept_xs) - recon.extrapolation_margin
        cover_hi = max(kept_xs) + recon.extrapolation_margin
        if x < cover_lo or x > cover_hi:
            continue  # 该方案下属于超距外推
        system = _system_for(model, recon.anchors, scenario.kept)
        objective = query_objective(model, segment, x)
        lo_res = lp.solve_lp(
            objective,
            a_eq=system.a_eq, b_eq=system.b_eq,
            a_ub=system.a_ub, b_ub=system.b_ub,
            free_vars=system.free_vars, sense="min",
        )
        hi_res = lp.solve_lp(
            objective,
            a_eq=system.a_eq, b_eq=system.b_eq,
            a_ub=system.a_ub, b_ub=system.b_ub,
            free_vars=system.free_vars, sense="max",
        )
        if lo_res.unbounded or hi_res.unbounded:
            raise UndecidedError("可行映射在该查询点无界（段内锚点不足）")
        assert lo_res.value is not None and hi_res.value is not None

        if recon.cross_check:
            n_rows = len(system.a_ub) + 2 * len(system.a_eq)
            if n_rows <= _ENUM_ROW_LIMIT:
                checked = enum_bounds(system, objective)
                assert checked is not None
                assert checked == (lo_res.value, hi_res.value), (
                    f"枚举对照不一致: {checked} != {(lo_res.value, hi_res.value)}"
                )

        lo, hi = lo_res.value, hi_res.value
        lower = lo if lower is None else min(lower, lo)
        upper = hi if upper is None else max(upper, hi)
        used += 1

    if used == 0 or lower is None or upper is None:
        raise ExtrapolationError(
            f"本地时间 {x} 超出所有可行方案的外推允许距离"
            f"（±{recon.extrapolation_margin}）"
        )
    return Correction(
        local=local,
        segment=segment,
        lower=Instant(lower),
        upper=Instant(upper),
        scenarios_used=used,
    )
