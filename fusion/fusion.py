"""多字段阈值式 TopK 融合（WAND/NRA 风格的上界停止）。

打分与缺失字段规则（公开、可复算）
----------------------------------
- 每个正权字段的归一化分在 [0,1]（见 normalize.py）。
- 文档 d 的融合分只在其"出现过的正权字段"集合 P(d) 上做权重平均：

      score(d) = sum_{f in P(d)} w_f * n_f(d) / sum_{f in P(d)} w_f

  因此缺失字段不产生 0 惩罚；分母随文档实际出现的字段变化。
- 在所有正权字段候选列表里都没出现的文档不参与排名。
- 权重 0 字段不取候选、不进分母、不影响排名。

上界与停止
----------
对部分命中的文档，未取字段 f 乐观取 cap(f)（剩余列表头部分）；对"还没
在任何字段见过"的文档取所有活跃字段 cap 的最大值。P(d) 未知时用贪心
分数规划求最优可达平均（greedy_upper_bound，含正确性说明）。
停止由剩余得分上界严格证明：当 TopK 中已有 K 个"已定"（全部字段要么
命中要么字段列表已耗尽）文档，且第 K 名的 (score,-id) 键严格大于每个
未定候选与未见候选的上界键时停止；严格大于保证并列不被截断。

拒绝 / 退化
-----------
- 负权重或权重全 0：直接拒绝（ValueError 子类）。
- 字段无界（bounded=False）或运行中观测到归一化分越界（违反 [0,1]
  假设）：拒绝"提前停止"，退化为全量精确融合，并记录退化原因。
"""

from __future__ import annotations

import math
from dataclasses import dataclass, field

from .index import FieldPlan, Index, Posting


class NegativeWeightError(ValueError):
    """负权重：单调上界假设不成立，直接拒绝。"""


class AllZeroWeightsError(ValueError):
    """权重全为 0：无法定义排名，直接拒绝。"""


@dataclass(frozen=True)
class Hit:
    doc_id: str
    score: float
    field_scores: dict[str, float]
    missing_fields: tuple[str, ...]
    exact: bool


@dataclass
class FuseResult:
    hits: list[Hit]
    mode: str
    degraded: bool
    degrade_reasons: tuple[str, ...]
    stop_reason: str
    pages_fetched: dict[str, int]
    docs_seen: int
    trace: list[str] = field(default_factory=list)


def validate_weights(weights: dict[str, float]) -> dict[str, float]:
    clean: dict[str, float] = {}
    for name, w in weights.items():
        wf = float(w)
        if math.isnan(wf) or math.isinf(wf):
            raise ValueError(f"字段 {name!r} 权重非法: {w}")
        if wf < 0.0:
            raise NegativeWeightError(f"字段 {name!r} 权重为负: {wf}")
        clean[name] = wf
    if not clean or all(w == 0.0 for w in clean.values()):
        raise AllZeroWeightsError("至少需要一个权重为正的字段")
    return clean


def exact_score(field_scores: dict[str, float],
                weights: dict[str, float]) -> float:
    """公开缺失规则：只在实际出现的正权字段上做权重平均。"""
    num = den = 0.0
    for f, n in field_scores.items():
        w = weights.get(f, 0.0)
        if w > 0.0:
            num += w * n
            den += w
    if den <= 0.0:
        raise ValueError("没有任何正权字段命中，无法计分")
    return num / den


def greedy_upper_bound(known: dict[str, float], caps: dict[str, float],
                       weights: dict[str, float]) -> float:
    """已知部分命中时的最优可达融合分上界。

    未取字段 f 可乐观取 [0, cap_f] 中任意值，且它可能"出现"或"缺失"。
    缺失规则的分母随出现集合变化，因此等价于：为未知字段选一个子集及其
    分值，使加权平均最大（已知字段固定）。

    单遍贪心正确性（分数规划）：按 cap 降序考虑未知字段。当前平均为 A
    时，加入 (w, cap) 会把平均推向 cap；cap > A 则平均上升，且按降序处理
    时 A 单调上升，之后更小的 cap 不可能再超过 A；若 cap <= A 即可立即
    停止。处理完即为全局最优。
    """
    num = den = 0.0
    for f, n in known.items():
        w = weights.get(f, 0.0)
        if w > 0.0:
            num += w * n
            den += w

    unknown = [
        (cap, weights[f])
        for f, cap in caps.items()
        if f not in known and weights.get(f, 0.0) > 0.0 and cap > 0.0
    ]
    unknown.sort(key=lambda x: (-x[0], x[1]))
    for cap, w in unknown:
        current = num / den if den > 0.0 else -1.0
        if cap <= current:
            break
        num += w * cap
        den += w
    if den <= 0.0:
        return 0.0
    return num / den


def _sorted_key(score: float, doc_id: str) -> tuple[float, str]:
    """排名键：分数降序、doc_id 升序。"""
    return (-score, doc_id)


def _dominates(a_score: float, a_id: str,
              b_score: float, b_id: str) -> bool:
    """a 是否严格优于 b：分数高，或同分而 doc_id 小（排名并列打破规则）。"""
    if a_score > b_score:
        return True
    if a_score < b_score:
        return False
    return a_id < b_id


def _make_hit(doc_id: str, scores: dict[str, float], weights: dict[str, float],
              all_fields: tuple[str, ...], exact: bool) -> Hit:
    missing = tuple(f for f in all_fields if f not in scores)
    return Hit(
        doc_id=doc_id,
        score=exact_score(scores, weights),
        field_scores=dict(sorted(scores.items())),
        missing_fields=missing,
        exact=exact,
    )


def _check_norms(postings: list[Posting], field: str) -> float | None:
    for p in postings:
        if not (-1e-9 <= p.norm <= 1.0 + 1e-9):
            return p.norm
    return None


def _build_plans(index: Index, terms: list[str], weights: dict[str, float],
                 bounded: dict[str, bool] | None,
                 page_size: int) -> list[FieldPlan]:
    bounded = bounded or {}
    plans: list[FieldPlan] = []
    for f in sorted(weights):
        if weights[f] <= 0.0:
            continue
        snap = index.get_snapshot(terms, f)
        plans.append(FieldPlan(
            field=f, weight=weights[f], snapshot=snap,
            bounded=bounded.get(f, True), page_size=page_size,
        ))
    return plans


def _exhaustive(plans: list[FieldPlan], weights: dict[str, float], k: int,
                mode: str, reasons: list[str], stop_reason: str,
                trace: list[str], page_counts: dict[str, int],
                *, degraded: bool = True) -> FuseResult:
    all_fields = tuple(p.field for p in plans)
    per_doc: dict[str, dict[str, float]] = {}
    pages_fetched = dict(page_counts)
    for plan in plans:
        postings = plan.snapshot.postings
        pages_fetched[plan.field] = pages_fetched.get(plan.field, 0) + 1
        trace.append(f"[exhaustive] {plan.field}: 全量读取 {len(postings)} 条")
        for p in postings:
            per_doc.setdefault(p.doc_id, {})[plan.field] = p.norm
    hits = [
        _make_hit(d, s, weights, all_fields, exact=True)
        for d, s in per_doc.items()
    ]
    hits.sort(key=lambda h: _sorted_key(h.score, h.doc_id))
    return FuseResult(
        hits=hits[:k], mode=mode, degraded=degraded,
        degrade_reasons=tuple(reasons), stop_reason=stop_reason,
        pages_fetched=pages_fetched, docs_seen=len(per_doc), trace=trace,
    )


def _build_result(candidates: dict[str, dict], paginators, weights,
                  all_fields: tuple[str, ...], pages_fetched: dict[str, int],
                  trace: list[str], *, stop: str, mode: str, degraded: bool,
                  reasons: tuple[str, ...], k: int) -> FuseResult:
    hits: list[Hit] = []
    for doc_id, entry in candidates.items():
        hits.append(_make_hit(
            doc_id, entry["scores"], weights, all_fields,
            exact=bool(entry["final"]),
        ))
    hits.sort(key=lambda h: _sorted_key(h.score, h.doc_id))
    chosen = hits[:k]
    return FuseResult(
        hits=chosen, mode=mode, degraded=degraded, degrade_reasons=reasons,
        stop_reason=stop, pages_fetched=dict(pages_fetched),
        docs_seen=len(candidates), trace=trace,
    )


def fuse_topk(index: Index, query: str | list[str],
              weights: dict[str, float], k: int = 10, *,
              page_size: int = 16,
              bounded: dict[str, bool] | None = None) -> FuseResult:
    """阈值式多字段 TopK 融合；上界假设不成立时自动退化全量精确融合。"""
    from .tokenizer import tokenize

    if not isinstance(k, int) or k < 1:
        raise ValueError("k 必须是 >= 1 的整数")
    weights = validate_weights(weights)
    terms = list(dict.fromkeys(
        tokenize(query) if isinstance(query, str) else query
    ))

    plans = _build_plans(index, terms, weights, bounded, page_size)
    weights = {p.field: p.weight for p in plans}
    all_fields = tuple(p.field for p in plans)
    trace = [f"查询词元: {terms}；正权字段: {list(weights)}"]

    reasons = [
        f"字段 {p.field!r} 无界（bounded=False），上界假设不成立"
        for p in plans if not p.bounded
    ]
    if reasons:
        trace.append("拒绝提前停止 -> 退化全量: " + "; ".join(reasons))
        return _exhaustive(plans, weights, k, "degraded_exhaustive",
                           reasons, "unbounded_field", trace, {})

    paginators = {p.field: p.paginator() for p in plans}
    pages_fetched = {f: 0 for f in weights}
    candidates: dict[str, dict] = {}

    def merge(field: str, page: list[Posting]) -> list[str]:
        violations: list[str] = []
        for p in page:
            bad = _check_norms([p], field)
            if bad is not None:
                violations.append(
                    f"字段 {field!r} 归一化分越界: doc={p.doc_id} norm={bad}")
            entry = candidates.setdefault(p.doc_id, {"scores": {}, "final": False})
            if field in entry["scores"]:
                raise AssertionError(f"{p.doc_id} 在字段 {field} 被重复计入")
            entry["scores"][field] = p.norm
        return violations

    def refresh_final() -> None:
        for entry in candidates.values():
            if entry["final"]:
                continue
            covered = {
                f for f, pg in paginators.items()
                if pg.exhausted or f in entry["scores"]
            }
            if len(covered) == len(paginators):
                entry["final"] = True

    while True:
        caps: dict[str, float] = {}
        violations: list[str] = []
        for f, pg in paginators.items():
            cap = pg.cap()
            if cap is None:
                violations.append(f"字段 {f!r} 运行时失去上界")
                continue
            caps[f] = cap
        if violations:
            uniq = list(dict.fromkeys(violations))
            trace.append("运行中违反上界假设 -> 退化全量: " + "; ".join(uniq))
            return _exhaustive(plans, weights, k, "degraded_exhaustive",
                               uniq, "unbounded_runtime", trace, pages_fetched)

        refresh_final()

        final_entries = [
            (d, e) for d, e in candidates.items() if e["final"]
        ]
        final_entries.sort(
            key=lambda de: _sorted_key(exact_score(de[1]["scores"], weights), de[0]))
        threshold = None
        if len(final_entries) >= k:
            tdoc, tentry = final_entries[k - 1]
            threshold = (exact_score(tentry["scores"], weights), tdoc)

        unseen_cap = max(caps.values(), default=0.0)

        def is_finalized(d: str, e: dict) -> bool:
            return bool(e["final"])

        undecided = [(d, e) for d, e in candidates.items()
                     if not is_finalized(d, e)]
        best_ub: tuple[float, str] | None = None
        for d, e in undecided:
            ub = greedy_upper_bound(e["scores"], caps, weights)
            if best_ub is None or _dominates(ub, d, best_ub[0], best_ub[1]):
                best_ub = (ub, d)

        all_done = all(pg.exhausted for pg in paginators.values())

        if threshold is not None and not all_done:
            ts, tid = threshold
            # 未定候选：阈值必须严格优于其上界键。
            undecided_ok = all(
                _dominates(ts, tid, greedy_upper_bound(e["scores"], caps, weights), d)
                for d, e in undecided
            )
            # 未见候选：其 id 未知，最保守地认为 id 比 tid 小，
            # 故仅当阈值分严格大于未见上界分才安全（同分不算停止）。
            unseen_ok = ts > unseen_cap
            if undecided_ok and unseen_ok:
                trace.append(
                    f"阈值 {ts:.6f} 严格大于剩余上界"
                    f"（未定 {best_ub[0] if best_ub else '-'}，"
                    f"未见 {unseen_cap:.6f}）-> 上界证明，提前停止")
                return _build_result(
                    candidates, paginators, weights, all_fields,
                    pages_fetched, trace, stop="upper_bound_proven",
                    mode="threshold", degraded=False, reasons=(), k=k)

        if all_done:
            trace.append("所有字段候选已取完 -> 精确融合")
            return _build_result(
                candidates, paginators, weights, all_fields,
                pages_fetched, trace, stop="all_exhausted",
                mode="threshold", degraded=False, reasons=(), k=k)

        field = min(
            (f for f, pg in paginators.items() if not pg.exhausted),
            key=lambda f: (-caps[f], f),
        )
        page = paginators[field].fetch_page()
        pages_fetched[field] += 1
        violations = merge(field, page)
        if violations:
            uniq = list(dict.fromkeys(violations))
            trace.append("观测到归一化分越界 -> 退化全量: " + "; ".join(uniq))
            return _exhaustive(plans, weights, k, "degraded_exhaustive",
                               uniq, "norm_violation", trace, pages_fetched)
        head = paginators[field].head()
        trace.append(
            f"读取 {field} 第 {pages_fetched[field]} 页（{len(page)} 条），"
            f"剩余帽={head.norm if head else 0.0:.6f}")


def exhaustive_fuse(index: Index, query: str | list[str],
                    weights: dict[str, float], k: int = 10) -> FuseResult:
    """全量融合参考核验：读全每个正权字段的固定快照后精确计分。"""
    from .tokenizer import tokenize

    if not isinstance(k, int) or k < 1:
        raise ValueError("k 必须是 >= 1 的整数")
    weights = validate_weights(weights)
    terms = list(dict.fromkeys(
        tokenize(query) if isinstance(query, str) else query
    ))
    plans = _build_plans(index, terms, weights, None, page_size=10**9)
    weights = {p.field: p.weight for p in plans}
    trace = [f"[参考] 查询词元: {terms}"]
    return _exhaustive(plans, weights, k, "exact_exhaustive", [],
                       "reference_full_scan", trace, {}, degraded=False)
