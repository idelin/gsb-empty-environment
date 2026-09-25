"""Set algebra over schedules: union, intersection and difference.

Combination happens on the absolute time line (instants), so occurrences that
coincide after offset resolution are treated as one.  ``expand`` is still
lazy -- each child is expanded only over the requested window -- and jump
counts from children are summed so large queries remain auditable.
"""

from __future__ import annotations

from dataclasses import dataclass

from .rules import Event, Expansion, Schedule, Window

UNION = "union"
INTERSECTION = "intersection"
DIFFERENCE = "difference"

_OPS = {UNION, INTERSECTION, DIFFERENCE}


class CompositeSchedule:
    def __init__(self, op: str, children: list, *, name: str | None = None):
        if op not in _OPS:
            raise ValueError(f"unknown set operation: {op!r}")
        if not children:
            raise ValueError("a composite needs at least one child")
        if op == DIFFERENCE and len(children) < 2:
            raise ValueError("difference needs at least two operands")
        tables = {id(child.table) for child in children if hasattr(child, "table")}
        if len(tables) > 1:
            raise ValueError("all operands must share one offset table")
        self.op = op
        self.children = list(children)
        self.name = name or op
        self.table = children[0].table

    @property
    def fingerprint(self) -> str:
        from .rules import _canonical_hash

        return _canonical_hash([self.op, [c.fingerprint for c in self.children]])

    def expand(self, window: Window) -> Expansion:
        expanded = [child.expand(window) for child in self.children]
        jumps = sum(e.jumps for e in expanded)

        # Union with de-duplication.
        if self.op == UNION:
            merged: dict[int, Event] = {}
            for exp in expanded:
                for event in exp.events:
                    merged.setdefault(event.instant.epoch_us, event)
            events = list(merged.values())
            events.sort(key=lambda e: e.instant.epoch_us)
            return Expansion(events, jumps)

        first = {e.instant.epoch_us: e for e in expanded[0].events}

        if self.op == INTERSECTION:
            common = set(first)
            for exp in expanded[1:]:
                common &= {e.instant.epoch_us for e in exp.events}
            events = [first[k] for k in sorted(common)]
            events.sort(key=lambda e: e.instant.epoch_us)
            return Expansion(events, jumps)

        # Difference: first minus every later operand.
        removed: set[int] = set()
        for exp in expanded[1:]:
            removed.update(e.instant.epoch_us for e in exp.events)
        events = [first[k] for k in sorted(set(first) - removed)]
        events.sort(key=lambda e: e.instant.epoch_us)
        return Expansion(events, jumps)


def union(children, *, name=None) -> CompositeSchedule:
    return CompositeSchedule(UNION, children, name=name)


def intersection(children, *, name=None) -> CompositeSchedule:
    return CompositeSchedule(INTERSECTION, children, name=name)


def difference(left, *rights, name=None) -> CompositeSchedule:
    return CompositeSchedule(DIFFERENCE, [left, *rights], name=name)
