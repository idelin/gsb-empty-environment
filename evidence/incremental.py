"""增量求解器：修改一个事实后，只撤销受影响时间段并局部重算。

正确性论证（区间有界情形）：
- 若事实 f 的版本变化只影响业务区间/可见性（来源不变），且 f 在新旧结果中
  都不参与空交集团，则所有含 f 的团的共同区间都落在 span = 旧区间 ∪ 新区间 内；
  不含 f 的团若极大性改变（因 f 可加入/退出），其区间必与 I(f) 相接，即触及 span。
  因此不触及 span 的段在新旧结果中逐字相同，可以保留，只需撤销触及 span 的段。
- 否则（来源变化或涉及空交集团）影响范围无界，回退为全量撤销重算。
"""
from __future__ import annotations

from dataclasses import dataclass

from .model import EvidenceStore, FactVersion
from .solver import SolveResult, _merge_intervals, solve_store


@dataclass(frozen=True)
class UpdateReport:
    fact_ref: str
    span: tuple          # 受影响时间段列表 ((lo, hi), ...)
    full_refresh: bool   # 是否回退为全量重算
    revoked: tuple       # 被撤销的段 ID
    kept: tuple          # 保留的段 ID
    added: tuple         # 重算产生的段 ID
    result: SolveResult


class IncrementalSolver:
    def __init__(self, store: EvidenceStore, observation: float, budget=None) -> None:
        self.store = store
        self.observation = observation
        self.budget = budget
        self._result = solve_store(store, observation, budget)

    @property
    def result(self) -> SolveResult:
        return self._result

    def _visible_by_id(self) -> dict:
        return {f.fact_id: f for f in self.store.visible_facts(self.observation)}

    @staticmethod
    def _touches(span, start: float, end: float) -> bool:
        # 相接（端点重合）也算受影响：半开区间的并分量在端点处可能合并
        return any(start <= hi and lo <= end for lo, hi in span)

    def update_fact(self, fv: FactVersion) -> UpdateReport:
        before = self._visible_by_id()
        self.store.add_fact_version(fv)
        after = self._visible_by_id()

        old, new = before.get(fv.fact_id), after.get(fv.fact_id)
        if old == new:
            kept = tuple(s.segment_id for s in self._result.segments)
            return UpdateReport(fv.ref, (), False, (), kept, (), self._result)

        fresh = solve_store(self.store, self.observation, self.budget)

        def cliques_of(result: SolveResult):
            return [c for c in result.cliques if fv.fact_id in
                    {ref.split("@")[0] for ref in c[0]}]

        structural = (old is not None and new is not None
                      and old.source != new.source)
        bounded = all(c[3] for c in cliques_of(self._result) + cliques_of(fresh))
        full_refresh = structural or not bounded

        if full_refresh:
            intervals = ([(s.start, s.end) for s in self._result.segments]
                         + [(s.start, s.end) for s in fresh.segments])
            if old is not None:
                intervals.append((old.valid_from, old.valid_to))
            if new is not None:
                intervals.append((new.valid_from, new.valid_to))
            span = tuple(_merge_intervals(intervals))
        else:
            intervals = []
            if old is not None:
                intervals.append((old.valid_from, old.valid_to))
            if new is not None:
                intervals.append((new.valid_from, new.valid_to))
            span = tuple(_merge_intervals(intervals))

        revoked = tuple(s.segment_id for s in self._result.segments
                        if self._touches(span, s.start, s.end))
        kept_segs = [s for s in self._result.segments
                     if not self._touches(span, s.start, s.end)]
        new_in_span = [s for s in fresh.segments
                       if self._touches(span, s.start, s.end)]

        merged = tuple(sorted(kept_segs + new_in_span,
                              key=lambda s: (s.start, s.end, s.status)))
        result = SolveResult(
            observation_time=fresh.observation_time,
            segments=merged,
            reasons=fresh.reasons,
            truncated=fresh.truncated,
            evidence=fresh.evidence,
            trace=fresh.trace,
            cliques=fresh.cliques,
            solver_version=fresh.solver_version,
        )
        self._result = result
        return UpdateReport(
            fact_ref=fv.ref,
            span=span,
            full_refresh=full_refresh,
            revoked=revoked,
            kept=tuple(s.segment_id for s in kept_segs),
            added=tuple(s.segment_id for s in new_in_span),
            result=result,
        )
