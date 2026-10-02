"""Diagnostics with per-position de-duplication.

Multiple errors must never produce repeated reports for the same input
position, so the sink drops any diagnostic whose token position was already
reported.
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Diagnostic:
    position: int  # token index in the parsed stream
    offset: int  # source offset of the offending token
    expected: tuple[str, ...]  # located expectation set (sorted)
    found: str
    kind: str  # 'missing-terminal' | 'unexpected-token'
    message: str


class DiagnosticSink:
    def __init__(self) -> None:
        self.items: list[Diagnostic] = []
        self._reported_positions: set[int] = set()

    def report(self, diagnostic: Diagnostic) -> bool:
        """Record ``diagnostic`` unless its position was already reported."""
        if diagnostic.position in self._reported_positions:
            return False
        self._reported_positions.add(diagnostic.position)
        self.items.append(diagnostic)
        return True
