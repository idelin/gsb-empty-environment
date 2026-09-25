"""Sweep-line solver for consistent temporal evidence selection.

A time point is feasible when there exists a complete selection (one
visible fact per required dimension) that is mutex-consistent and whose
common business interval covers that point. The solver sweeps atomic
segments between interval endpoints and merges adjacent segments to report all
maximal feasible segments, each with at least one witness selection.
Infeasible and unknown (budget-limited) ranges are reported explicitly.
"""
from __future__ import annotations


from dataclasses import dataclass
from itertools import product

from .journal import Journal
from .model import (
    Query,
    common_interval,
    conflicting_pairs,
    is_consistent,
    visible_versions,
)

FEASIBLE = "feasible"
INFEASIBLE = "infeasible"
UNKNOWN = "unknown"

NO_COVERAGE = "NO_COVERAGE"
NOT_VISIBLE = "NOT_VISIBLE"
MUTEX_CONFLICT = "MUTEX_CONFLICT"
EMPTY_INTERSECTION = "EMPTY_INTERSECTION"
SEARCH_INCOMPLETE = "SEARCH_INCOMPLETE"


def _fmt(value: float) -> str:
    if isinstance(value, float) and value.is_integer():
        return str(int(value))
    return repr(value)


@dataclass(frozen=True)
class Reason:
    code: str
    detail: str
    dimension: object = None
    fact_refs: tuple = ()


@dataclass(frozen=True)
class Selection:
    """A complete, mutex-consistent choice of one fact per dimension."""

    facts: tuple

    @property
    def refs(self) -> tuple:
        return tuple(sorted(f.ref for f in self.facts))

    def common_interval(self):
        return common_interval(self.facts)


@dataclass
class _Atomic:
    start: float
    end: float
    status: str
    selections: tuple = ()
    reasons: tuple = ()


@dataclass
class SegmentResult:
    segment_id: str
    start: float
    end: float
    status: str
    witnesses: tuple = ()
    reasons: tuple = ()
    covers_entire_segment: bool = False
    parts: tuple = ()


@dataclass
class SolveResult:
    query: Query
    segments: list
    journal: Journal

    @property
    def feasible_segments(self) -> list:
        return [s for s in self.segments if s.status == FEASIBLE]


def segment_signature(segments) -> list:
    signature = []
    for seg in segments:
        signature.append(
            {
                "segment_id": seg.segment_id,
                "status": seg.status,
                "witnesses": sorted(list(sel.refs) for sel in seg.witnesses),
                "reasons": sorted({r.code for r in seg.reasons}),
                "covers_entire_segment": seg.covers_entire_segment,
            }
        )
    return signature


def _atomic_bounds(visible, lo: float, hi: float) -> list:
    points = {lo, hi}
    for fact in visible:
        if lo < fact.valid_from < hi:
            points.add(fact.valid_from)
        if lo < fact.valid_to < hi:
            points.add(fact.valid_to)
    ordered = sorted(points)
    return list(zip(ordered, ordered[1:]))


def _evaluate(lo, hi, visible, all_facts, rules, query, max_combinations) -> _Atomic:
    per_dim = {}
    reasons = []
    for dim in query.dimensions:
        cands = [f for f in visible if f.dimension == dim and f.covers_range(lo, hi)]
        if cands:
            per_dim[dim] = cands
            continue
        late = sorted(
            {
                f.ref
                for f in all_facts
                if f.dimension == dim
                and f.recorded_at > query.observation_time
                and f.covers_range(lo, hi)
            }
        )
        if late:
            reasons.append(
                Reason(
                    NOT_VISIBLE,
                    f"dimension '{dim}' only has evidence recorded after "
                    f"observation time {query.observation_time}",
                    dim,
                    tuple(late),
                )
            )
        else:
            reasons.append(
                Reason(NO_COVERAGE, f"no visible evidence covers dimension '{dim}'", dim, ())
            )
    if reasons:
        return _Atomic(lo, hi, INFEASIBLE, (), tuple(reasons))
    total = 1
    for dim in query.dimensions:
        total *= len(per_dim[dim])
    if max_combinations is not None and total > max_combinations:
        reason = Reason(
            SEARCH_INCOMPLETE,
            f"combination budget exceeded: {total} candidates > {max_combinations}",
        )
        return _Atomic(lo, hi, UNKNOWN, (), (reason,))
    witnesses = []
    conflicts = set()
    for combo in product(*(per_dim[d] for d in query.dimensions)):
        if is_consistent(combo, rules):
            witnesses.append(Selection(tuple(sorted(combo, key=lambda f: f.dimension))))
        else:
            conflicts.update(conflicting_pairs(combo, rules))
    if not witnesses:
        detail = "; ".join(f"{a} x {b} ({ref})" for a, b, ref in sorted(conflicts))
        reason = Reason(MUTEX_CONFLICT, f"every combination violates mutex rules: {detail}")
        return _Atomic(lo, hi, INFEASIBLE, (), (reason,))
    return _Atomic(lo, hi, FEASIBLE, tuple(witnesses), ())


def _merge(parts) -> list:
    groups = []
    for part in parts:
        key = (part.status, tuple(sorted(r.code for r in part.reasons)))
        if groups and groups[-1][0] == key:
            groups[-1][1].append(part)
        else:
            groups.append((key, [part]))
    results = []
    for (status, _), group in groups:
        lo, hi = group[0].start, group[-1].end
        segment_id = f"seg[{_fmt(lo)}~{_fmt(hi)})#{status}"
        witnesses: tuple = ()
        reasons: tuple = ()
        covers = False
        if status == FEASIBLE:
            common = None
            for part in group:
                refs = {sel.refs for sel in part.selections}
                common = refs if common is None else (common & refs)
            whole = [sel for sel in group[0].selections if sel.refs in (common or set())]
            if whole:
                witnesses = tuple(sorted(whole, key=lambda s: s.refs))
                covers = True
            else:
                seen = set()
                collected = []
                for part in group:
                    for sel in part.selections:
                        if sel.refs not in seen:
                            seen.add(sel.refs)
                            collected.append(sel)
                witnesses = tuple(sorted(collected, key=lambda s: s.refs))
        else:
            seen_reasons = set()
            collected_reasons = []
            for part in group:
                for reason in part.reasons:
                    if reason not in seen_reasons:
                        seen_reasons.add(reason)
                        collected_reasons.append(reason)
            reasons = tuple(collected_reasons)
        results.append(
            SegmentResult(segment_id, lo, hi, status, witnesses, reasons, covers, tuple(group))
        )
    return results


def solve(
    facts,
    rules,
    query: Query,
    horizon=None,
    max_atomic_segments=None,
    max_combinations=None,
    journal: Journal = None,
) -> SolveResult:
    journal = journal if journal is not None else Journal()
    facts = list(facts)
    rules = list(rules)
    visible_map = visible_versions(facts, query.observation_time)
    visible = list(visible_map.values())
    late = sorted({f.ref for f in facts if f.recorded_at > query.observation_time})
    superseded = sorted(
        {
            f.ref
            for f in facts
            if f.recorded_at <= query.observation_time and visible_map.get(f.fact_id) is not f
        }
    )
    journal.record(
        "visibility",
        {
            "query_id": query.query_id,
            "observation_time": query.observation_time,
            "dimensions": list(query.dimensions),
            "visible": sorted(f.ref for f in visible),
            "excluded_late": late,
            "superseded": superseded,
            "rules": sorted(r.ref for r in rules),
        },
    )
    if horizon is None:
        if not visible:
            journal.record("segments", {"segments": []})
            return SolveResult(query, [], journal)
        lo = min(f.valid_from for f in visible)
        hi = max(f.valid_to for f in visible)
    else:
        lo, hi = horizon
    parts = []
    if lo < hi and visible:
        bounds = _atomic_bounds(visible, lo, hi)
        exhausted_at = None
        for index, (a, b) in enumerate(bounds):
            if max_atomic_segments is not None and index >= max_atomic_segments:
                exhausted_at = index
                break
            part = _evaluate(a, b, visible, facts, rules, query, max_combinations)
            parts.append(part)
            journal.record(
                "atomic",
                {
                    "start": a,
                    "end": b,
                    "status": part.status,
                    "reasons": [r.code for r in part.reasons],
                    "witnesses": [list(sel.refs) for sel in part.selections],
                },
            )
        if exhausted_at is not None:
            a = bounds[exhausted_at][0]
            reason = Reason(
                SEARCH_INCOMPLETE,
                f"atomic segment budget exhausted at {max_atomic_segments}; "
                f"range [{_fmt(a)}, {_fmt(hi)}) left unknown",
            )
            parts.append(_Atomic(a, hi, UNKNOWN, (), (reason,)))
            journal.record(
                "atomic",
                {"start": a, "end": hi, "status": UNKNOWN, "reasons": [SEARCH_INCOMPLETE], "witnesses": []},
            )
    segments = _merge(parts)
    journal.record("segments", {"segments": segment_signature(segments)})
    return SolveResult(query, segments, journal)
