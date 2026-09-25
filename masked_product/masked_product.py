"""Sparse masked A @ B with transactional, spillable incremental maintenance.

The implementation uses only the Python standard library.  A mask cell is a
structural bit: only paths whose (k, j) edge belongs to M are visible.  The
mask complement is never materialised.
"""

from __future__ import annotations

import numbers
import os
import pickle
import shutil
import sqlite3
import tempfile
import threading
from collections import defaultdict
from contextlib import contextmanager
from dataclasses import dataclass
from pathlib import Path
from typing import Iterator, Mapping, Sequence


_EMPTY: dict[object, object] = {}
_MISSING = object()


class TransactionError(RuntimeError):
    """Base class for transaction failures."""


class BatchCancelled(TransactionError):
    """Raised when a batch is explicitly cancelled."""


class SpillError(TransactionError):
    """Raised when intermediate contributions cannot be spilled."""


class VersionMismatch(KeyError):
    """Raised when a query is bound to an unknown version vector."""


@dataclass(frozen=True)
class VersionVector:
    """Immutable input versions (A, B, M)."""

    a: int
    b: int
    m: int

    def as_tuple(self) -> tuple[int, int, int]:
        return (self.a, self.b, self.m)


def _is_number(value: object) -> bool:
    return isinstance(value, numbers.Number) and not isinstance(value, bool)


def _is_zero(value: object) -> bool:
    return isinstance(value, numbers.Number) and value == 0


def _intersect(left, right):
    """Yield common keys without constructing the complete intersection."""
    if isinstance(left, (set, dict)) and isinstance(right, (set, dict)):
        if len(left) > len(right):
            left, right = right, left
        for key in left:
            if key in right:
                yield key
        return
    if len(right) < len(left):
        for key in right:
            if key in left:
                yield key
    else:
        for key in left:
            if key in right:
                yield key


class _OverlayMapping:
    """Dict-like sparse overlay; ``None`` in changes means deletion."""

    __slots__ = ("base", "changes")

    def __init__(self, base: Mapping[int, object], changes: Mapping[int, object]):
        self.base = base
        self.changes = changes

    def __len__(self) -> int:
        return sum(1 for _ in self)

    def __iter__(self) -> Iterator[int]:
        for key, value in self.changes.items():
            if value is not None and key not in self.base:
                yield key
        for key in self.base:
            if key not in self.changes:
                yield key

    def __contains__(self, key: object) -> bool:
        if key in self.changes:
            return self.changes[key] is not None
        return key in self.base

    def __getitem__(self, key: int) -> object:
        value = self.changes.get(key, _EMPTY)
        if value is not _EMPTY:
            if value is None:
                raise KeyError(key)
            return value
        return self.base[key]

    def get(self, key: int, default: object = None) -> object:
        try:
            return self[key]
        except KeyError:
            return default


class _OverlaySet:
    """Set-like sparse overlay used for mask rows."""

    __slots__ = ("base", "adds", "removes")

    def __init__(self, base: set[int], adds: set[int], removes: set[int]):
        self.base = base
        self.adds = adds
        self.removes = removes

    def __len__(self) -> int:
        return sum(1 for _ in self)

    def __iter__(self) -> Iterator[int]:
        for key in self.adds:
            if key not in self.base and key not in self.removes:
                yield key
        for key in self.base:
            if key not in self.removes and key not in self.adds:
                yield key

    def __contains__(self, key: object) -> bool:
        if key in self.removes:
            return False
        if key in self.adds:
            return True
        return key in self.base


class _DeltaTable:
    """Sparse per-output-row contribution deltas.

    Hot rows are moved out of Python memory into a private SQLite file as soon
    as that row exceeds ``row_threshold``.  A value of ``None`` is a tombstone.
    """

    def __init__(self, directory: Path, row_threshold: int):
        if row_threshold <= 0:
            raise ValueError("row_threshold must be positive")
        self.row_threshold = row_threshold
        self.directory = directory
        self.directory.mkdir(parents=True, exist_ok=True)
        self.path = self.directory / "delta.sqlite3"
        self.memory: dict[int, dict[tuple[int, int], object]] = {}
        self.spilled_rows: set[int] = set()
        self._conn: sqlite3.Connection | None = None

    @property
    def connection(self) -> sqlite3.Connection:
        if self._conn is None:
            self._conn = sqlite3.connect(self.path, isolation_level=None)
            self._conn.execute("PRAGMA journal_mode=DELETE")
            self._conn.execute(
                "CREATE TABLE delta ("
                "row_id INTEGER NOT NULL, j INTEGER NOT NULL, k INTEGER NOT NULL, "
                "value BLOB, PRIMARY KEY(row_id, j, k)) WITHOUT ROWID"
            )
        return self._conn

    def upsert(self, row: int, key: tuple[int, int], value: object) -> None:
        if row in self.spilled_rows:
            self._upsert_sql(row, key, value)
            return
        table = self.memory.setdefault(row, {})
        table[key] = value
        if len(table) > self.row_threshold:
            self._spill_row(row)

    def _upsert_sql(self, row: int, key: tuple[int, int], value: object) -> None:
        j, k = key
        if value is None:
            self.connection.execute(
                "INSERT INTO delta(row_id,j,k,value) VALUES(?,?,?,?) "
                "ON CONFLICT(row_id,j,k) DO UPDATE SET value=excluded.value",
                (row, j, k, b""),
            )
        else:
            self.connection.execute(
                "INSERT INTO delta(row_id,j,k,value) VALUES(?,?,?,?) "
                "ON CONFLICT(row_id,j,k) DO UPDATE SET value=excluded.value",
                (row, j, k, pickle.dumps(value)),
            )

    def _spill_row(self, row: int) -> None:
        try:
            conn = self.connection
            conn.execute("BEGIN IMMEDIATE")
            conn.executemany(
                "INSERT OR REPLACE INTO delta(row_id,j,k,value) VALUES(?,?,?,?)",
                (
                    (row, j, k, b"" if value is None else pickle.dumps(value))
                    for (j, k), value in self.memory[row].items()
                ),
            )
            conn.execute("COMMIT")
        except Exception as exc:
            try:
                self.connection.execute("ROLLBACK")
            except sqlite3.Error:
                pass
            raise SpillError("failed to spill hot contribution row") from exc
        del self.memory[row]
        self.spilled_rows.add(row)

    def row_count(self, row: int) -> int:
        if row in self.spilled_rows:
            cur = self.connection.execute(
                "SELECT COUNT(*) FROM delta WHERE row_id=?", (row,)
            )
            return int(cur.fetchone()[0])
        return len(self.memory.get(row, {}))

    def entries(self, row: int):
        if row in self.spilled_rows:
            cur = self.connection.execute(
                "SELECT j,k,value FROM delta WHERE row_id=? ORDER BY j,k", (row,)
            )
            for j, k, raw in cur:
                yield (j, k), None if raw == b"" else pickle.loads(raw)
            return
        table = self.memory.get(row)
        if table is not None:
            for key, value in sorted(table.items()):
                yield key, value

    def rows(self) -> list[int]:
        rows = set(self.memory) | self.spilled_rows
        return sorted(rows)

    def memory_size(self) -> int:
        return sum(len(row) for row in self.memory.values())

    def close(self) -> None:
        if self._conn is not None:
            self._conn.close()
            self._conn = None
        shutil.rmtree(self.directory, ignore_errors=True)


class _HotStore:
    """SQLite-backed storage for spilled contribution rows and old releases."""

    def __init__(self, directory: Path, initial_version: VersionVector):
        self.directory = directory
        self.directory.mkdir(parents=True, exist_ok=True)
        self.path = self.directory / "hot.sqlite3"
        self.conn = sqlite3.connect(self.path, check_same_thread=False)
        self.conn.execute("PRAGMA journal_mode=WAL")
        self.read_conn = sqlite3.connect(self.path, check_same_thread=False)
        self.read_conn.execute("PRAGMA query_only=ON")
        self.conn.executescript(
            """
            CREATE TABLE IF NOT EXISTS hot (
                row_id INTEGER NOT NULL,
                j INTEGER NOT NULL,
                k INTEGER NOT NULL,
                value BLOB NOT NULL,
                PRIMARY KEY(row_id, j, k)
            ) WITHOUT ROWID;
            CREATE TABLE IF NOT EXISTS staging (
                stage_id INTEGER NOT NULL,
                row_id INTEGER NOT NULL,
                j INTEGER NOT NULL,
                k INTEGER NOT NULL,
                value BLOB NOT NULL,
                PRIMARY KEY(stage_id, row_id, j, k)
            ) WITHOUT ROWID;
            CREATE TABLE IF NOT EXISTS releases (
                release_id INTEGER PRIMARY KEY
            );
            CREATE TABLE IF NOT EXISTS hot_counts (
                row_id INTEGER PRIMARY KEY,
                count INTEGER NOT NULL
            );
            CREATE TABLE IF NOT EXISTS release_cells (
                release_id INTEGER NOT NULL,
                row_id INTEGER NOT NULL,
                j INTEGER NOT NULL,
                value BLOB NOT NULL,
                PRIMARY KEY(release_id, row_id, j)
            ) WITHOUT ROWID;
            CREATE TABLE IF NOT EXISTS release_delta (
                stage_id INTEGER NOT NULL,
                row_id INTEGER NOT NULL,
                j INTEGER NOT NULL,
                value BLOB,
                PRIMARY KEY(stage_id, row_id, j)
            ) WITHOUT ROWID;
            """
        )
        existing = self.conn.execute("SELECT release_id FROM releases").fetchone()
        if existing is None:
            self.conn.execute("INSERT INTO releases(release_id) VALUES (1)")
            self.conn.commit()
        self.initial_version = initial_version

    def row_count(self, row: int) -> int:
        cur = self.read_conn.execute(
            "SELECT COUNT(*) FROM hot WHERE row_id=?", (row,)
        )
        return int(cur.fetchone()[0])

    def total_count(self) -> int:
        cur = self.conn.execute("SELECT COUNT(*) FROM hot")
        return int(cur.fetchone()[0])

    def get(self, row: int, key: tuple[int, int]):
        cur = self.conn.execute(
            "SELECT value FROM hot WHERE row_id=? AND j=? AND k=?",
            (row, key[0], key[1]),
        )
        found = cur.fetchone()
        return None if found is None else pickle.loads(found[0])

    def stream(self, row: int):
        cur = self.conn.execute(
            "SELECT j,k,value FROM hot WHERE row_id=? ORDER BY j,k", (row,)
        )
        for j, k, raw in cur:
            yield (j, k), pickle.loads(raw)

    def replace_staged_row(
        self, stage_id: int, row: int, entries
    ) -> int:
        try:
            self.conn.execute("BEGIN IMMEDIATE")
            self.conn.execute(
                "DELETE FROM staging WHERE stage_id=? AND row_id=?",
                (stage_id, row),
            )
            self.conn.executemany(
                "INSERT INTO staging(stage_id,row_id,j,k,value) "
                "VALUES(?,?,?,?,?)",
                (
                    (stage_id, row, j, k, pickle.dumps(value))
                    for (j, k), value in entries
                ),
            )
            self.conn.execute("COMMIT")
        except Exception as exc:
            self._rollback()
            raise SpillError("failed to stage spilled row") from exc
        return self.staged_row_count(stage_id, row)

    def stage_existing_hot(
        self, stage_id: int, row: int, delta_entries
    ) -> int:
        changes: dict[tuple[int, int], object] = {}
        for key, value in delta_entries:
            changes[key] = value
        try:
            self.conn.execute("BEGIN IMMEDIATE")
            self.conn.execute(
                "DELETE FROM staging WHERE stage_id=? AND row_id=?",
                (stage_id, row),
            )
            self.conn.executemany(
                "INSERT INTO staging(stage_id,row_id,j,k,value) "
                "VALUES(?,?,?,?,?)",
                (
                    (stage_id, row, j, k, pickle.dumps(value))
                    for (j, k), value in changes.items()
                    if value is not None
                ),
            )
            self.conn.executemany(
                "INSERT INTO staging(stage_id,row_id,j,k,value) "
                "SELECT ?,row_id,j,k,value FROM hot AS h "
                "WHERE row_id=? AND NOT EXISTS ("
                "SELECT 1 FROM staging s WHERE s.stage_id=? AND s.row_id=? "
                "AND s.j=h.j AND s.k=h.k)",
                ((stage_id, row, stage_id, row),),
            )
            count = self.staged_row_count(stage_id, row)
            self.conn.execute("COMMIT")
            return count
        except Exception as exc:
            self._rollback()
            raise SpillError("failed to stage spilled row") from exc

    def apply_existing_hot_staging(
        self, stage_id: int, row: int, delta_entries
    ) -> tuple[int, object]:
        count_delta = 0
        total_delta = 0
        try:
            self.conn.execute("BEGIN IMMEDIATE")
            self.conn.execute(
                "DELETE FROM staging WHERE stage_id=? AND row_id=?",
                (stage_id, row),
            )
            self.conn.execute(
                "INSERT INTO staging(stage_id,row_id,j,k,value) "
                "SELECT ?,row_id,j,k,value FROM hot WHERE row_id=?",
                (stage_id, row),
            )
            for (j, k), value in delta_entries:
                cur = self.conn.execute(
                    "SELECT value FROM staging WHERE stage_id=? AND row_id=? "
                    "AND j=? AND k=?",
                    (stage_id, row, j, k),
                )
                found = cur.fetchone()
                if value is None:
                    if found is not None:
                        old = pickle.loads(found[0])
                        total_delta -= old
                        count_delta -= 1
                        self.conn.execute(
                            "DELETE FROM staging WHERE stage_id=? AND row_id=? "
                            "AND j=? AND k=?",
                            (stage_id, row, j, k),
                        )
                else:
                    if found is None:
                        count_delta += 1
                    else:
                        total_delta -= pickle.loads(found[0])
                    total_delta += value
                    self.conn.execute(
                        "INSERT INTO staging(stage_id,row_id,j,k,value) "
                        "VALUES(?,?,?,?,?) ON CONFLICT(stage_id,row_id,j,k) "
                        "DO UPDATE SET value=excluded.value",
                        (stage_id, row, j, k, pickle.dumps(value)),
                    )
            self.conn.execute("COMMIT")
            return count_delta, total_delta
        except Exception as exc:
            self._rollback()
            raise SpillError("failed to stage spilled row") from exc

    def stage_memory_hot(
        self,
        stage_id: int,
        row: int,
        base_entries,
        delta_entries,
    ) -> int:
        try:
            self.conn.execute("BEGIN IMMEDIATE")
            self.conn.execute(
                "DELETE FROM staging WHERE stage_id=? AND row_id=?",
                (stage_id, row),
            )
            self.conn.executemany(
                "INSERT INTO staging(stage_id,row_id,j,k,value) "
                "VALUES(?,?,?,?,?)",
                (
                    (stage_id, row, j, k, pickle.dumps(value))
                    for (j, k), value in base_entries
                ),
            )
            for (j, k), value in delta_entries:
                if value is None:
                    self.conn.execute(
                        "DELETE FROM staging WHERE stage_id=? AND row_id=? "
                        "AND j=? AND k=?",
                        (stage_id, row, j, k),
                    )
                else:
                    self.conn.execute(
                        "INSERT INTO staging(stage_id,row_id,j,k,value) "
                        "VALUES(?,?,?,?,?) ON CONFLICT(stage_id,row_id,j,k) "
                        "DO UPDATE SET value=excluded.value",
                        (stage_id, row, j, k, pickle.dumps(value)),
                    )
            count = self.staged_row_count(stage_id, row)
            self.conn.execute("COMMIT")
            return count
        except Exception as exc:
            self._rollback()
            raise SpillError("failed to stage spilled row") from exc

    def stage_release_deltas(self, stage_id: int, items) -> None:
        try:
            self.conn.execute("BEGIN IMMEDIATE")
            self.conn.executemany(
                "INSERT INTO release_delta(stage_id,row_id,j,value) "
                "VALUES(?,?,?,?) ON CONFLICT(stage_id,row_id,j) "
                "DO UPDATE SET value=excluded.value",
                (
                    (stage_id, row, j, None if value is None else pickle.dumps(value))
                    for row, j, value in items
                ),
            )
            self.conn.execute("COMMIT")
        except Exception as exc:
            self._rollback()
            raise SpillError("failed to stage release deltas") from exc

    def staged_row_count(self, stage_id: int, row: int) -> int:
        cur = self.conn.execute(
            "SELECT COUNT(*) FROM staging WHERE stage_id=? AND row_id=?",
            (stage_id, row),
        )
        return int(cur.fetchone()[0])

    def stream_staged(self, stage_id: int, row: int):
        cur = self.conn.execute(
            "SELECT j,k,value FROM staging "
            "WHERE stage_id=? AND row_id=? ORDER BY j,k",
            (stage_id, row),
        )
        for j, k, raw in cur:
            yield (j, k), pickle.loads(raw)

    def publish(
        self,
        stage_id: int,
        release_id: int,
        staged_rows: Sequence[int] = (),
    ) -> None:
        try:
            self.conn.execute("BEGIN IMMEDIATE")
            for row in staged_rows:
                self.conn.execute("DELETE FROM hot WHERE row_id=?", (row,))
                self.conn.execute(
                    "INSERT INTO hot(row_id,j,k,value) "
                    "SELECT row_id,j,k,value FROM staging "
                    "WHERE stage_id=? AND row_id=?",
                    (stage_id, row),
                )
                self.conn.execute("DELETE FROM hot_counts WHERE row_id=?", (row,))
                self.conn.execute(
                    "INSERT INTO hot_counts(row_id,count) "
                    "SELECT row_id,COUNT(*) FROM hot WHERE row_id=? GROUP BY row_id",
                    (row,),
                )
            self.conn.execute("INSERT INTO releases(release_id) VALUES (?)",
                              (release_id,))
            self.conn.executemany(
                "INSERT INTO release_cells(release_id,row_id,j,value) "
                "SELECT ?,row_id,j,value FROM release_cells AS old "
                "WHERE old.release_id=? AND NOT EXISTS ("
                "SELECT 1 FROM release_delta d WHERE d.stage_id=? "
                "AND d.row_id=old.row_id AND d.j=old.j)",
                ((release_id, release_id - 1, stage_id),),
            )
            self.conn.execute(
                "INSERT INTO release_cells(release_id,row_id,j,value) "
                "SELECT ?,row_id,j,value FROM release_delta "
                "WHERE stage_id=? AND value IS NOT NULL",
                (release_id, stage_id),
            )
            self.conn.execute("DELETE FROM staging WHERE stage_id=?",
                              (stage_id,))
            self.conn.execute("DELETE FROM release_delta WHERE stage_id=?",
                              (stage_id,))
            self.conn.execute("COMMIT")
        except Exception as exc:
            self._rollback()
            raise SpillError("failed to publish spilled contributions") from exc

    def discard_stage(self, stage_id: int, rows: Sequence[int] = ()) -> None:
        try:
            self.conn.execute("BEGIN IMMEDIATE")
            if rows:
                for row in rows:
                    self.conn.execute(
                        "DELETE FROM staging WHERE stage_id=? AND row_id=?",
                        (stage_id, row),
                    )
            else:
                self.conn.execute(
                    "DELETE FROM staging WHERE stage_id=?", (stage_id,)
                )
            self.conn.execute(
                "DELETE FROM release_delta WHERE stage_id=?", (stage_id,)
            )
            self.conn.execute("COMMIT")
        except sqlite3.Error:
            self._rollback()

    def recover_staged(self, stage_id: int, rows: Sequence[int]):
        for row in rows:
            yield row, list(self.stream_staged(stage_id, row))
        self.discard_stage(stage_id, rows)

    def prune_releases(self, retain_ids: set[int]) -> None:
        values = list(retain_ids)
        if not values:
            return
        placeholders = ",".join("?" for _ in values)
        self.conn.execute("BEGIN IMMEDIATE")
        self.conn.execute(
            f"DELETE FROM release_cells WHERE release_id NOT IN ({placeholders})",
            values,
        )
        self.conn.execute(
            f"DELETE FROM releases WHERE release_id NOT IN ({placeholders})",
            values,
        )
        self.conn.execute("COMMIT")

    def release_rows(self, release_id: int):
        current_row = None
        cells: list[tuple[int, object]] = []
        cur = self.conn.execute(
            "SELECT row_id,j,value FROM release_cells "
            "WHERE release_id=? ORDER BY row_id,j",
            (release_id,),
        )
        for row, j, raw in cur:
            if current_row is not None and row != current_row:
                yield current_row, cells
                cells = []
            current_row = row
            cells.append((j, pickle.loads(raw)))
        if current_row is not None:
            yield current_row, cells

    def _rollback(self) -> None:
        try:
            self.conn.execute("ROLLBACK")
        except sqlite3.Error:
            pass

    def close(self) -> None:
        try:
            self.conn.execute("PRAGMA wal_checkpoint(TRUNCATE)")
            self.read_conn.close()
            self.conn.execute("PRAGMA journal_mode=DELETE")
        except sqlite3.Error:
            pass
        self.conn.close()


class Batch:
    """Private batch transaction; publication happens in :meth:`commit`."""

    def __init__(self, engine: "MaskedABProduct"):
        self._engine = engine
        self._closed = False
        self._a_changes: dict[tuple[int, int], object] = {}
        self._b_changes: dict[tuple[int, int], object] = {}
        self._mask_adds: defaultdict[int, set[int]] = defaultdict(set)
        self._mask_removes: defaultdict[int, set[int]] = defaultdict(set)

    def _require_open(self) -> None:
        if self._closed:
            raise TransactionError("batch is already closed")

    def set_a(self, i: int, k: int, value: object) -> "Batch":
        self._require_open()
        self._engine._check_a_index(i, k)
        if not _is_number(value):
            raise TypeError("A values must be real or complex numbers")
        self._a_changes[(i, k)] = None if _is_zero(value) else value
        return self

    def delete_a(self, i: int, k: int) -> "Batch":
        self._require_open()
        self._engine._check_a_index(i, k)
        self._a_changes[(i, k)] = None
        return self

    def set_b(self, k: int, j: int, value: object) -> "Batch":
        self._require_open()
        self._engine._check_b_index(k, j)
        if not _is_number(value):
            raise TypeError("B values must be real or complex numbers")
        self._b_changes[(k, j)] = None if _is_zero(value) else value
        return self

    def delete_b(self, k: int, j: int) -> "Batch":
        self._require_open()
        self._engine._check_b_index(k, j)
        self._b_changes[(k, j)] = None
        return self

    def add_mask(self, k: int, j: int) -> "Batch":
        self._require_open()
        self._engine._check_b_index(k, j)
        self._mask_adds[k].add(j)
        self._mask_removes[k].discard(j)
        return self

    def remove_mask(self, k: int, j: int) -> "Batch":
        self._require_open()
        self._engine._check_b_index(k, j)
        self._mask_removes[k].add(j)
        self._mask_adds[k].discard(j)
        return self

    def commit(self) -> VersionVector:
        self._require_open()
        try:
            return self._engine._commit(self)
        finally:
            self._closed = True

    def cancel(self) -> None:
        self._require_open()
        self._closed = True
        raise BatchCancelled("batch cancelled")

    def __enter__(self) -> "Batch":
        return self

    def __exit__(self, exc_type, exc, tb) -> bool:
        if not self._closed:
            if exc_type is None:
                self.commit()
            else:
                self._closed = True
        return False


class MaskedABProduct:
    """Maintain sparse masked A @ B contributions with MVCC queries."""

    def __init__(
        self,
        n: int,
        p: int,
        m: int,
        *,
        row_threshold: int = 64,
        memory_contributions: int = 256,
        spill_dir: str | os.PathLike[str] | None = None,
        keep_spill: bool = False,
    ):
        if min(n, p, m) < 0 or any(x != int(x) for x in (n, p, m)):
            raise ValueError("dimensions must be non-negative integers")
        if row_threshold <= 0 or memory_contributions <= 0:
            raise ValueError("memory limits must be positive")
        self.n = int(n)
        self.p = int(p)
        self.m = int(m)
        self.row_threshold = int(row_threshold)
        self.memory_contributions = int(memory_contributions)
        self._lock = threading.RLock()
        self._owns_spill = spill_dir is None
        self._keep_spill = keep_spill
        self._spill_root = Path(
            spill_dir
            if spill_dir is not None
            else tempfile.mkdtemp(prefix="masked-product-")
        )
        self._spill_root.mkdir(parents=True, exist_ok=True)
        self._delta_seq = 0

        self.a_rows: dict[int, dict[int, object]] = {}
        self.a_cols: defaultdict[int, dict[int, object]] = defaultdict(dict)
        self.b_cols: defaultdict[int, dict[int, object]] = defaultdict(dict)
        self.b_rows: defaultdict[int, dict[int, object]] = defaultdict(dict)
        self.mask: defaultdict[int, set[int]] = defaultdict(set)

        self.contributions: dict[int, dict[tuple[int, int], object]] = {}
        self.source_counts: dict[int, dict[tuple[int, int], int]] = {}
        self.hot_rows: set[int] = set()
        self.versions = VersionVector(1, 1, 1)
        self.release_ids: dict[VersionVector, int] = {self.versions: 1}
        self.pinned_releases: defaultdict[int, int] = defaultdict(int)
        self.current_release_id = 1
        self.hot = _HotStore(self._spill_root / "hot", self.versions)

    @property
    def current_version(self) -> VersionVector:
        return self.versions

    @property
    def active_contribution_count(self) -> int:
        with self._lock:
            count = self.hot.total_count()
            count += sum(len(row) for row in self.contributions.values())
            return count

    @property
    def memory_contribution_count(self) -> int:
        with self._lock:
            return sum(len(row) for row in self.contributions.values())

    @property
    def spilled_row_count(self) -> int:
        with self._lock:
            return len(self.hot_rows)

    def batch(self) -> Batch:
        return Batch(self)

    def close(self) -> None:
        with self._lock:
            self.hot.close()
            if self._owns_spill and not self._keep_spill:
                shutil.rmtree(self._spill_root, ignore_errors=True)

    def __enter__(self) -> "MaskedABProduct":
        return self

    def __exit__(self, exc_type, exc, tb) -> bool:
        self.close()
        return False

    def _check_a_index(self, i: int, k: int) -> None:
        if not (0 <= int(i) < self.n and 0 <= int(k) < self.m):
            raise IndexError("A index outside explicit (n, m) bounds")

    def _check_b_index(self, k: int, j: int) -> None:
        if not (0 <= int(k) < self.m and 0 <= int(j) < self.p):
            raise IndexError("B/M index outside explicit (m, p) bounds")

    def _commit(self, batch: Batch) -> VersionVector:
        with self._lock:
            return self._commit_locked(batch)

    def _commit_locked(self, batch: Batch) -> VersionVector:
        old_a = self.versions.a
        old_b = self.versions.b
        old_m = self.versions.m
        actual_a = {
            key: value
            for key, value in batch._a_changes.items()
            if self.a_rows.get(key[0], _EMPTY).get(key[1]) != value
        }
        actual_b = {
            key: value
            for key, value in batch._b_changes.items()
            if self.b_rows[key[0]].get(key[1]) != value
        }
        actual_mask_adds = defaultdict(set)
        actual_mask_removes = defaultdict(set)
        for k in batch._mask_adds:
            adds = batch._mask_adds[k] - self.mask[k]
            removes = batch._mask_removes[k] & self.mask[k]
            adds -= removes
            if adds:
                actual_mask_adds[k] = adds
            if removes:
                actual_mask_removes[k] = removes

        if not (actual_a or actual_b or actual_mask_adds or actual_mask_removes):
            return self.versions

        self._delta_seq += 1
        delta_dir = self._spill_root / f"delta-{self._delta_seq}"
        delta = _DeltaTable(delta_dir, self.row_threshold)
        stage_id = self._delta_seq
        stage_rows: list[int] = []
        staged_memory: list[int] = []
        memory_undo: list[tuple] = []
        local_totals: defaultdict[tuple[int, int], object] = defaultdict(int)

        def add_contribution(i: int, j: int, k: int, product: object) -> None:
            delta.upsert(i, (j, k), product)

        def remove_contribution(i: int, j: int, k: int) -> None:
            delta.upsert(i, (j, k), None)

        def observe_a(i: int, k: int, old_av, new_av) -> None:
            old_mask = self.mask[k]
            new_mask = _OverlaySet(
                old_mask,
                actual_mask_adds.get(k, set()),
                actual_mask_removes.get(k, set()),
            )
            old_b_row = self.b_rows[k]
            new_b_row = _OverlayMapping(
                old_b_row,
                {
                    j: value
                    for (bk, j), value in actual_b.items()
                    if bk == k
                },
            )
            candidates = set(old_mask) | set(new_mask)
            candidates |= set(old_b_row) | set(new_b_row)
            for j in candidates:
                old_bv = old_b_row.get(j)
                new_bv = new_b_row.get(j)
                old_visible = (
                    old_av is not None and old_bv is not None and j in old_mask
                )
                new_visible = (
                    new_av is not None and new_bv is not None and j in new_mask
                )
                if old_visible:
                    local_totals[(i, j)] -= old_av * old_bv
                    remove_contribution(i, j, k)
                if new_visible:
                    local_totals[(i, j)] += new_av * new_bv
                    add_contribution(i, j, k, new_av * new_bv)

        a_changed_rows = defaultdict(dict)
        for (i, k), value in actual_a.items():
            a_changed_rows[i][k] = value
        a_changed_cells = {
            (i, k) for i, row in a_changed_rows.items() for k in row
        }

        b_changed_rows = defaultdict(dict)
        for (k, j), value in actual_b.items():
            b_changed_rows[k][j] = value

        for i, changes in a_changed_rows.items():
            old_a_row = self.a_rows.get(i, _EMPTY)
            for k, new_av in changes.items():
                observe_a(i, k, old_a_row.get(k), new_av)

        for k, b_changes in b_changed_rows.items():
            old_b_row = self.b_rows[k]
            old_mask = self.mask[k]
            new_mask = _OverlaySet(
                old_mask,
                actual_mask_adds.get(k, set()),
                actual_mask_removes.get(k, set()),
            )
            for i, av in self.a_cols[k].items():
                if (i, k) in a_changed_cells:
                    continue
                for j, new_bv in b_changes.items():
                    old_bv = old_b_row.get(j)
                    old_visible = old_bv is not None and j in old_mask
                    new_visible = new_bv is not None and j in new_mask
                    if old_visible:
                        local_totals[(i, j)] -= av * old_bv
                        remove_contribution(i, j, k)
                    if new_visible:
                        local_totals[(i, j)] += av * new_bv
                        add_contribution(i, j, k, av * new_bv)

        for k, adds in actual_mask_adds.items():
            removes = actual_mask_removes.get(k, set())
            b_row = self.b_rows[k]
            for j in adds:
                if j in removes:
                    continue
                bv = b_row.get(j)
                if bv is None:
                    continue
                for i in self.a_cols[k]:
                    if (i, k) in a_changed_cells or (k, j) in actual_b:
                        continue
                    av = self.a_rows[i][k]
                    local_totals[(i, j)] += av * bv
                    add_contribution(i, j, k, av * bv)

        for k, removes in actual_mask_removes.items():
            b_row = self.b_rows[k]
            for j in removes:
                bv = b_row.get(j)
                if bv is None:
                    continue
                for i in self.a_cols[k]:
                    if (i, k) in a_changed_cells or (k, j) in actual_b:
                        continue
                    av = self.a_rows[i][k]
                    local_totals[(i, j)] -= av * bv
                    remove_contribution(i, j, k)

        try:
            release_deltas = self._publish_delta(
                delta=delta,
                stage_id=stage_id,
                stage_rows=stage_rows,
                staged_memory=staged_memory,
                memory_undo=memory_undo,
                local_totals=local_totals,
            )
            self._finish_commit(
                stage_id=stage_id,
                release_deltas=release_deltas,
                stage_rows=stage_rows,
                staged_memory=staged_memory,
                actual_a=actual_a,
                actual_b=actual_b,
                actual_mask_adds=actual_mask_adds,
                actual_mask_removes=actual_mask_removes,
                old_versions=(old_a, old_b, old_m),
            )
        except Exception:
            self._abort_commit(
                stage_id=stage_id,
                stage_rows=stage_rows,
                staged_memory=staged_memory,
                memory_undo=memory_undo,
            )
            delta.close()
            raise

        delta.close()
        return self.versions

    def _publish_delta(
        self,
        *,
        delta: _DeltaTable,
        stage_id: int,
        stage_rows: list[int],
        staged_memory: list[int],
        memory_undo: list,
        local_totals: defaultdict,
    ) -> list[tuple[int, int, object]]:
        target_rows = set(delta.rows())

        def mutate_memory_row(row: int) -> None:
            table = self.contributions.setdefault(row, {})
            counts = self.source_counts.setdefault(row, {})
            for key, value in delta.entries(row):
                old = table.get(key)
                old_count = counts.get(key, 0)
                memory_undo.append(
                    (
                        table,
                        counts,
                        key,
                        _MISSING if old is None else old,
                        _MISSING if old_count == 0 else old_count,
                    )
                )
                if value is None:
                    table.pop(key, None)
                    counts.pop(key, None)
                else:
                    table[key] = value
                    counts[key] = old_count if old is not None else old_count + 1

        mutated_memory_rows: set[int] = set()
        for row in sorted(target_rows):
            delta_count = delta.row_count(row)
            if row in self.hot_rows:
                try:
                    count_delta, _ = self.hot.apply_existing_hot_staging(
                        stage_id, row, delta.entries(row)
                    )
                except SpillError:
                    raise
                except Exception as exc:
                    raise SpillError("failed to spill hot contribution row") from exc
                stage_rows.append(row)
            elif delta_count > self.row_threshold:
                base = self.contributions.get(row, {})
                try:
                    count = self.hot.stage_memory_hot(
                        stage_id,
                        row,
                        base.items(),
                        delta.entries(row),
                    )
                except SpillError:
                    raise
                except Exception as exc:
                    raise SpillError("failed to spill hot contribution row") from exc
                stage_rows.append(row)
                staged_memory.append(row)
            else:
                mutate_memory_row(row)
                mutated_memory_rows.add(row)

        while self._total_memory_contributions(skip=staged_memory) > self.memory_contributions:
            candidates = [
                (len(self.contributions.get(row, {})), row)
                for row in self.contributions
                if row not in stage_rows
            ]
            if not candidates:
                break
            _, row = max(candidates)
            entries = list(self.contributions[row].items())
            try:
                count = self.hot.stage_memory_hot(stage_id, row, entries, [])
            except SpillError:
                raise
            except Exception as exc:
                raise SpillError("failed to spill hot contribution row") from exc
            stage_rows.append(row)
            if row in mutated_memory_rows:
                staged_memory.append(row)
            else:
                staged_memory.append(row)

        release_deltas: list[tuple[int, int, object]] = []
        touched_outputs = sorted(local_totals)
        for row, j in touched_outputs:
            if row in stage_rows:
                final_value = 0
                source_count = 0
                for (jj, _), value in self.hot.stream_staged(stage_id, row):
                    if jj == j:
                        final_value += value
                        source_count += 1
            else:
                final_value = 0
                source_count = len(
                    [key for key in self.source_counts.get(row, {}) if key[0] == j]
                )
                for (jj, _), value in self.contributions.get(row, {}).items():
                    if jj == j:
                        final_value += value
            release_deltas.append(
                (row, j, None if final_value == 0 and source_count == 0 else final_value)
            )

        self.hot.stage_release_deltas(stage_id, release_deltas)
        return release_deltas

    def _total_memory_contributions(self, skip: Sequence[int]) -> int:
        skipped = set(skip)
        return sum(
            len(row)
            for i, row in self.contributions.items()
            if i not in skipped
        )

    def _abort_commit(
        self,
        *,
        stage_id: int,
        stage_rows: Sequence[int],
        staged_memory: Sequence[int],
        memory_undo: Sequence[tuple],
    ) -> None:
        staged_memory_set = set(staged_memory)
        for row in sorted(staged_memory_set, reverse=True):
            restored: dict[tuple[int, int], object] = {}
            for key, value in self.hot.stream_staged(stage_id, row):
                restored[key] = value
            if restored:
                self.contributions[row] = restored
                self.source_counts[row] = {key: 1 for key in restored}
            else:
                self.contributions.pop(row, None)
                self.source_counts.pop(row, None)
        for table, counts, key, old_value, old_count in reversed(memory_undo):
            if old_value is _MISSING:
                table.pop(key, None)
                counts.pop(key, None)
            else:
                table[key] = old_value
                counts[key] = old_count
        for row in list(self.source_counts):
            if not self.source_counts[row] and row not in self.contributions:
                del self.source_counts[row]
        self.hot.discard_stage(stage_id, stage_rows)

    def _finish_commit(
        self,
        *,
        stage_id: int,
        release_deltas: Sequence[tuple[int, int, object]],
        stage_rows: Sequence[int],
        staged_memory: Sequence[int],
        actual_a: Mapping,
        actual_b: Mapping,
        actual_mask_adds: Mapping,
        actual_mask_removes: Mapping,
        old_versions: tuple[int, int, int],
    ) -> None:
        new_release_id = self.current_release_id + 1
        try:
            self.hot.publish(stage_id, new_release_id, stage_rows)
        except Exception:
            raise

        for row in staged_memory:
            self.contributions.pop(row, None)
            self.source_counts.pop(row, None)
            self.hot_rows.add(row)
        for row in stage_rows:
            self.hot_rows.add(row)
        for row in list(self.contributions):
            if not self.contributions[row]:
                del self.contributions[row]
                self.source_counts.pop(row, None)

        for (i, k), value in actual_a.items():
            row = self.a_rows.setdefault(i, {})
            col = self.a_cols[k]
            if value is None:
                row.pop(k, None)
                col.pop(i, None)
            else:
                row[k] = value
                col[i] = value
            if not row:
                self.a_rows.pop(i, None)
            if not col:
                del self.a_cols[k]

        for (k, j), value in actual_b.items():
            row = self.b_rows[k]
            col = self.b_cols[j]
            if value is None:
                row.pop(j, None)
                col.pop(k, None)
            else:
                row[j] = value
                col[k] = value
            if not row:
                del self.b_rows[k]
            if not col:
                del self.b_cols[j]

        for k, adds in actual_mask_adds.items():
            self.mask[k].update(adds)
        for k, removes in actual_mask_removes.items():
            self.mask[k].difference_update(removes)
            if not self.mask[k]:
                del self.mask[k]

        self.versions = VersionVector(
            old_versions[0] + bool(actual_a),
            old_versions[1] + bool(actual_b),
            old_versions[2] + bool(actual_mask_adds or actual_mask_removes),
        )
        self.current_release_id = new_release_id
        self.release_ids[self.versions] = new_release_id
        self._prune_releases()

    def _release_id(self, version: VersionVector | tuple[int, int, int]) -> int:
        vector = version if isinstance(version, VersionVector) else VersionVector(*version)
        try:
            return self.release_ids[vector]
        except KeyError:
            raise VersionMismatch(f"unknown input version vector {vector}") from None

    def _pin(self, release_id: int) -> None:
        self.pinned_releases[release_id] += 1

    def _unpin(self, release_id: int) -> None:
        count = self.pinned_releases[release_id] - 1
        if count <= 0:
            del self.pinned_releases[release_id]
        else:
            self.pinned_releases[release_id] = count
        self._prune_releases()

    def _prune_releases(self) -> None:
        retain = set(self.pinned_releases) | {self.current_release_id}
        self.hot.prune_releases(retain)

    def snapshot(
        self, version: VersionVector | tuple[int, int, int] | None = None
    ) -> "Snapshot":
        with self._lock:
            vector = self.versions if version is None else (
                version if isinstance(version, VersionVector)
                else VersionVector(*version)
            )
            release_id = self._release_id(vector)
            self._pin(release_id)
            return Snapshot(self, vector, release_id)

    def query(
        self, version: VersionVector | tuple[int, int, int] | None = None
    ) -> list[tuple[int, int, object]]:
        with self.snapshot(version) as snap:
            return snap.rows()

    def contribution_sources(self, i: int, j: int) -> int:
        """Current number of k-paths contributing to output (i,j)."""
        with self._lock:
            if i in self.hot_rows:
                cur = self.hot.conn.execute(
                    "SELECT COUNT(*) FROM hot WHERE row_id=? AND j=?", (i, j)
                )
                return int(cur.fetchone()[0])
            return sum(1 for jj, _ in self.contributions.get(i, {}) if jj == j)

    def export_inputs(self):
        with self._lock:
            return (
                {
                    (i, k): value
                    for i, row in self.a_rows.items()
                    for k, value in row.items()
                },
                {
                    (k, j): value
                    for k, row in self.b_rows.items()
                    for j, value in row.items()
                },
                {
                    (k, j)
                    for k, row in self.mask.items()
                    for j in row
                },
            )

    def check_integrity(self) -> None:
        """Test-only invariant check: every retained contribution is visible."""
        with self._lock:
            memory_count = sum(len(row) for row in self.contributions.values())
            hot_count = self.hot.total_count()
            expected = 0
            for i, a_row in self.a_rows.items():
                for k, av in a_row.items():
                    for j in self.mask.get(k, set()):
                        if j in self.b_rows.get(k, {}):
                            expected += 1
            if memory_count + hot_count != expected:
                raise AssertionError(
                    f"orphan contributions: {memory_count + hot_count} != {expected}"
                )
            for i, table in self.contributions.items():
                counts = self.source_counts.get(i, {})
                if set(counts) != set(table):
                    raise AssertionError("source-count keys diverge from contributions")
                for key, count in counts.items():
                    if count != 1:
                        raise AssertionError("a unique k-path cannot have count != 1")


class Snapshot:
    """Immutable read handle pinned to one (A,B,M) version vector."""

    def __init__(self, engine: MaskedABProduct, version: VersionVector, release_id: int):
        self._engine = engine
        self.version = version
        self._release_id = release_id
        self._closed = False

    @property
    def closed(self) -> bool:
        return self._closed

    def rows(self) -> list[tuple[int, int, object]]:
        if self._closed:
            raise TransactionError("snapshot is closed")
        result: list[tuple[int, int, object]] = []
        for row, cells in self._engine.hot.release_rows(self._release_id):
            for j, value in cells:
                result.append((row, j, value))
        return result

    def value(self, i: int, j: int):
        if self._closed:
            raise TransactionError("snapshot is closed")
        cur = self._engine.hot.read_conn.execute(
            "SELECT value FROM release_cells "
            "WHERE release_id=? AND row_id=? AND j=?",
            (self._release_id, i, j),
        )
        found = cur.fetchone()
        return None if found is None else pickle.loads(found[0])

    def close(self) -> None:
        if not self._closed:
            self._closed = True
            with self._engine._lock:
                self._engine._unpin(self._release_id)

    def __enter__(self) -> "Snapshot":
        return self

    def __exit__(self, exc_type, exc, tb) -> bool:
        self.close()
        return False


def dense_reference(
    a: Mapping[tuple[int, int], object],
    b: Mapping[tuple[int, int], object],
    mask: set[tuple[int, int]],
    n: int,
    p: int,
    m: int,
) -> list[list[object]]:
    """Small, deliberately dense reference implementation for test checks."""
    result = [[0 for _ in range(p)] for _ in range(n)]
    for i in range(n):
        for j in range(p):
            total = 0
            for k in range(m):
                av = a.get((i, k))
                bv = b.get((k, j))
                if av is not None and bv is not None and (k, j) in mask:
                    total += av * bv
            result[i][j] = total
    return result
