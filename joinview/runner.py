"""Group fixture changes into transactions and publish on commit.

Changes are grouped by ``txn_id`` and replayed in ``commit_seq`` order.
All left/right movements inside one transaction are summed, so the
published changelog only shows the net, final-consistent result; rows
that end unchanged (signs cancel) are never published.
"""

from __future__ import annotations

from collections import Counter, OrderedDict
from dataclasses import dataclass
from typing import Any, Callable, Iterable

from .engine import LeftOuterJoinView
from .model import Change

Listener = Callable[["Commit"], None]


def freeze(row: dict[str, Any]) -> tuple[tuple[str, str, Any], ...]:
    """Hashable, deterministic representation of an output row."""
    return tuple((name, type(value).__name__, value) for name, value in sorted(row.items()))


@dataclass(frozen=True, slots=True)
class SignedRow:
    sign: int  # +1 addition, -1 retraction
    row: dict[str, Any]


@dataclass(frozen=True, slots=True)
class Commit:
    """Net changelog published when one transaction commits."""

    txn_id: str
    commit_seq: int
    changes: tuple[SignedRow, ...]

    @property
    def additions(self) -> tuple[SignedRow, ...]:
        return tuple(item for item in self.changes if item.sign > 0)

    @property
    def retractions(self) -> tuple[SignedRow, ...]:
        return tuple(item for item in self.changes if item.sign < 0)


class JoinChangelog:
    """Applies transactions to the view and publishes net commits."""

    def __init__(self, view: LeftOuterJoinView) -> None:
        self._view = view
        self._listeners: list[Listener] = []
        self.published: list[Commit] = []

    def subscribe(self, listener: Listener) -> None:
        self._listeners.append(listener)

    def ingest_all(self, changes: Iterable[Change]) -> list[Commit]:
        by_txn: dict[str, list[Change]] = OrderedDict()
        for change in changes:
            by_txn.setdefault(change.txn_id, []).append(change)

        commits: list[tuple[int, str, list[Change]]] = []
        for txn_id, items in by_txn.items():
            seqs = {item.commit_seq for item in items}
            if len(seqs) != 1:
                raise ValueError(
                    f"transaction {txn_id!r} mixes commit_seq values: {sorted(seqs)}"
                )
            commits.append((items[0].commit_seq, txn_id, items))
        seen_seq: set[int] = set()
        for seq, _, _ in commits:
            if seq in seen_seq:
                raise ValueError(f"duplicate commit_seq: {seq}")
            seen_seq.add(seq)
        commits.sort(key=lambda item: (item[0], item[1]))

        for _, txn_id, items in commits:
            self._commit(txn_id, items)
        return list(self.published)

    def _commit(self, txn_id: str, items: list[Change]) -> Commit:
        totals: Counter[tuple[Any, ...]] = Counter()
        seq = items[0].commit_seq
        for change in items:
            for sign, row in self._view.apply_change(change):
                totals[freeze(row)] += sign
        published = self._expand(totals)
        commit = Commit(txn_id=txn_id, commit_seq=seq, changes=tuple(published))
        self.published.append(commit)
        for listener in self._listeners:
            listener(commit)
        return commit

    @staticmethod
    def _expand(totals: Counter[tuple[Any, ...]]) -> list[SignedRow]:
        ordered = sorted(
            totals.items(),
            # retractions before additions, then deterministic row ordering
            key=lambda item: (
                0 if item[1] < 0 else 1,
                tuple((name, type_name, repr(value)) for name, type_name, value in item[0]),
            ),
        )
        out: list[SignedRow] = []
        for key, total in ordered:
            if total == 0:
                continue  # net-zero churn inside the transaction is suppressed
            sign = 1 if total > 0 else -1
            row = {name: value for name, _, value in key}
            out.extend(SignedRow(sign, dict(row)) for _ in range(abs(total)))
        return out

    def snapshot(self) -> list[dict[str, Any]]:
        return self._view.snapshot()


def build_processor(
    right_columns: Iterable[str],
    left_join_key: str = "k",
    right_join_key: str = "k",
) -> tuple[LeftOuterJoinView, JoinChangelog]:
    view = LeftOuterJoinView(right_columns, left_join_key, right_join_key)
    return view, JoinChangelog(view)
