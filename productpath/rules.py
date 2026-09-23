"""Versioned forbidden rules.

A rule forbids being at ``vertex`` in ``state`` when the arrival time
falls inside ``[start, end)`` (left-closed, right-open).  ``None``
means the interval is unbounded on that side.  Every mutation of a
:class:`RuleSet` bumps its version; cursors are bound to a version.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Optional


@dataclass(frozen=True)
class ForbiddenRule:
    vertex: str
    state: str
    start: Optional[float] = None  # inclusive
    end: Optional[float] = None    # exclusive

    def __post_init__(self) -> None:
        if self.start is not None and self.end is not None and self.start > self.end:
            raise ValueError("rule time interval is empty: start > end")

    def applies(self, vertex: str, state: str, time: float) -> bool:
        return (
            vertex == self.vertex
            and state == self.state
            and (self.start is None or time >= self.start)
            and (self.end is None or time < self.end)
        )


class RuleSet:
    def __init__(self) -> None:
        self._rules: list[ForbiddenRule] = []
        self._version = 0

    @property
    def version(self) -> int:
        return self._version

    @property
    def rules(self) -> tuple[ForbiddenRule, ...]:
        return tuple(self._rules)

    def add_rule(self, rule: ForbiddenRule) -> None:
        self._rules.append(rule)
        self._version += 1

    def remove_rule(self, rule: ForbiddenRule) -> None:
        self._rules.remove(rule)
        self._version += 1

    def clear(self) -> None:
        if self._rules:
            self._rules.clear()
            self._version += 1

    def is_forbidden(self, vertex: str, state: str, time: float) -> bool:
        return any(rule.applies(vertex, state, time) for rule in self._rules)
