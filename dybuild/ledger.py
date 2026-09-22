"""Record of what the kernel really executed (vs. cache hits)."""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Literal

Kind = Literal["executed", "cache_hit", "rejected_stale", "cycle_rejected", "rescheduled"]


@dataclass
class LedgerEntry:
    node: str
    kind: Kind
    detail: str = ""


@dataclass
class Ledger:
    entries: list[LedgerEntry] = field(default_factory=list)

    def record(self, node: str, kind: Kind, detail: str = "") -> None:
        self.entries.append(LedgerEntry(node, kind, detail))

    def nodes(self, kind: Kind | None = None) -> list[str]:
        return [e.node for e in self.entries if kind is None or e.kind == kind]

    def executed_nodes(self) -> list[str]:
        return self.nodes("executed")

    def reset(self) -> None:
        self.entries.clear()
