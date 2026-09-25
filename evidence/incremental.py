"""Incremental engine: undo and recompute only segments affected by an update."""
from __future__ import annotations

from dataclasses import dataclass

from .journal import Journal
from .model import visible_versions
from .solver import (
    SEARCH_INCOMPLETE,
    UNKNOWN,
    Reason,
    SolveResult,
    _Atomic,
    _atomic_bounds,
    _evaluate,
    _fmt,
    _merge,
    segment_signature,
    solve,
)


@dataclass
class UpdateReport:
    fact_ref: str
    changed: bool
    affected_range: object = None
    undone_segment_ids: tuple = ()
    new_segment_ids: tuple = ()


class IncrementalEngine:
    """Applies fact-version updates incrementally.

    Only segments overlapping the affected time range are undone and
    recomputed; the remaining atomic parts are kept and re-merged.
    """

    def __init__(self, rules, query, max_atomic_segments=None, max_combinations=None):
        self.rules = list(rules)
        self.query = query
        self.max_atomic_segments = max_atomic_segments
        self.max_combinations = max_combinations
        self.journal = Journal()
        self._facts: list = []
        self._initial_facts: list = []
        self._updates: list = []
        self.result: SolveResult | None = None

    def load(self, facts) -> SolveResult:
        self._facts = list(facts)
        self._initial_facts = list(facts)
        self.result = solve(
            self._facts,
            self.rules,
            self.query,
            max_atomic_segments=self.max_atomic_segments,
            max_combinations=self.max_combinations,
            journal=self.journal,
        )
        return self.result

    def apply_update(self, new_version) -> UpdateReport:
        before = visible_versions(self._facts, self.query.observation_time)
        old = before.get(new_version.fact_id)
        self._facts.append(new_version)
        self._updates.append(new_version)
        after = visible_versions(self._facts, self.query.observation_time)
        new = after.get(new_version.fact_id)
        if old == new or new is None:
            self.journal.record("update", {"fact": new_version.ref, "changed": False})
            return UpdateReport(new_version.ref, False)
        lo = min(x.valid_from for x in (old, new) if x is not None)
        hi = max(x.valid_to for x in (old, new) if x is not None)
        visible = list(after.values())
        old_parts = [part for seg in self.result.segments for part in seg.parts]
        undone_parts = [p for p in old_parts if p.start < hi and lo < p.end]
        if undone_parts:
            lo = min(lo, min(p.start for p in undone_parts))
            hi = max(hi, max(p.end for p in undone_parts))
        fresh_parts = []
        bounds = _atomic_bounds(visible, lo, hi)
        for index, (a, b) in enumerate(bounds):
            if self.max_atomic_segments is not None and index >= self.max_atomic_segments:
                reason = Reason(
                    SEARCH_INCOMPLETE,
                    f"atomic segment budget exhausted at {self.max_atomic_segments}; "
                    f"range [{_fmt(a)}, {_fmt(hi)}) left unknown",
                )
                fresh_parts.append(_Atomic(a, hi, UNKNOWN, (), (reason,)))
                break
            fresh_parts.append(
                _evaluate(a, b, visible, self._facts, self.rules, self.query, self.max_combinations)
            )
        kept = [p for p in old_parts if p.end <= lo or p.start >= hi]
        all_parts = kept + fresh_parts
        if visible:
            horizon_lo = min(f.valid_from for f in visible)
            horizon_hi = max(f.valid_to for f in visible)
            all_parts = [
                p for p in all_parts if p.start >= horizon_lo and p.end <= horizon_hi
            ]
        else:
            all_parts = []
        undone = tuple(
            seg.segment_id for seg in self.result.segments if seg.start < hi and lo < seg.end
        )
        merged = _merge(sorted(all_parts, key=lambda p: (p.start, p.end)))
        self.result = SolveResult(self.query, merged, self.journal)
        new_ids = tuple(s.segment_id for s in merged if s.start < hi and lo < s.end)
        self.journal.record(
            "update",
            {
                "fact": new_version.ref,
                "changed": True,
                "affected_range": [lo, hi],
                "undone_segment_ids": list(undone),
                "segments": segment_signature(merged),
            },
        )
        return UpdateReport(new_version.ref, True, (lo, hi), undone, new_ids)

    def replay(self) -> bool:
        """Re-run the recorded load and updates; results must be identical."""
        clone = IncrementalEngine(
            self.rules, self.query, self.max_atomic_segments, self.max_combinations
        )
        clone.load(self._initial_facts)
        for update in self._updates:
            clone.apply_update(update)
        return (
            segment_signature(clone.result.segments) == segment_signature(self.result.segments)
            and clone.journal.digest() == self.journal.digest()
        )
