"""Execution engine: block pruning decision + row-by-row scan.

A block is skipped ONLY when block-level evaluation proves FALSE (no row can
evaluate to TRUE). The independent row interpreter lets every decision be
cross-checked: if we skip, the interpreter must find zero matching rows.
"""

from __future__ import annotations

import dataclasses
from typing import Any

from .batch import Batch
from .predicate import Predicate
from .tvl import TVL


@dataclasses.dataclass(frozen=True)
class PruneDecision:
    skipped: bool
    reason: str | None
    verdict: TVL


@dataclasses.dataclass(frozen=True)
class QueryResult:
    decision: PruneDecision
    matched_rows: tuple[dict[str, Any], ...]
    matched_indexes: tuple[int, ...]
    # Full per-row verdicts for comparison/debugging.
    row_verdicts: tuple[TVL, ...]

    def explain(self) -> str:
        head = (
            f"SKIPPED: {self.decision.reason}"
            if self.decision.skipped
            else "SCANNED: block could not be proven empty"
        )
        lines = [
            head,
            f"block verdict: {self.decision.verdict.name}, "
            f"matched rows: {len(self.matched_rows)}",
        ]
        if not self.decision.skipped:
            for i, v in enumerate(self.row_verdicts):
                lines.append(f"  row {i}: {v.name}")
        return "\n".join(lines)


def prune_decision(batch: Batch, predicate: Predicate) -> PruneDecision:
    predicate.bind(batch.schema)
    stats = {name: batch.stats(name) for name in batch.schema.names}
    verdict = predicate.eval_block(stats)
    if verdict.value is TVL.FALSE:
        return PruneDecision(True, verdict.why, TVL.FALSE)
    return PruneDecision(False, None, verdict.value)


def row_scan(batch: Batch, predicate: Predicate) -> tuple[tuple[int, ...], tuple[dict[str, Any], ...], tuple[TVL, ...]]:
    """Independent row-by-row three-valued interpreter."""
    predicate.bind(batch.schema)
    indexes: list[int] = []
    verdicts: list[TVL] = []
    for i, row in enumerate(batch.rows()):
        v = predicate.eval_row(row)
        verdicts.append(v)
        if v is TVL.TRUE:  # FALSE and UNKNOWN are both excluded by WHERE
            indexes.append(i)
    rows = tuple(
        {name: batch.columns[name][i] for name in batch.schema.names}
        for i in indexes
    )
    return tuple(indexes), rows, tuple(verdicts)


def select(batch: Batch, predicate: Predicate) -> QueryResult:
    decision = prune_decision(batch, predicate)
    if decision.skipped:
        verdicts = tuple(TVL.UNKNOWN for _ in range(batch.num_rows))
        return QueryResult(decision, (), (), verdicts)
    indexes, rows, verdicts = row_scan(batch, predicate)
    return QueryResult(decision, rows, indexes, verdicts)


def cross_check(batch: Batch, predicate: Predicate) -> QueryResult:
    """Run pruning AND the row interpreter; assert the skip is sound."""
    decision = prune_decision(batch, predicate)
    indexes, rows, verdicts = row_scan(batch, predicate)
    if decision.skipped and any(v is TVL.TRUE for v in verdicts):
        raise AssertionError(
            f"UNSOUND PRUNE: skipped block but rows {indexes} evaluate TRUE; "
            f"reason was: {decision.reason}"
        )
    if not decision.skipped:
        return QueryResult(decision, rows, indexes, verdicts)
    return QueryResult(decision, (), (), verdicts)
