from __future__ import annotations

from dataclasses import dataclass
from typing import Iterable, Mapping, Sequence

from block_pruning import SkipDecision, analyze_block
from predicate_tree import Block, Column, Predicate, TriBool, _eval, typecheck_predicate


RowId = tuple[int, int]


@dataclass(frozen=True)
class Batch:
    columns: tuple[Column, ...]
    rows: tuple[Mapping[str, object], ...]
    block: Block

    @classmethod
    def typed(
        cls, columns: Iterable[Column], rows: Iterable[Mapping[str, object]]
    ) -> "Batch":
        column_tuple = tuple(columns)
        row_tuple = tuple(rows)
        block = Block.from_batch(column_tuple, row_tuple)
        return cls(column_tuple, row_tuple, block)


@dataclass(frozen=True)
class RowEvaluation:
    row_id: RowId
    result: TriBool
    row: Mapping[str, object]


@dataclass(frozen=True)
class BlockScan:
    block_id: int
    skipped: bool
    decision: SkipDecision
    rows: tuple[RowEvaluation, ...]


@dataclass(frozen=True)
class AuditReport:
    ok: bool
    reference_selected: tuple[RowId, ...]
    pruned_selected: tuple[RowId, ...]
    unsafe_skips: tuple[int, ...]
    missing_rows: tuple[RowId, ...]
    extra_rows: tuple[RowId, ...]


def typed_batch(
    columns: Iterable[Column], rows: Iterable[Mapping[str, object]]
) -> Batch:
    return Batch.typed(columns, rows)


def scan_row_by_row(
    batches: Sequence[Batch], predicate: Predicate
) -> tuple[BlockScan, ...]:
    typecheck_predicate(predicate)
    scans: list[BlockScan] = []
    for block_id, batch in enumerate(batches):
        rows = tuple(
            RowEvaluation(
                (block_id, row_index),
                _eval(predicate, row),
                row,
            )
            for row_index, row in enumerate(batch.rows)
        )
        decision = analyze_block(predicate, batch.block)
        scans.append(BlockScan(block_id, False, decision, rows))
    return tuple(scans)


def scan_with_pruning(
    batches: Sequence[Batch], predicate: Predicate
) -> tuple[BlockScan, ...]:
    typecheck_predicate(predicate)
    scans: list[BlockScan] = []
    for block_id, batch in enumerate(batches):
        decision = analyze_block(predicate, batch.block)
        if decision.skip:
            scans.append(BlockScan(block_id, True, decision, ()))
            continue
        rows = tuple(
            RowEvaluation(
                (block_id, row_index),
                _eval(predicate, row),
                row,
            )
            for row_index, row in enumerate(batch.rows)
        )
        scans.append(BlockScan(block_id, False, decision, rows))
    return tuple(scans)


def selected_rows(scans: Sequence[BlockScan]) -> tuple[RowId, ...]:
    return tuple(
        row.row_id
        for scan in scans
        for row in scan.rows
        if row.result is TriBool.TRUE
    )


def audit_pruning(
    reference: Sequence[BlockScan], pruned: Sequence[BlockScan]
) -> AuditReport:
    reference_selected = selected_rows(reference)
    pruned_selected = selected_rows(pruned)
    reference_rows = {
        scan.block_id: scan.rows for scan in reference
    }
    unsafe_skips = tuple(
        scan.block_id
        for scan in pruned
        if scan.skipped
        and any(
            row.result is TriBool.TRUE
            for row in reference_rows[scan.block_id]
        )
    )
    reference_set = set(reference_selected)
    pruned_set = set(pruned_selected)
    missing = tuple(row_id for row_id in reference_selected if row_id not in pruned_set)
    extra = tuple(row_id for row_id in pruned_selected if row_id not in reference_set)
    return AuditReport(
        not unsafe_skips and not missing and not extra,
        reference_selected,
        pruned_selected,
        unsafe_skips,
        missing,
        extra,
    )
