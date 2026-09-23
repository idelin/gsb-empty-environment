"""Incremental two-table LEFT OUTER JOIN view driven by row changes.

State kept for each side is a primary-key -> row table plus a join-key
index.  Every primitives returns a signed changelog (``(sign, row)``)
describing how the materialised view moves:

* a new left row with no right match emits one +1 NULL-extended row;
* the FIRST right match to arrive retracts (-1) that NULL row and emits
  the joined row(s);
* the LAST right match to leave retracts the joined rows and re-emits
  the NULL-extended row;
* multiplicity is preserved by always emitting one delta per pair.
"""

from __future__ import annotations

from collections import defaultdict
from typing import Any, Iterable

from .model import Change

SignedRow = tuple[int, dict[str, Any]]


class LeftOuterJoinView:
    """Materialised ``left LEFT OUTER JOIN right ON join keys equal``."""

    def __init__(
        self,
        right_columns: Iterable[str],
        left_join_key: str = "k",
        right_join_key: str = "k",
    ) -> None:
        right_cols = tuple(right_columns)
        if right_join_key in right_cols:
            raise ValueError("right join key must not also be a payload column")
        self._lj = left_join_key
        self._rj = right_join_key
        self._right_cols = right_cols
        # pk -> row, plus join-key -> set of pks
        self._left: dict[tuple[Any, ...], dict[str, Any]] = {}
        self._left_by_key: dict[Any, set[tuple[Any, ...]]] = defaultdict(set)
        self._right: dict[tuple[Any, ...], dict[str, Any]] = {}
        self._right_by_key: dict[Any, set[tuple[Any, ...]]] = defaultdict(set)

    # -- public API ----------------------------------------------------

    def apply_change(self, change: Change) -> list[SignedRow]:
        """Apply one row change and return the view-level signed deltas."""
        if change.op == "insert":
            return self._insert(change.table, change.pk, dict(change.after))
        if change.op == "delete":
            return self._delete(change.table, change.pk, dict(change.before))
        # Normalise an update into delete-before / insert-after so that a
        # join-key migration does not need a separate code path.
        deltas = self._delete(change.table, change.pk, dict(change.before))
        deltas += self._insert(change.table, change.pk, dict(change.after))
        return deltas

    def snapshot(self) -> list[dict[str, Any]]:
        """Current materialised rows, deterministically ordered."""
        rows: list[dict[str, Any]] = []
        for lpk in sorted(self._left, key=_sort_key):
            lrow = self._left[lpk]
            matches = sorted(self._right_by_key.get(lrow[self._lj], ()), key=_sort_key)
            if matches:
                for rpk in matches:
                    rows.append(self._join_row(lrow, self._right[rpk]))
            else:
                rows.append(self._join_row(lrow, None))
        return rows

    # -- left side -----------------------------------------------------

    def _insert_left(self, pk: tuple[Any, ...], row: dict[str, Any]) -> list[SignedRow]:
        if pk in self._left:
            raise ValueError(f"duplicate left primary key: {pk!r}")
        key = row[self._lj]
        matches = sorted(self._right_by_key.get(key, ()), key=_sort_key)
        deltas: list[SignedRow] = []
        if matches:
            for rpk in matches:
                deltas.append((1, self._join_row(row, self._right[rpk])))
        else:
            deltas.append((1, self._join_row(row, None)))
        self._left[pk] = row
        self._left_by_key[key].add(pk)
        return deltas

    def _delete_left(self, pk: tuple[Any, ...], row: dict[str, Any]) -> list[SignedRow]:
        if pk not in self._left:
            raise ValueError(f"delete of unknown left primary key: {pk!r}")
        key = row[self._lj]
        matches = sorted(self._right_by_key.get(key, ()), key=_sort_key)
        deltas: list[SignedRow] = []
        if matches:
            for rpk in matches:
                deltas.append((-1, self._join_row(row, self._right[rpk])))
        else:
            deltas.append((-1, self._join_row(row, None)))
        self._left.pop(pk, None)
        self._left_by_key[key].discard(pk)
        return deltas

    # -- right side ----------------------------------------------------

    def _insert_right(self, pk: tuple[Any, ...], row: dict[str, Any]) -> list[SignedRow]:
        if pk in self._right:
            raise ValueError(f"duplicate right primary key: {pk!r}")
        key = row[self._rj]
        matches = sorted(self._left_by_key.get(key, ()), key=_sort_key)
        deltas: list[SignedRow] = []
        # FIRST match arriving on each left row: retract its NULL extension.
        for lpk in matches:
            if len(self._right_by_key.get(key, ())) == 0:
                deltas.append((-1, self._join_row(self._left[lpk], None)))
            deltas.append((1, self._join_row(self._left[lpk], row)))
        self._right[pk] = row
        self._right_by_key[key].add(pk)
        return deltas

    def _delete_right(self, pk: tuple[Any, ...], row: dict[str, Any]) -> list[SignedRow]:
        if pk not in self._right:
            raise ValueError(f"delete of unknown right primary key: {pk!r}")
        key = row[self._rj]
        peers = self._right_by_key.get(key, set())
        was_last = len(peers) == 1 and pk in peers
        matches = sorted(self._left_by_key.get(key, ()), key=_sort_key)
        deltas: list[SignedRow] = []
        for lpk in matches:
            deltas.append((-1, self._join_row(self._left[lpk], row)))
            # LAST match leaving: restore the NULL-extended row.
            if was_last:
                deltas.append((1, self._join_row(self._left[lpk], None)))
        self._right.pop(pk, None)
        peers.discard(pk)
        return deltas

    # -- helpers -------------------------------------------------------

    def _insert(self, table: str, pk: tuple[Any, ...], row: dict[str, Any]) -> list[SignedRow]:
        if table == "left":
            return self._insert_left(pk, row)
        return self._insert_right(pk, row)

    def _delete(self, table: str, pk: tuple[Any, ...], row: dict[str, Any]) -> list[SignedRow]:
        if table == "left":
            return self._delete_left(pk, row)
        return self._delete_right(pk, row)

    def _join_row(self, left_row: dict[str, Any], right_row: dict[str, Any] | None) -> dict[str, Any]:
        row = {f"l_{name}": value for name, value in left_row.items()}
        for name in self._right_cols:
            row[f"r_{name}"] = None if right_row is None else right_row.get(name)
        return row


def _sort_key(value: Any) -> Any:
    """Ordering key that tolerates mixed/None values."""
    return tuple((type(item).__name__, repr(item)) for item in value)
