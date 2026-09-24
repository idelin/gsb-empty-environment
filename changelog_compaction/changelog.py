"""Local change-log model, replay, identity and incremental aggregation."""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any, Iterable

ABSENT = None


def is_absent(value: Any) -> bool:
    return value is ABSENT


def op_of(before: Any, after: Any) -> str:
    if is_absent(before) and not is_absent(after):
        return "INSERT"
    if not is_absent(before) and is_absent(after):
        return "DELETE"
    if is_absent(before) and is_absent(after):
        raise ValueError("absent -> absent is not a legal change")
    return "UPDATE"


@dataclass(frozen=True)
class Change:
    idx: int
    txn_id: str
    table: str
    key: Any
    before: Any
    after: Any
    commit_seq: int

    @property
    def op(self) -> str:
        return op_of(self.before, self.after)


def load_changes(rows: Iterable[dict]) -> list[Change]:
    changes: list[Change] = []
    for i, row in enumerate(rows):
        idx = int(row.get("idx", i))
        before = row["before"]
        after = row["after"]
        op_of(before, after)
        changes.append(
            Change(
                idx=idx,
                txn_id=str(row["txn_id"]),
                table=str(row["table"]),
                key=row["key"],
                before=before,
                after=after,
                commit_seq=int(row["commit_seq"]),
            )
        )
    return changes


def dump_change(change: Change) -> dict:
    return {
        "idx": change.idx,
        "txn_id": change.txn_id,
        "table": change.table,
        "key": change.key,
        "before": change.before,
        "after": change.after,
        "commit_seq": change.commit_seq,
        "op": change.op,
    }


@dataclass
class Replayer:
    """Applies an ordered change feed commit by commit.

    Before-values are validated against the materialised state on every
    application.  Delete/recreate semantics are preserved with an explicit
    identity generation counter per (table, key); a row that is deleted and
    re-created inside one commit receives a fresh identity, while a pure
    update keeps the old one.
    """

    values: dict[tuple, Any] = field(default_factory=dict)
    gens: dict[tuple, int] = field(default_factory=dict)
    next_gen: dict[tuple, int] = field(default_factory=dict)

    # Incremental aggregates maintained as deltas are fed in.
    counts: dict[str, int] = field(default_factory=dict)
    sums: dict[str, float] = field(default_factory=dict)
    inserts: dict[str, int] = field(default_factory=dict)
    deletes: dict[str, int] = field(default_factory=dict)
    updates: dict[str, int] = field(default_factory=dict)

    def _identity(self, table: str, key: Any, gen: int) -> str:
        return f"{table}:{json.dumps(key, sort_keys=True, ensure_ascii=False)}#g{gen}"

    def _add(self, bucket: dict[str, float], table: str, delta: float) -> None:
        if delta:
            bucket[table] = bucket.get(table, 0) + delta

    def _numeric(self, value: Any) -> float | None:
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            return None
        return float(value)

    def apply_commit(self, commit_changes: Iterable[Change]) -> None:
        # Validate every before-value incrementally, but nothing is visible to
        # downstream aggregates until the commit is sealed.
        order: list[tuple] = []
        chains: dict[tuple, dict] = {}
        cursor: dict[tuple, Any] = {}
        for change in commit_changes:
            entity = (change.table, change.key)
            if entity not in chains:
                started = self.values.get(entity, ABSENT)
                chains[entity] = {
                    "started": started,
                    "after": started,
                    "saw_absent": is_absent(started),
                }
                cursor[entity] = started
                order.append(entity)
            if cursor[entity] != change.before:
                current = self.values.get(entity, ABSENT)
                raise ValueError(
                    f"before-value mismatch for {change.table}/{change.key!r} "
                    f"at idx {change.idx}: log={change.before!r} "
                    f"state={current!r}"
                )
            cursor[entity] = change.after
            chains[entity]["after"] = change.after
            if is_absent(change.after):
                chains[entity]["saw_absent"] = True

        for entity in order:
            table, _key = entity
            chain = chains[entity]
            before = chain["started"]
            after = chain["after"]
            if is_absent(before) and not is_absent(after):
                self._seal_insert(entity, table, after)
            elif not is_absent(before) and is_absent(after):
                self._seal_delete(entity, table, before)
            elif not is_absent(before) and not is_absent(after):
                if chain["saw_absent"]:
                    # Committed delete/recreate: the new row is a new identity.
                    self._seal_delete(entity, table, before)
                    self._seal_insert(entity, table, after)
                else:
                    self._seal_update(entity, table, before, after)

    def _seal_insert(self, entity: tuple, table: str, after: Any) -> None:
        gen = self.next_gen.get(entity, 1)
        self.next_gen[entity] = gen + 1
        self.gens[entity] = gen
        self.values[entity] = after
        self.counts[table] = self.counts.get(table, 0) + 1
        self.inserts[table] = self.inserts.get(table, 0) + 1
        numeric = self._numeric(after)
        if numeric is not None:
            self._add(self.sums, table, numeric)

    def _seal_delete(self, entity: tuple, table: str, before: Any) -> None:
        self.values.pop(entity, None)
        self.gens.pop(entity, None)
        self.counts[table] = self.counts.get(table, 0) - 1
        self.deletes[table] = self.deletes.get(table, 0) + 1
        numeric = self._numeric(before)
        if numeric is not None:
            self._add(self.sums, table, -numeric)

    def _seal_update(
        self, entity: tuple, table: str, before: Any, after: Any
    ) -> None:
        self.updates[table] = self.updates.get(table, 0) + 1
        old_num = self._numeric(before)
        new_num = self._numeric(after)
        if old_num is not None or new_num is not None:
            self._add(
                self.sums,
                table,
                (new_num or 0.0) - (old_num or 0.0),
            )
        self.values[entity] = after

    def state(self) -> dict[str, dict[str, Any]]:
        out: dict[str, dict[str, Any]] = {}
        for (table, key), value in self.values.items():
            gen = self.gens[(table, key)]
            out.setdefault(table, {})[json.dumps(key, sort_keys=True)] = {
                "key": key,
                "value": value,
                "identity": self._identity(table, key, gen),
                "gen": gen,
            }
        return out

    def aggregate(self) -> dict[str, dict[str, float]]:
        tables = set(self.counts) | set(self.sums)
        out: dict[str, dict[str, float]] = {}
        for table in sorted(tables):
            out[table] = {
                "count": self.counts.get(table, 0),
                "sum": self.sums.get(table, 0),
                "inserts": self.inserts.get(table, 0),
                "deletes": self.deletes.get(table, 0),
                "updates": self.updates.get(table, 0),
            }
        return out


def replay(changes: Iterable[Change], commit_points: Iterable[int]) -> dict[int, dict]:
    by_commit: dict[int, list[Change]] = {}
    for change in changes:
        by_commit.setdefault(change.commit_seq, []).append(change)
    replayer = Replayer()
    snapshots: dict[int, dict] = {}
    for seq in sorted(commit_points):
        commit_changes = sorted(by_commit.get(seq, []), key=lambda c: c.idx)
        replayer.apply_commit(commit_changes)
        snapshots[seq] = {
            "state": replayer.state(),
            "aggregate": replayer.aggregate(),
        }
    return snapshots
