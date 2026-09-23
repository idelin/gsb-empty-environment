"""Change-data engine applying local fixtures to fold groups.

Changes arrive from local fixture files (no message platform involved).
Each change carries a transaction id, table, primary key, before/after
values and a commit sequence number. Transactions are applied atomically
and in strictly increasing commit-sequence order; a cross-group move is a
single update whose before/after values name different groups, applied as
one atomic unit together with the rest of its transaction.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Optional

from .group import FoldGroup
from .matrix import Matrix, from_rows, normalize

RowKey = tuple[str, str]  # (table, primary key)


class ConflictError(RuntimeError):
    """Raised when a change's before-image does not match current state.

    The offending transaction is rejected as a whole; no partial mutation
    is left behind.
    """


def _canon_value(value: dict[str, Any], mod: int) -> dict[str, Any]:
    """Canonical form of a row value for storage and before-image checks."""
    return {
        "group": str(value["group"]),
        "sort_key": value["sort_key"],
        "matrix": normalize(from_rows(value["matrix"]), mod),
    }


@dataclass(frozen=True)
class Change:
    table: str
    pk: str
    before: Optional[dict[str, Any]]  # None -> insert
    after: Optional[dict[str, Any]]   # None -> delete


@dataclass(frozen=True)
class Transaction:
    tx_id: str
    commit_seq: int
    changes: tuple[Change, ...] = field(default_factory=tuple)


class FoldEngine:
    """Applies transactions to per-group matrix folds under a modulus."""

    def __init__(self, mod: int):
        if mod < 1:
            raise ValueError("modulus must be a positive integer")
        self.mod = mod
        self._groups: dict[str, FoldGroup] = {}
        self._rows: dict[RowKey, dict[str, Any]] = {}
        self.last_commit_seq = 0

    def _group(self, name: str) -> FoldGroup:
        group = self._groups.get(name)
        if group is None:
            group = self._groups[name] = FoldGroup(self.mod)
        return group

    def group_names(self) -> list[str]:
        return sorted(self._groups)

    def product(self, group: str) -> Matrix:
        """Incrementally maintained product of ``group`` (identity if empty)."""
        existing = self._groups.get(group)
        return existing.product() if existing is not None else (1, 0, 0, 1)

    def full_scan_product(self, group: str) -> Matrix:
        """Reference product recomputed by a full ordered scan."""
        existing = self._groups.get(group)
        return existing.full_scan_product() if existing is not None else (1, 0, 0, 1)

    def apply_transaction(self, tx: Transaction) -> None:
        """Validate then atomically apply every change of ``tx``."""
        if tx.commit_seq <= self.last_commit_seq:
            raise ConflictError(
                f"commit_seq {tx.commit_seq} is not after {self.last_commit_seq}"
            )
        # Phase 1: validate all before-images against current state.
        prepared: list[tuple[RowKey, Optional[dict], Optional[dict]]] = []
        for change in tx.changes:
            key = (change.table, change.pk)
            before = (
                _canon_value(change.before, self.mod)
                if change.before is not None
                else None
            )
            after = (
                _canon_value(change.after, self.mod)
                if change.after is not None
                else None
            )
            if before is None and after is None:
                raise ConflictError(f"{tx.tx_id}: change for {key} is a no-op")
            if self._rows.get(key) != before:
                raise ConflictError(
                    f"{tx.tx_id}: before-image mismatch for {key}: "
                    f"expected {before!r}, found {self._rows.get(key)!r}"
                )
            prepared.append((key, before, after))
        # Phase 2: apply. After validation none of these operations can
        # fail, so the transaction (including cross-group moves) is atomic.
        for key, before, after in prepared:
            if (
                before is not None
                and after is not None
                and before["group"] == after["group"]
            ):
                # Same-group update: O(log n) point update on the tree.
                self._group(after["group"]).update(
                    key, after["sort_key"], after["matrix"]
                )
            else:
                # Insert, delete, or atomic cross-group move.
                if before is not None:
                    self._group(before["group"]).delete(key)
                if after is not None:
                    self._group(after["group"]).insert(
                        key, after["sort_key"], after["matrix"]
                    )
            if after is None:
                del self._rows[key]
            else:
                self._rows[key] = after
        self.last_commit_seq = tx.commit_seq
