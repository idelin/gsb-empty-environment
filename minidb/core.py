"""Core storage engine: tables, stable entity ids, atomic transactions.

Design notes
-------------
* Rows are addressed internally by a *stable entity id* that never changes
  and is never reused, even after the row is deleted.  Primary keys are
  just values in a unique index on top of those ids.
* A primary-key change is applied inside a :class:`Transaction`.  Commit
  runs in phases: detach every old key first, then attach the new keys.
  Because all old keys are detached before any new key is attached, two
  entities may swap keys -- or N entities may rotate keys in a cycle --
  inside one transaction without tripping the uniqueness check midway.
* Foreign-key references follow *entity identity*: when a primary key
  changes, every foreign-key column that points at the moved entity is
  revised to the new key inside the same transaction (reverse-reference
  synchronisation).  Cross-table foreign-key updates and the key change
  itself therefore commit -- or roll back -- as a single unit.
* Any failure during commit restores the checkpoint taken at transaction
  begin, so a failed transaction leaves no partial state behind.
"""

from __future__ import annotations

import copy
from dataclasses import dataclass
from typing import Any, Dict, Hashable, Iterable, List, Optional, Set, Tuple


class ConflictError(Exception):
    """A commit-time uniqueness or referential conflict aborted the txn."""


class UnknownKeyError(Exception):
    """An operation referenced a primary key that does not exist."""


class FrozenSnapshotError(Exception):
    """Attempted to mutate a read-only snapshot."""


@dataclass(frozen=True)
class ForeignKey:
    column: str
    ref_table: str
    ref_column: str


class Table:
    """A table: rows keyed internally by stable entity id."""

    def __init__(
        self,
        name: str,
        primary_key: str,
        foreign_keys: Iterable[ForeignKey] = (),
    ) -> None:
        self.name = name
        self.primary_key = primary_key
        self.foreign_keys: Dict[str, ForeignKey] = {
            fk.column: fk for fk in foreign_keys
        }
        self.rows: Dict[int, Dict[str, Any]] = {}      # entity_id -> row
        self.index: Dict[Hashable, int] = {}           # pk value -> entity_id
        self.versions: Dict[int, int] = {}             # entity_id -> version
        self.commit_seqs: Dict[int, int] = {}          # entity_id -> last seq
        self.retired_ids: Set[int] = set()             # never reused
        self._next_entity_id = 1

    # -- identity ------------------------------------------------------
    def _allocate_entity_id(self) -> int:
        entity_id = self._next_entity_id
        self._next_entity_id += 1
        return entity_id


class Database:
    """A collection of tables plus global reverse-reference bookkeeping."""

    def __init__(self) -> None:
        self.tables: Dict[str, Table] = {}
        # (ref_table, ref_entity_id) -> {(src_table, src_entity_id, column)}
        self.reverse_refs: Dict[Tuple[str, int], Set[Tuple[str, int, str]]] = {}
        self.commit_seq = 0
        self._frozen = False

    # -- schema --------------------------------------------------------
    def create_table(
        self,
        name: str,
        primary_key: str,
        foreign_keys: Iterable[ForeignKey] = (),
    ) -> Table:
        self._check_mutable()
        if name in self.tables:
            raise ConflictError(f"table {name!r} already exists")
        table = Table(name, primary_key, foreign_keys)
        self.tables[name] = table
        return table

    # -- guards ----------------------------------------------------------
    def _check_mutable(self) -> None:
        if self._frozen:
            raise FrozenSnapshotError("snapshots are read-only")

    # -- row operations --------------------------------------------------
    def insert(self, table: str, row: Dict[str, Any]) -> int:
        """Insert a row; returns the new stable entity id."""
        self._check_mutable()
        t = self.tables[table]
        pk = row[t.primary_key]
        if pk in t.index:
            raise ConflictError(f"{table}.{t.primary_key}={pk!r} already exists")
        entity_id = t._allocate_entity_id()
        stored = dict(row)
        t.rows[entity_id] = stored
        t.index[pk] = entity_id
        t.versions[entity_id] = 1
        t.commit_seqs[entity_id] = self.commit_seq
        self._attach_refs(t, entity_id, stored)
        return entity_id

    def delete(self, table: str, pk: Hashable) -> None:
        """Delete a row; its entity id is retired and never reused."""
        self._check_mutable()
        t = self.tables[table]
        entity_id = self._entity_id(t, pk)
        if self.reverse_refs.get((table, entity_id)):
            raise ConflictError(
                f"{table}.{t.primary_key}={pk!r} is still referenced"
            )
        self._detach_refs(t, entity_id, t.rows[entity_id])
        del t.rows[entity_id]
        del t.index[pk]
        del t.versions[entity_id]
        del t.commit_seqs[entity_id]
        t.retired_ids.add(entity_id)

    def get(self, table: str, pk: Hashable) -> Optional[Dict[str, Any]]:
        """Point lookup by primary key; None when the key does not exist."""
        t = self.tables[table]
        entity_id = t.index.get(pk)
        if entity_id is None:
            return None
        return dict(t.rows[entity_id])

    def entity_id_of(self, table: str, pk: Hashable) -> Optional[int]:
        return self.tables[table].index.get(pk)

    # -- reverse references ----------------------------------------------
    def _attach_refs(self, t: Table, entity_id: int, row: Dict[str, Any]) -> None:
        for column, fk in t.foreign_keys.items():
            value = row.get(column)
            if value is None:
                continue
            target = self.tables[fk.ref_table]
            target_id = target.index.get(value)
            if target_id is None:
                raise UnknownKeyError(
                    f"{t.name}.{column}={value!r} has no {fk.ref_table} target"
                )
            self.reverse_refs.setdefault((fk.ref_table, target_id), set()).add(
                (t.name, entity_id, column)
            )

    def _detach_refs(self, t: Table, entity_id: int, row: Dict[str, Any]) -> None:
        for column, fk in t.foreign_keys.items():
            value = row.get(column)
            if value is None:
                continue
            target = self.tables[fk.ref_table]
            target_id = target.index.get(value)
            if target_id is None:
                continue
            refs = self.reverse_refs.get((fk.ref_table, target_id))
            if refs:
                refs.discard((t.name, entity_id, column))
                if not refs:
                    del self.reverse_refs[(fk.ref_table, target_id)]

    # -- transactions ------------------------------------------------------
    def begin(self, txn_id: str) -> "Transaction":
        self._check_mutable()
        return Transaction(self, txn_id)

    def _checkpoint(self) -> Tuple[Any, ...]:
        return (
            copy.deepcopy(self.tables),
            copy.deepcopy(self.reverse_refs),
            self.commit_seq,
        )

    def _restore(self, checkpoint: Tuple[Any, ...]) -> None:
        self.tables, self.reverse_refs, self.commit_seq = checkpoint

    # -- snapshots ---------------------------------------------------------
    def snapshot(self) -> "Database":
        """Return a frozen deep copy; every snapshot is independently
        verifiable and never observes a half-applied transaction."""
        snap = copy.deepcopy(self)
        snap._frozen = True
        return snap

    # -- helpers -----------------------------------------------------------
    @staticmethod
    def _entity_id(t: Table, pk: Hashable) -> int:
        entity_id = t.index.get(pk)
        if entity_id is None:
            raise UnknownKeyError(f"{t.name}.{t.primary_key}={pk!r} not found")
        return entity_id


class Transaction:
    """Buffers primary-key changes and commits them atomically.

    Commit phases:
        1. validate that every old key currently exists;
        2. detach all old keys from the index (old lookups stop hitting);
        3. attach all new keys, failing the *whole* transaction if any new
           key is still occupied by an entity that is not moving away;
        4. revise foreign-key columns that reference the moved entities.
    """

    def __init__(self, db: Database, txn_id: str) -> None:
        self.db = db
        self.txn_id = txn_id
        self._key_changes: List[Tuple[str, Hashable, Hashable, int]] = []
        self.state = "open"

    def change_primary_key(
        self,
        table: str,
        old_pk: Hashable,
        new_pk: Hashable,
        commit_seq: int = 0,
    ) -> None:
        if self.state != "open":
            raise RuntimeError(f"transaction {self.txn_id} is {self.state}")
        self._key_changes.append((table, old_pk, new_pk, commit_seq))

    # ------------------------------------------------------------------
    def commit(self) -> None:
        if self.state != "open":
            raise RuntimeError(f"transaction {self.txn_id} is {self.state}")
        db = self.db
        checkpoint = db._checkpoint()
        try:
            self._apply()
        except Exception:
            db._restore(checkpoint)
            self.state = "rolled back"
            raise
        db.commit_seq = max(
            [db.commit_seq] + [seq for *_rest, seq in self._key_changes]
        )
        self.state = "committed"

    def rollback(self) -> None:
        """Explicit rollback of an uncommitted transaction (a no-op for
        buffered changes, kept for symmetry with commit)."""
        if self.state == "open":
            self._key_changes.clear()
            self.state = "rolled back"

    # ------------------------------------------------------------------
    def _apply(self) -> None:
        db = self.db
        moving: List[Tuple[Table, int, Hashable, Hashable, int]] = []
        seen_old: Set[Tuple[str, Hashable]] = set()
        seen_new: Set[Tuple[str, Hashable]] = set()

        # Phase 1: validate old keys exist and are unique within the txn.
        for table_name, old_pk, new_pk, seq in self._key_changes:
            t = db.tables[table_name]
            if (table_name, old_pk) in seen_old:
                raise ConflictError(
                    f"{table_name}.{t.primary_key}={old_pk!r} moved twice "
                    f"in transaction {self.txn_id}"
                )
            if (table_name, new_pk) in seen_new:
                raise ConflictError(
                    f"{table_name}.{t.primary_key}={new_pk!r} assigned twice "
                    f"in transaction {self.txn_id}"
                )
            entity_id = t.index.get(old_pk)
            if entity_id is None:
                raise UnknownKeyError(
                    f"{table_name}.{t.primary_key}={old_pk!r} not found"
                )
            seen_old.add((table_name, old_pk))
            seen_new.add((table_name, new_pk))
            moving.append((t, entity_id, old_pk, new_pk, seq))

        # Phase 2: detach every old key (atomic undo target for rollback).
        for t, entity_id, old_pk, _new_pk, _seq in moving:
            del t.index[old_pk]

        # Phase 3: attach new keys; any survivor conflict fails everything.
        for t, entity_id, _old_pk, new_pk, seq in moving:
            if new_pk in t.index:
                raise ConflictError(
                    f"{t.name}.{t.primary_key}={new_pk!r} is occupied; "
                    f"transaction {self.txn_id} fails as a whole"
                )
            t.index[new_pk] = entity_id
            t.rows[entity_id][t.primary_key] = new_pk
            t.versions[entity_id] += 1
            if seq:
                t.commit_seqs[entity_id] = max(t.commit_seqs[entity_id], seq)

        # Phase 4: revise foreign keys that reference the moved entities.
        # Reverse refs are keyed by stable entity id, so references follow
        # the *entity*, not the key value -- this is what makes swaps safe.
        for t, entity_id, _old_pk, new_pk, _seq in moving:
            referrers = db.reverse_refs.get((t.name, entity_id), ())
            for src_table, src_entity_id, column in sorted(referrers):
                db.tables[src_table].rows[src_entity_id][column] = new_pk
