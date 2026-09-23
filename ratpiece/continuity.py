"""Continuity analysis with exact algebraic evidence.

Every verdict is backed by exact rational arithmetic:

* one-sided limits at rational boundary points come from direct evaluation
  or from the parity of ``(x - b)`` multiplicities (pole sign);
* irrational poles inside a segment are located by Sturm-isolated intervals
  and their one-sided signs are decided by exact sign tests at rational
  endpoints of a separating interval;
* when the bisection budget is exhausted before isolation/separation
  succeeds, the point is reported as *undecided* — never guessed.

A point defined on only one side is reported as one-sided; the checker never
claims continuity from a single side.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction

from .piecewise import PiecewiseModel
from .rational import RationalFunc, SideLimit
from .roots import (
    DEFAULT_BUDGET,
    IsolatedRoot,
    analyze_roots,
    intervals_overlap,
    refine_root,
)
from .poly import sturm_sequence


@dataclass
class BoundaryReport:
    point: Fraction
    kind: str
    defined: bool
    value: Fraction | None = None
    left: SideLimit | None = None
    right: SideLimit | None = None
    detail: str = ""
    evidence: tuple[str, ...] = ()


@dataclass
class SingularityReport:
    kind: str               # 'irrational_pole' | 'irrational_removable' | 'undecided'
    lo: Fraction
    hi: Fraction
    detail: str = ""
    left_sign: int | None = None
    right_sign: int | None = None
    evidence: tuple[str, ...] = ()


@dataclass
class ContinuityReport:
    name: str
    boundaries: list[BoundaryReport] = field(default_factory=list)
    singularities: list[SingularityReport] = field(default_factory=list)

    def has_undecided(self) -> bool:
        return (any(b.kind == "undecided" for b in self.boundaries)
                or any(s.kind == "undecided" for s in self.singularities))

    def summary(self) -> str:
        lines = [f"连续性报告：{self.name}"]
        for b in self.boundaries:
            lines.append(f"  边界 {b.point}: [{b.kind}] {b.detail}")
        for s in self.singularities:
            lines.append(f"  区间 ({s.lo}, {s.hi}): [{s.kind}] {s.detail}")
        if not self.boundaries and not self.singularities:
            lines.append("  无内部边界与奇点：在定义域的每个区间上连续")
        if self.has_undecided():
            lines.append("  结论：存在未决点（预算耗尽），未作猜测")
        return "\n".join(lines)


def _fmt_limit(lim: SideLimit | None) -> str:
    if lim is None:
        return "无定义"
    if lim.kind == "value":
        return str(lim.value)
    if lim.kind == "pole":
        return ("+" if lim.sign > 0 else "-") + "∞"
    return "未决"


def check_continuity(model: PiecewiseModel,
                     budget: int = DEFAULT_BUDGET) -> ContinuityReport:
    report = ContinuityReport(model.name)
    for b in model.boundaries:
        report.boundaries.append(_check_boundary(model, b, budget))
    report.singularities = _check_segment_singularities(model, budget)
    return report


def _check_boundary(model: PiecewiseModel, b: Fraction,
                    budget: int) -> BoundaryReport:
    left_cell, point_cell, right_cell = model.cells_around(b)
    l_expr = model.cell_expr(left_cell) if left_cell.contributors else None
    r_expr = model.cell_expr(right_cell) if right_cell.contributors else None
    p_expr = model.cell_expr(point_cell) if point_cell.contributors else None
    defined = p_expr is not None and p_expr.is_defined_at(b)
    value = p_expr.eval(b) if defined else None
    left_lim = l_expr.side_limits(b, budget)[0] if l_expr else None
    right_lim = r_expr.side_limits(b, budget)[1] if r_expr else None
    evidence: list[str] = []
    for lim, side in ((left_lim, "左"), (right_lim, "右")):
        if lim is not None:
            evidence.extend(f"{side}侧：{e}" for e in lim.evidence)
    srcs = sorted(set(model.cell_sources(point_cell)))
    if srcs:
        evidence.append(f"该点取值来源：{', '.join(srcs)}")

    rep = BoundaryReport(b, "undefined", defined, value,
                         left_lim, right_lim, "", tuple(evidence))

    if left_lim is None and right_lim is None:
        rep.kind = "undefined"
        rep.detail = "两侧均无定义"
        return rep
    if left_lim is None or right_lim is None:
        side = right_lim if left_lim is None else left_lim
        which = "右" if left_lim is None else "左"
        rep.kind = "one_sided"
        if side.kind == "undecided":
            rep.kind = "undecided"
            rep.detail = f"仅{which}侧有定义，且该侧极限未决"
            return rep
        side_s = _fmt_limit(side)
        if defined and side.kind == "value" and value == side.value:
            rep.detail = (f"仅{which}侧有定义；{which}极限 {side_s} 等于函数值 {value}，"
                          f"故在 {b} 处{which}侧连续（不冒称双侧连续）")
        elif defined:
            rep.detail = (f"仅{which}侧有定义；{which}极限 {side_s}，函数值 {value}，"
                          f"{which}侧不连续")
        else:
            rep.detail = f"仅{which}侧有定义（{which}极限 {side_s}），该点本身无定义"
        return rep

    if left_lim.kind == "undecided" or right_lim.kind == "undecided":
        rep.kind = "undecided"
        rep.detail = "至少一侧极限未决（预算耗尽），保留未决边界"
        return rep
    if left_lim.kind == "pole" or right_lim.kind == "pole":
        rep.kind = "infinite"
        rep.detail = (f"无穷间断：左极限 {_fmt_limit(left_lim)}，"
                      f"右极限 {_fmt_limit(right_lim)}")
        return rep
    lv, rv = left_lim.value, right_lim.value
    if lv != rv:
        rep.kind = "jump"
        rep.detail = (f"跳变：左极限 {lv}，右极限 {rv}，跃度 {rv - lv}"
                      + (f"，函数值 {value}" if defined else "，该点无定义"))
        return rep
    if defined and value == lv:
        rep.kind = "continuous"
        rep.detail = f"连续：左右极限与函数值均为 {lv}"
        return rep
    rep.kind = "removable"
    if defined:
        rep.detail = f"可去间断：极限 {lv} 存在，但函数值 {value} 与之不同"
    else:
        rep.detail = f"可去间断：极限 {lv} 存在，但该点无定义（定义域空洞）"
    return rep


def _check_segment_singularities(model: PiecewiseModel,
                                 budget: int) -> list[SingularityReport]:
    reports: list[SingularityReport] = []
    seen: set[tuple] = set()
    for seg in model.segs:
        if not seg.contributors:
            continue
        expr = model.cell_expr(seg)
        if expr is None:
            continue
        key = (expr.num.coeffs, expr.den.coeffs, expr.original_den.coeffs)
        if key in seen:
            continue
        seen.add(key)
        reports.extend(_singularities_of(expr, budget))
    reports.sort(key=lambda s: (s.lo, s.hi))
    return reports


def _singularities_of(expr: RationalFunc, budget: int) -> list[SingularityReport]:
    out: list[SingularityReport] = []
    den_analysis = expr.den_root_analysis(budget)
    for cluster in den_analysis.unknown:
        out.append(SingularityReport(
            "undecided", cluster.lo, cluster.hi,
            detail=f"分母在 ({cluster.lo}, {cluster.hi}) 内尚有 {cluster.count} 个根"
                   "未能分离（预算耗尽），保留未决",
            evidence=("Sturm 二分预算耗尽",)))
    for iso in den_analysis.isolated:
        out.append(_irrational_pole(expr, iso, budget))
    hole_analysis = expr.hole_root_analysis(budget)
    for cluster in hole_analysis.unknown:
        out.append(SingularityReport(
            "undecided", cluster.lo, cluster.hi,
            detail="可去奇点位置未能隔离（预算耗尽），保留未决",
            evidence=("Sturm 二分预算耗尽",)))
    for iso in hole_analysis.isolated:
        out.append(SingularityReport(
            "irrational_removable", iso.lo, iso.hi,
            detail=f"无理可去奇点位于 ({iso.lo}, {iso.hi}) 内；"
                   "该点无定义，双侧极限为同一代数数",
            evidence=("约去的公因式在此区间内有唯一实根（Sturm 隔离）",)))
    return out


def _irrational_pole(expr: RationalFunc, iso: IsolatedRoot,
                     budget: int) -> SingularityReport:
    """Locate an irrational pole and decide both side signs exactly."""
    chain = sturm_sequence(expr.den)
    spend = [budget]
    iv = refine_root(chain, iso, spend)
    num_analysis = analyze_roots(expr.num, budget) if not expr.num.is_zero() else None
    num_exact = {r.value for r in num_analysis.rational} if num_analysis else set()
    num_isolated = list(num_analysis.isolated) if num_analysis else []

    def separated(iv: IsolatedRoot) -> bool:
        if any(iv.lo < r < iv.hi for r in num_exact):
            return False
        return not any(intervals_overlap(iv.lo, iv.hi, n.lo, n.hi)
                       for n in num_isolated)

    while not separated(iv) and spend[0] > 0:
        iv = refine_root(chain, iv, spend)
    if not separated(iv):
        return SingularityReport(
            "undecided", iv.lo, iv.hi,
            detail="极点与分子零点未能分离（预算耗尽），单侧极限符号未决",
            evidence=("分离所需二分次数超出预算",))
    num_sign = expr.num.sign_at(iv.lo)
    if num_sign == 0:
        num_sign = expr.num.sign_at(iv.hi)
    left_sign = num_sign * expr.den.sign_at(iv.lo)
    right_sign = num_sign * expr.den.sign_at(iv.hi)
    evidence = (
        f"分母在 ({iv.lo}, {iv.hi}) 内有唯一实根（Sturm 隔离）",
        f"分子在该区间无零点，符号 {'+' if num_sign > 0 else '-'}",
        f"分母在左端点符号 {'+' if expr.den.sign_at(iv.lo) > 0 else '-'}、"
        f"右端点符号 {'+' if expr.den.sign_at(iv.hi) > 0 else '-'}",
    )
    return SingularityReport(
        "irrational_pole", iv.lo, iv.hi,
        detail=(f"无理极点位于 ({iv.lo}, {iv.hi}) 内；"
                f"左侧极限 {'+' if left_sign > 0 else '-'}∞，"
                f"右侧极限 {'+' if right_sign > 0 else '-'}∞"),
        left_sign=left_sign, right_sign=right_sign, evidence=evidence)

