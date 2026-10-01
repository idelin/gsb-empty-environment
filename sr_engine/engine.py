"""Transactional sparse semiring matrix engine maintaining C = A x B.

A is m x k, B is k x n, C is m x n.  All three stay sparse: a cell is
present iff its value differs from the semiring zero.

For every output cell (i, j) the engine keeps the per-k contributions
``contrib[(i, j)][k] = A[i, k] (x) B[k, j]`` together with inverted
indexes ``_A_cols[k] -> {i: v}`` and ``_B_rows[k] -> {j: v}``.  A
transaction therefore only recomputes the output cells reachable through
the inverted index of the entries it touches -- never the whole matrix.
Because the aggregate of each output cell is recomputed from its
contribution map, deletion works uniformly for semirings without
subtraction (min-plus falls back to the next-best candidate, boolean
keeps surviving witness paths, integer sums that cancel to zero drop
back to the canonical sparse form).

Concurrency model: a single writer thread (guarded by a lock) and any
number of reader threads pinning immutable version snapshots.
"""

from __future__ import annotations

import threading
from types import MappingProxyType

from .errors import (
    DimensionError,
    DuplicateTxnError,
    EngineError,
    VersionNotRetainedError,
)
from .semirings import get_semiring
from .storage import Storage, _sha, make_txn_record

_DELETE = object()


class Stats:
    """Work counters proving updates stay local to the inverted index."""

    def __init__(self):
        self.contrib_updates = 0
        self.output_recomputes = 0

    def reset(self):
        self.contrib_updates = 0
        self.output_recomputes = 0


class Snapshot:
    """Immutable, pinned view of A, B and C at one retained version."""

    def __init__(self, engine, version, state):
        self._engine = engine
        self.version = version
        self.A, self.B, self.C, self._contrib = state
        self._closed = False

    def value(self, i, j):
        return self.C.get((i, j), self._engine.semiring.zero)

    def witnesses(self, i, j):
        """Contributing k indices in ascending order.

        For min-plus these are exactly the k attaining the minimum, so
        tied optima are reported in ascending k order.
        """
        km = self._contrib.get((i, j))
        if not km:
            return []
        if self._engine.semiring.name == "minplus":
            best = self.C.get((i, j))
            if best is None:
                return []
            return sorted(k for k, v in km.items() if v == best)
        return sorted(km)

    def close(self):
        if not self._closed:
            self._closed = True
            self._engine._unpin(self.version)

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()
        return False


class Engine:
    def __init__(self, m, k, n, semiring, storage=None):
        if not all(isinstance(d, int) and not isinstance(d, bool) and d >= 0 for d in (m, k, n)):
            raise DimensionError("dimensions must be non-negative integers")
        self.m, self.k, self.n = m, k, n
        self.semiring = get_semiring(semiring)
        self.storage = storage
        self.A = {}
        self.B = {}
        self._A_cols = {}  # k -> {i: value}   inverted index of A
        self._B_rows = {}  # k -> {j: value}   inverted index of B
        self._contrib = {}  # (i, j) -> {k: A[i,k] (x) B[k,j]}
        self.C = {}
        self.version = 0
        self.stats = Stats()
        self._versions = {}
        self._pins = {}
        self._txn_ids = {}  # txn_id -> (version, opsha)
        self._write_lock = threading.Lock()
        self._snap_lock = threading.Lock()
        self._save_version()

    # ------------------------------------------------------------------
    # construction / recovery
    # ------------------------------------------------------------------
    @classmethod
    def create(cls, path, m, k, n, semiring):
        storage = Storage(path)
        if storage.has_checkpoint():
            raise EngineError(f"{path} already initialised")
        eng = cls(m, k, n, semiring, storage)
        eng.checkpoint()
        return eng

    @classmethod
    def open(cls, path):
        storage = Storage(path)
        state = storage.read_checkpoint()
        if state is None:
            raise EngineError(f"{path} has no checkpoint; use create()")
        eng = cls(state["m"], state["k"], state["n"], state["semiring"], storage)
        eng.version = state["version"]
        eng._txn_ids = {t: (v, h) for t, v, h in state["txns"]}
        eng.A = {(i, j): v for i, j, v in state["A"]}
        eng.B = {(i, j): v for i, j, v in state["B"]}
        eng._rebuild_indexes()
        eng._rebuild_c()  # full rebuild is allowed on checkpoint recovery
        eng._versions = {}
        eng._save_version()
        for rec in storage.recover():
            if rec["version"] <= eng.version:
                continue  # already captured by the checkpoint
            merged = {
                (t, i, j): (_DELETE if v is None else v) for t, i, j, v in rec["ops"]
            }
            eng._apply(merged)
            eng.version = rec["version"]
            eng._txn_ids[rec["txn_id"]] = (rec["version"], rec["opsha"])
            eng._save_version()
        storage.open_append()
        return eng

    def close(self):
        if self.storage is not None:
            self.storage.close()

    # ------------------------------------------------------------------
    # validation
    # ------------------------------------------------------------------
    def _check_coords(self, target, i, j):
        if not all(isinstance(x, int) and not isinstance(x, bool) for x in (i, j)):
            raise DimensionError("coordinates must be integers")
        rows, cols = (self.m, self.k) if target == "A" else (self.k, self.n)
        if not (0 <= i < rows and 0 <= j < cols):
            raise DimensionError(
                f"{target}[{i},{j}] out of bounds for {rows}x{cols} matrix"
            )

    def _validate_and_merge(self, ops):
        """Validate every op; merge duplicate coordinates (last wins).

        Raises before any state is touched, so a rejected transaction
        rolls back cleanly.
        """
        if not ops:
            raise ValueError("transaction must contain at least one op")
        merged = {}
        for op in ops:
            action = op[0]
            if action == "set":
                if len(op) != 5:
                    raise ValueError("set op must be ('set', target, i, j, value)")
                _, target, i, j, v = op
            elif action == "del":
                if len(op) != 4:
                    raise ValueError("del op must be ('del', target, i, j)")
                _, target, i, j = op
                v = _DELETE
            else:
                raise ValueError(f"unknown op action {action!r}")
            if target not in ("A", "B"):
                raise ValueError(f"unknown target {target!r}")
            self._check_coords(target, i, j)
            if v is not _DELETE:
                v = self.semiring.validate(v)
                if self.semiring.is_zero(v):
                    v = _DELETE  # explicit zero canonicalises to absent
            merged[(target, i, j)] = v
        return merged

    @staticmethod
    def _wire(merged):
        return [
            [t, i, j, None if v is _DELETE else v]
            for (t, i, j), v in sorted(merged.items())
        ]

    # ------------------------------------------------------------------
    # commit path
    # ------------------------------------------------------------------
    def commit(self, txn_id, ops):
        """Validate, durably log, then publish one transaction."""
        if not isinstance(txn_id, str) or not txn_id:
            raise ValueError("txn_id must be a non-empty string")
        merged = self._validate_and_merge(ops)  # rollback-safe: raises first
        wire = self._wire(merged)
        opsha = _sha(wire)
        with self._write_lock:
            if txn_id in self._txn_ids:
                version, known = self._txn_ids[txn_id]
                if known == opsha:
                    return version  # idempotent retry of the same txn
                raise DuplicateTxnError(
                    f"txn_id {txn_id!r} already committed with different content"
                )
            version = self.version + 1
            if self.storage is not None:
                # Publish point: visible only after the record is durable.
                self.storage.append_txn(make_txn_record(txn_id, version, wire, opsha))
            self._apply(merged)
            self.version = version
            self._txn_ids[txn_id] = (version, opsha)
            self._save_version()
            return version

    def _apply(self, merged):
        a_ops = [(i, j) for (t, i, j) in merged if t == "A"]
        b_ops = [(i, j) for (t, i, j) in merged if t == "B"]
        # Phase 1: collect affected outputs via the inverted indexes,
        # including the A-op x B-op cross terms of this same transaction.
        affected = {}
        for i, k in a_ops:
            for j in self._B_rows.get(k, ()):
                affected.setdefault((i, j), set()).add(k)
        for k, j in b_ops:
            for i in self._A_cols.get(k, ()):
                affected.setdefault((i, j), set()).add(k)
        b_by_k = {}
        for k, j in b_ops:
            b_by_k.setdefault(k, []).append(j)
        for i, k in a_ops:
            for j in b_by_k.get(k, ()):
                affected.setdefault((i, j), set()).add(k)
        # Phase 2: mutate inputs and their inverted indexes.
        for (t, i, j), v in merged.items():
            if t == "A":
                if v is _DELETE:
                    self.A.pop((i, j), None)
                    col = self._A_cols.get(j)
                    if col is not None:
                        col.pop(i, None)
                        if not col:
                            del self._A_cols[j]
                else:
                    self.A[(i, j)] = v
                    self._A_cols.setdefault(j, {})[i] = v
            else:
                if v is _DELETE:
                    self.B.pop((i, j), None)
                    row = self._B_rows.get(i)
                    if row is not None:
                        row.pop(j, None)
                        if not row:
                            del self._B_rows[i]
                else:
                    self.B[(i, j)] = v
                    self._B_rows.setdefault(i, {})[j] = v
        # Phase 3: refresh only the touched contributions, then
        # re-aggregate each affected output from its contribution map.
        sr = self.semiring
        for (i, j), ks in affected.items():
            km = self._contrib.get((i, j))
            for k in ks:
                a = self.A.get((i, k))
                b = self.B.get((k, j))
                if a is not None and b is not None:
                    if km is None:
                        km = self._contrib[(i, j)] = {}
                    km[k] = sr.mul(a, b)
                elif km is not None:
                    km.pop(k, None)
                self.stats.contrib_updates += 1
            self.stats.output_recomputes += 1
            km = self._contrib.get((i, j))
            value = self._aggregate(km) if km else None
            if value is None:
                self.C.pop((i, j), None)
                if km is not None and not km:
                    del self._contrib[(i, j)]
            else:
                self.C[(i, j)] = value

    def _aggregate(self, km):
        """Fold a contribution map; None means the cell stays absent."""
        sr = self.semiring
        if sr.name == "minplus":
            return min(km.values())
        if sr.name == "boolean":
            return True
        total = sr.zero
        for v in km.values():
            total = sr.add(total, v)
        return None if sr.is_zero(total) else total

    # ------------------------------------------------------------------
    # full rebuild (checkpoint recovery)
    # ------------------------------------------------------------------
    def _rebuild_indexes(self):
        self._A_cols = {}
        for (i, k), v in self.A.items():
            self._A_cols.setdefault(k, {})[i] = v
        self._B_rows = {}
        for (k, j), v in self.B.items():
            self._B_rows.setdefault(k, {})[j] = v

    def _rebuild_c(self):
        self._contrib = {}
        for (i, k), a in self.A.items():
            row = self._B_rows.get(k)
            if not row:
                continue
            for j, b in row.items():
                self._contrib.setdefault((i, j), {})[k] = self.semiring.mul(a, b)
        self.C = {}
        for ij, km in self._contrib.items():
            value = self._aggregate(km)
            if value is not None:
                self.C[ij] = value
        self._contrib = {ij: km for ij, km in self._contrib.items() if km}

    # ------------------------------------------------------------------
    # versions and snapshots
    # ------------------------------------------------------------------
    def _save_version(self):
        self._versions[self.version] = (
            MappingProxyType(dict(self.A)),
            MappingProxyType(dict(self.B)),
            MappingProxyType(dict(self.C)),
            MappingProxyType(
                {ij: MappingProxyType(dict(km)) for ij, km in self._contrib.items()}
            ),
        )

    def snapshot(self, version=None):
        with self._snap_lock:
            v = self.version if version is None else version
            if v not in self._versions:
                raise VersionNotRetainedError(f"version {v} not retained")
            self._pins[v] = self._pins.get(v, 0) + 1
            return Snapshot(self, v, self._versions[v])

    def _unpin(self, version):
        with self._snap_lock:
            self._pins[version] = self._pins.get(version, 1) - 1

    def gc(self):
        """Drop retained versions that are neither current nor pinned."""
        with self._snap_lock:
            keep = {self.version} | {v for v, c in self._pins.items() if c > 0}
            self._versions = {v: s for v, s in self._versions.items() if v in keep}

    def retained_versions(self):
        """Versions currently available for snapshots."""
        with self._snap_lock:
            return sorted(self._versions)

    def value(self, i, j):
        return self.C.get((i, j), self.semiring.zero)

    def witnesses(self, i, j):
        with self.snapshot() as snap:
            return snap.witnesses(i, j)

    # ------------------------------------------------------------------
    # checkpointing
    # ------------------------------------------------------------------
    def checkpoint(self):
        if self.storage is None:
            raise EngineError("in-memory engine has no storage")
        with self._write_lock:
            state = {
                "m": self.m,
                "k": self.k,
                "n": self.n,
                "semiring": self.semiring.name,
                "version": self.version,
                "A": [[i, j, v] for (i, j), v in sorted(self.A.items())],
                "B": [[i, j, v] for (i, j), v in sorted(self.B.items())],
                "txns": [[t, v, h] for t, (v, h) in sorted(self._txn_ids.items())],
            }
            self.storage.write_checkpoint(state)
