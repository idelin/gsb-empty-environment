"""Per-data-version epsilon ledger with sequential (pure) composition."""

from __future__ import annotations

import threading
from dataclasses import dataclass
from typing import Dict, List


class BudgetExceededError(RuntimeError):
    """Raised when a fresh mechanism run would overspend the epsilon budget."""


@dataclass(frozen=True)
class LedgerEntry:
    query_id: str
    epsilon: float
    cumulative_spent: float
    kind: str  # "charge" (new publication) or "reuse" (free cache hit)
    note: str


class BudgetLedger:
    def __init__(self, version: str, total_epsilon: float) -> None:
        if total_epsilon <= 0:
            raise ValueError("total_epsilon must be positive")
        self.version = version
        self.total_epsilon = float(total_epsilon)
        self.spent = 0.0
        self.entries: List[LedgerEntry] = []
        self._lock = threading.Lock()

    @property
    def remaining(self) -> float:
        return self.total_epsilon - self.spent

    def check_charge(self, epsilon: float) -> None:
        with self._lock:
            if self.spent + epsilon > self.total_epsilon + 1e-12:
                raise BudgetExceededError(
                    f"version {self.version[:12]}: need {epsilon}, "
                    f"spent {self.spent:.6g}, budget {self.total_epsilon:.6g}"
                )

    def charge(self, query_id: str, epsilon: float, note: str = "new publication") -> None:
        with self._lock:
            if self.spent + epsilon > self.total_epsilon + 1e-12:
                raise BudgetExceededError(
                    f"version {self.version[:12]}: need {epsilon}, "
                    f"spent {self.spent:.6g}, budget {self.total_epsilon:.6g}"
                )
            self.spent += epsilon
            self.entries.append(
                LedgerEntry(query_id, epsilon, self.spent, "charge", note)
            )

    def note_reuse(self, query_id: str, note: str = "cache hit") -> None:
        with self._lock:
            self.entries.append(
                LedgerEntry(query_id, 0.0, self.spent, "reuse", note)
            )

    def report(self) -> str:
        lines = [
            f"ledger version={self.version[:12]} budget={self.total_epsilon:g} "
            f"spent={self.spent:g} remaining={self.remaining:g} charges="
            f"{sum(1 for e in self.entries if e.kind == 'charge')} "
            f"reuses={sum(1 for e in self.entries if e.kind == 'reuse')}"
        ]
        for entry in self.entries:
            lines.append(
                f"  [{entry.kind:6}] eps={entry.epsilon:g} "
                f"cum={entry.cumulative_spent:g} id={entry.query_id[:48]} ({entry.note})"
            )
        return "\n".join(lines)
