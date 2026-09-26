"""Composition of rules: union / intersection / difference, exception
dates, extra dates, lazy windowed expansion and cursor-based pagination.

Semantics (all windows are half-open ``[start, end)``):

- include rules are unioned (heapq merge, duplicates removed);
- a candidate survives only if *every* intersect rule contains it and
  *no* exclude rule contains it;
- ``exdates`` remove occurrences; ``rdates`` add them;
- conflict policy: an exdate beats an rdate at the same wall time
  (exclusions always win, so they also affect count and rank);
- output is deduplicated by wall time.

Pagination cursors are bound to the ruleset fingerprint and the offset
table version: resuming with a stale cursor raises ``ValueError``.
"""

from __future__ import annotations

import base64
import hashlib
import heapq
import json
from dataclasses import dataclass
from typing import Iterator, Optional

from .offsettable import FoldPolicy, GapPolicy, OffsetTable
from .rules import Rule
from .timeprims import Instant, WallClock


@dataclass(frozen=True)
class Cursor:
    rule_fingerprint: str
    table_version: str
    last_epoch: int

    def encode(self) -> str:
        payload = json.dumps(
            {
                "fp": self.rule_fingerprint,
                "tv": self.table_version,
                "last": self.last_epoch,
            }
        ).encode()
        return base64.urlsafe_b64encode(payload).decode()

    @classmethod
    def decode(cls, token: str) -> "Cursor":
        data = json.loads(base64.urlsafe_b64decode(token.encode()))
        return cls(data["fp"], data["tv"], data["last"])


class RuleSet:
    def __init__(self, table: OffsetTable):
        self.table = table
        self.include_rules: list[Rule] = []
        self.intersect_rules: list[Rule] = []
        self.exclude_rules: list[Rule] = []
        self.exdates: set[WallClock] = set()
        self.rdates: set[WallClock] = set()

    # -- construction -------------------------------------------------------
    def add_rule(self, rule: Rule) -> "RuleSet":
        self.include_rules.append(rule)
        return self

    def add_intersect(self, rule: Rule) -> "RuleSet":
        self.intersect_rules.append(rule)
        return self

    def add_exclude_rule(self, rule: Rule) -> "RuleSet":
        self.exclude_rules.append(rule)
        return self

    def add_exdate(self, wall: WallClock) -> "RuleSet":
        self.exdates.add(wall)
        return self

    def add_rdate(self, wall: WallClock) -> "RuleSet":
        self.rdates.add(wall)
        return self

    # -- identity ------------------------------------------------------------
    def fingerprint(self) -> str:
        blob = repr(
            (
                [r.spec() for r in self.include_rules],
                [r.spec() for r in self.intersect_rules],
                [r.spec() for r in self.exclude_rules],
                sorted(w.seconds for w in self.exdates),
                sorted(w.seconds for w in self.rdates),
            )
        ).encode()
        return hashlib.sha256(blob).hexdigest()[:16]

    # -- expansion -----------------------------------------------------------
    def _rule_candidates(self, start: WallClock, end: WallClock) -> Iterator[WallClock]:
        streams = [r.iter_wall(start, end) for r in self.include_rules]
        for wall in heapq.merge(*streams):
            if not all(r.contains(wall) for r in self.intersect_rules):
                continue
            if any(r.contains(wall) for r in self.exclude_rules):
                continue
            yield wall

    def iter_wall(self, start: WallClock, end: WallClock) -> Iterator[WallClock]:
        if not self.include_rules and not self.rdates:
            return
        extra = sorted(
            w for w in self.rdates if start <= w < end and w not in self.exdates
        )
        prev: Optional[WallClock] = None
        for wall in heapq.merge(self._rule_candidates(start, end), extra):
            if wall == prev:  # dedupe (e.g. rdate equal to a rule occurrence)
                continue
            prev = wall
            if wall in self.exdates:
                continue
            yield wall

    def iter_instants(
        self,
        start: WallClock,
        end: WallClock,
        gap_policy: GapPolicy = GapPolicy.SHIFT_FORWARD,
        fold_policy: FoldPolicy = FoldPolicy.EARLIER,
    ) -> Iterator[tuple[WallClock, Instant]]:
        for wall in self.iter_wall(start, end):
            yield wall, self.table.to_instant(wall, gap_policy, fold_policy)

    # -- counting / ranking ----------------------------------------------------
    def count_between(self, start: WallClock, end: WallClock) -> int:
        return sum(1 for _ in self.iter_wall(start, end))

    def kth(self, k: int, start: WallClock) -> WallClock:
        """k-th occurrence (0-based) at or after ``start``."""
        if k < 0:
            raise IndexError("k must be >= 0")
        lo = start.seconds
        hi = lo + 86400
        while self.count_between(start, WallClock.from_seconds(hi)) <= k:
            hi = lo + (hi - lo) * 2
        while lo + 1 < hi:
            mid = (lo + hi) // 2
            if self.count_between(start, WallClock.from_seconds(mid)) > k:
                hi = mid
            else:
                lo = mid
        items = list(self.iter_wall(start, WallClock.from_seconds(hi)))
        return items[k]

    # -- pagination --------------------------------------------------------------
    def page(
        self,
        start: WallClock,
        end: WallClock,
        limit: int,
        cursor: Optional[Cursor] = None,
        gap_policy: GapPolicy = GapPolicy.SHIFT_FORWARD,
        fold_policy: FoldPolicy = FoldPolicy.EARLIER,
    ) -> tuple[list[tuple[WallClock, Instant]], Optional[Cursor]]:
        if limit < 1:
            raise ValueError("limit must be >= 1")
        if cursor is not None:
            if cursor.rule_fingerprint != self.fingerprint():
                raise ValueError("cursor belongs to a different ruleset")
            if cursor.table_version != self.table.version:
                raise ValueError("cursor belongs to a different table version")
        out: list[tuple[WallClock, Instant]] = []
        stream = self.iter_instants(start, end, gap_policy, fold_policy)
        exhausted = True
        for wall, instant in stream:
            if cursor is not None and instant.epoch_seconds <= cursor.last_epoch:
                continue
            out.append((wall, instant))
            if len(out) > limit:
                exhausted = False
                out.pop()
                break
        next_cursor = None
        if not exhausted:
            next_cursor = Cursor(
                self.fingerprint(), self.table.version, out[-1][1].epoch_seconds
            )
        return out, next_cursor
