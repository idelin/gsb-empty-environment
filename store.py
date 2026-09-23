"""Group store: applies CDC-style change events and maintains one ordered
matrix fold per group.

Events carry a transaction id, table, primary key, before/after row images
and a commit sequence number. A row image holds the group, the stable sort
key and the 2x2 matrix. An event whose before/after images name different
groups is an atomic cross-group migration: the event is validated in full
before anything is applied, so a group pair is never left half-updated.
"""

from __future__ import annotations

from dataclasses import dataclass

from matrix import IDENTITY, Matrix, fold, normalize
from treap import Treap


@dataclass
class ChangeEvent:
    tx_id: str
    table: str
    pk: str
    commit_seq: int
    before: dict | None
    after: dict | None

    def kind(self) -> str:
        if self.before is None:
            return "insert"
        if self.after is None:
            return "delete"
        if self.before["group"] != self.after["group"]:
            return "migrate"
        return "update"


class StoreError(Exception):
    pass


class GroupStore:
    def __init__(self, modulus: int):
        if modulus < 2:
            raise ValueError("modulus must be >= 2")
        self.modulus = modulus
        self._trees: dict[str, Treap] = {}
        self._items: dict[tuple[str, str], tuple] = {}  # (group, pk) -> (sort_key, matrix)
        self._pk_index: dict[str, str] = {}  # pk -> group (pk unique per table)

    def _tree(self, group: str) -> Treap:
        return self._trees.setdefault(group, Treap(self.modulus))

    def apply(self, event: ChangeEvent) -> None:
        before, after = event.before, event.after
        if before is None and after is None:
            raise StoreError(f"{event.tx_id}: event has neither before nor after image")

        # Validate everything first so the change applies atomically.
        if before is not None:
            old_key = (before["group"], event.pk)
            if old_key not in self._items:
                raise StoreError(
                    f"{event.tx_id}: row {event.pk!r} not found in group {before['group']!r}"
                )
        if after is not None:
            if event.pk in self._pk_index and before is None:
                raise StoreError(
                    f"{event.tx_id}: pk {event.pk!r} already exists in group "
                    f"{self._pk_index[event.pk]!r}; a before image is required"
                )

        if before is not None:
            group = before["group"]
            sort_key, _ = self._items.pop((group, event.pk))
            removed = self._trees[group].delete((sort_key, event.pk))
            assert removed, "index and tree out of sync"
            del self._pk_index[event.pk]
        if after is not None:
            group = after["group"]
            matrix = normalize(after["matrix"], self.modulus)
            sort_key = after["sort_key"]
            self._tree(group).insert((sort_key, event.pk), matrix)
            self._items[(group, event.pk)] = (sort_key, matrix)
            self._pk_index[event.pk] = group

    def product(self, group: str) -> Matrix:
        """Incremental fold from the tree; identity for an empty group."""
        tree = self._trees.get(group)
        if tree is None:
            return IDENTITY
        return tree.aggregate()

    def full_scan_product(self, group: str) -> Matrix:
        """Ground truth: sort every row of the group and fold left to right."""
        entries = sorted(
            (sort_key, pk, matrix)
            for (g, pk), (sort_key, matrix) in self._items.items()
            if g == group
        )
        return fold([matrix for _, _, matrix in entries], self.modulus)

    def groups(self) -> list[str]:
        return sorted({g for g, _ in self._items} | set(self._trees))
