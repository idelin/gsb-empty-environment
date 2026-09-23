"""Polynomial revision: interval reuse and root identity matching.

Identity rules (no guessing by interval-center distance):
* a new root is the unique root of the new polynomial inside an old
  isolating interval  -> identity preserved (containment-based);
* an old interval containing k > 1 new roots -> a "split" link whose
  descendants form an ambiguous identity set;
* an old interval/point with no new root -> "vanished";
* a new root outside every old region -> "new";
* a new multiple root coexisting with vanished old roots -> a set-valued
  "merge_candidate" annotation, never a unique 1-1 guess.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Dict, List, Optional, Tuple

from .engine import (IsolationResult, Interval, RootCertificate, SturmEngine,
                     _root_sort_key)
from .polynomials import Poly, degree, evaluate, poly

REVISION_NOTES: Tuple[str, ...] = (
    "区间重用：在旧隔离区间上用新多项式的 Sturm 序列重新精确计数；计数为 1 时区间直接重用且身份保持。",
    "身份匹配仅依据区间包含关系与精确计数：计数 >1 记为分裂（后代为不确定身份集合），计数 0 记为消失。",
    "重根分裂/合并不按区间中心距离猜测唯一身份；只给出集合级的 split/merge 候选标注。",
    "端点恰根：若新多项式在旧区间右端点精确为零，按精确规则提取为点根并相应调整区间计数（左排右含）。",
    "间隙扫描：旧区间与保留点根把 (-B,B] 切块，仅对间隙重新隔离；保留点根处用 hi_excluded 精确排除，避免重复计数。",
)


@dataclass
class IdentityLink:
    relation: str  # preserved | split | vanished | new |
    #                # merge_candidate | split_candidate | pending
    old_ids: Tuple[str, ...]
    new_ids: Tuple[str, ...]
    detail: str


@dataclass
class RevisionResult:
    old_polynomial: Poly
    new_polynomial: Poly
    isolation: IsolationResult
    links: List[IdentityLink]
    reused_intervals: int
    notes: Tuple[str, ...] = REVISION_NOTES


def revise_polynomial(p, changes: Dict[int, object]) -> Poly:
    """Apply exact coefficient changes {power: new_value} to p."""
    coeffs = list(poly(p))
    for power, value in changes.items():
        while len(coeffs) <= power:
            coeffs.append(Fraction(0))
        coeffs[power] = Fraction(value)
    return poly(coeffs)


def revise_isolation(old: IsolationResult, p_new,
                     max_steps: Optional[int] = None) -> RevisionResult:
    """Reuse old isolating intervals for a revised polynomial.

    Old intervals are re-counted with the NEW polynomial's Sturm sequence
    and reused directly when they still isolate exactly one root. Budget
    interruption returns isolated roots plus pending intervals as usual.
    """
    eng = SturmEngine(p_new, id_prefix="n")
    p_new = eng.p
    budget = None if max_steps is None else [max_steps]
    links: List[IdentityLink] = []
    roots: List[RootCertificate] = []
    pending = []
    reused = 0
    vanished: List[str] = []
    covered: List[Tuple[Fraction, Fraction]] = []
    cut_points: List[Fraction] = []  # preserved exact roots excluded from gaps
    has_roots = degree(eng.q) > 0

    old_intervals = sorted((c for c in old.roots if c.kind == "interval"),
                           key=lambda c: c.interval.lo)
    old_points = [c for c in old.roots if c.kind == "point"]

    for cert in old_intervals:
        iv = cert.interval
        hi_excluded = iv.hi_excluded
        if has_roots and not hi_excluded and evaluate(eng.q, iv.hi) == 0:
            # The right endpoint is an exact root of the NEW polynomial:
            # extract it as a point root under the exact endpoint rule.
            pc = eng.point_certificate(iv.hi)
            roots.append(pc)
            cut_points.append(iv.hi)
            links.append(IdentityLink(
                "new", (), (pc.root_id,),
                "旧区间右端点恰为新多项式的根，按精确规则提取为点根"))
            hi_excluded = True
        region = Interval(iv.lo, iv.hi, hi_excluded)
        k = eng.count_interval(region)
        if k == 1:
            new_cert = eng.interval_certificate(region, root_id=cert.root_id)
            roots.append(new_cert)
            reused += 1
            links.append(IdentityLink(
                "preserved", (cert.root_id,), (cert.root_id,),
                "旧区间内新多项式恰有 1 个根：身份经包含关系保持（非距离猜测）"))
        elif k > 1:
            sub_roots, sub_pending = eng.refine(region, k, budget)
            roots.extend(sub_roots)
            pending.extend(sub_pending)
            if sub_roots:
                links.append(IdentityLink(
                    "split", (cert.root_id,),
                    tuple(r.root_id for r in sub_roots),
                    f"旧区间内现有 {k} 个根：后代身份为不确定集合，不指定唯一对应"))
            if sub_pending:
                links.append(IdentityLink(
                    "pending", (cert.root_id,), (),
                    "预算中断：旧区间内的分裂尚未完全解析"))
        else:
            vanished.append(cert.root_id)
            links.append(IdentityLink(
                "vanished", (cert.root_id,), (), "旧区间内新多项式无根"))
        covered.append((iv.lo, iv.hi))

    for cert in old_points:
        if has_roots and evaluate(eng.q, cert.point) == 0:
            pc = eng.point_certificate(cert.point, root_id=cert.root_id)
            roots.append(pc)
            cut_points.append(cert.point)
            links.append(IdentityLink(
                "preserved", (cert.root_id,), (cert.root_id,),
                "精确有理根值在新多项式下保持"))
        else:
            vanished.append(cert.root_id)
            links.append(IdentityLink(
                "vanished", (cert.root_id,), (),
                "旧点根不再是新多项式的根（精确求值判定）"))

    if has_roots:
        # Gaps between covered regions; preserved point roots cut the gaps
        # with hi_excluded so they are neither missed nor double counted.
        blockers = [(lo, hi, 1) for lo, hi in covered]
        blockers += [(t, t, 0) for t in cut_points]
        blockers.sort(key=lambda item: (item[0], item[2]))
        gaps: List[Interval] = []
        cursor = -eng.bound
        for lo, hi, kind in blockers:
            if cursor < lo:
                gaps.append(Interval(cursor, lo, hi_excluded=(kind == 0)))
            if hi > cursor:
                cursor = hi
        if cursor < eng.bound:
            gaps.append(Interval(cursor, eng.bound))
        for gap in gaps:
            kg = eng.count_interval(gap)
            if kg == 0:
                continue
            if kg == 1:
                rc = eng.interval_certificate(gap)
                roots.append(rc)
                links.append(IdentityLink(
                    "new", (), (rc.root_id,), "旧区间覆盖之外的新根"))
            else:
                sub_roots, sub_pending = eng.refine(gap, kg, budget)
                roots.extend(sub_roots)
                pending.extend(sub_pending)
                for rc in sub_roots:
                    links.append(IdentityLink(
                        "new", (), (rc.root_id,), "旧区间覆盖之外的新根"))
                if sub_pending:
                    links.append(IdentityLink(
                        "pending", (), (), "预算中断：间隙区域尚未完全解析"))

    new_ids = {link.new_ids[0] for link in links
               if link.relation == "new" and link.new_ids}
    if vanished:
        merged = [r for r in roots
                  if r.root_id in new_ids and r.multiplicity >= 2]
        for r in merged:
            links.append(IdentityLink(
                "merge_candidate", tuple(vanished), (r.root_id,),
                "新根重数>=2 且存在消失的旧根：可能为重根合并；对应关系不确定，不做距离猜测"))
        old_mult = {c.root_id: c.multiplicity for c in old.roots}
        vanished_multiple = [v for v in vanished if old_mult.get(v, 1) >= 2]
        if vanished_multiple and new_ids:
            links.append(IdentityLink(
                "split_candidate", tuple(vanished_multiple),
                tuple(sorted(new_ids)),
                "消失的旧根重数>=2：可能分裂为多个新根；身份对应不确定（集合级标注）"))

    roots.sort(key=_root_sort_key)
    total = eng.total_roots()
    isolation = IsolationResult(
        polynomial=p_new, squarefree_part=eng.q,
        squarefree_factors=eng.factors, roots=roots, pending=pending,
        completed=not pending, total_distinct_roots=total)
    return RevisionResult(old.polynomial, p_new, isolation, links, reused)
