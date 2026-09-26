"""Persistent epsilon budget ledger (synthetic data only).

Implements reservation / commit / cancel of per-query privacy budget with:

- idempotent query IDs (same ID + same content replays safely),
- conflict detection (same ID + different content),
- explicit epsilon-sum composition (basic composition),
- one durable transaction covering the availability check, noise
  generation, result publication and budget deduction,
- crash-retry semantics: once a publication is durable, a retry reuses the
  stored noise and budget record instead of re-sampling,
- an append-only public event log that never contains secret/random state.

Nothing in this module constitutes a formal privacy proof.  Statistical
experiments performed with it are sanity checks only.
"""

from __future__ import annotations

import contextlib
import hashlib
import json
import math
import random
import sqlite3
import time
from dataclasses import dataclass

RESERVED = "RESERVED"
PUBLISHED = "PUBLISHED"
CANCELLED = "CANCELLED"

#: Enumerated failure points at the commit boundary.  A fault injected at
#: any of the in-transaction points rolls the whole commit back, so a retry
#: is safe.  ``after_tx_commit`` simulates a crash *after* the publication
#: became durable; a retry must then reuse the stored noise/budget record.
FAULT_POINTS = (
    "before_tx",
    "after_availability_check",
    "after_noise_generated",
    "after_publish_recorded",
    "after_budget_deducted",
    "after_tx_commit",
)

_EPS_TOL = 1e-9

#: Keys that must never appear in the public event log.
FORBIDDEN_LOG_KEYS = frozenset({"noise", "seed", "secret", "rng", "random_state"})


class LedgerError(Exception):
    """Base class for ledger errors."""


class BudgetExhaustedError(LedgerError):
    """Raised when a reservation would exceed the total budget."""


class ConflictError(LedgerError):
    """Raised when a query ID is reused with different content."""


class UnknownQueryError(LedgerError):
    """Raised when operating on a query ID that was never reserved."""


class InvalidStateError(LedgerError):
    """Raised on illegal state transitions (e.g. cancel after publish)."""


class FaultInjectedError(LedgerError):
    """Raised when a test injects a fault at a commit boundary point."""


@dataclass(frozen=True)
class Reservation:
    query_id: str
    epsilon: float
    status: str
    reused: bool  # True when an existing identical reservation was replayed


@dataclass(frozen=True)
class Publication:
    query_id: str
    result: float
    publish_seq: int
    noise_reused: bool  # True when a durable publication was replayed


@dataclass(frozen=True)
class Cancellation:
    query_id: str
    refunded: bool
    reused: bool


@dataclass(frozen=True)
class BudgetSnapshot:
    total: float
    reserved: float  # held by non-terminal reservations
    spent: float  # consumed by published queries (explicit epsilon sum)
    available: float


def _hash_content(content) -> str:
    canonical = json.dumps(content, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()


class BudgetLedger:
    """Durable epsilon budget ledger backed by a single SQLite file."""

    def __init__(self, db_path, total_epsilon: float, sensitivity: float = 1.0):
        total_epsilon = float(total_epsilon)
        if not math.isfinite(total_epsilon) or total_epsilon <= 0:
            raise ValueError("total_epsilon must be a positive finite number")
        self.db_path = str(db_path)
        self.sensitivity = float(sensitivity)
        if not math.isfinite(self.sensitivity) or self.sensitivity <= 0:
            raise ValueError("sensitivity must be a positive finite number")
        # Randomness source is process-private and is never persisted or
        # written to the public event log.
        self._rng = random.SystemRandom()
        # Schema DDL runs in autocommit mode (executescript manages its own
        # transaction state); only the meta initialisation uses _tx.
        conn = self._connect()
        try:
            conn.executescript(
                """
                CREATE TABLE IF NOT EXISTS meta (
                    key TEXT PRIMARY KEY,
                    value TEXT NOT NULL
                );
                CREATE TABLE IF NOT EXISTS queries (
                    query_id TEXT PRIMARY KEY,
                    content_hash TEXT NOT NULL,
                    epsilon REAL NOT NULL,
                    status TEXT NOT NULL,
                    noise REAL,
                    result REAL,
                    publish_seq INTEGER,
                    created_at REAL NOT NULL,
                    updated_at REAL NOT NULL
                );
                CREATE TABLE IF NOT EXISTS events (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    ts REAL NOT NULL,
                    query_id TEXT NOT NULL,
                    event TEXT NOT NULL,
                    epsilon REAL NOT NULL DEFAULT 0,
                    details TEXT NOT NULL DEFAULT '{}'
                );
                """
            )
        finally:
            conn.close()
        with self._tx() as conn:
            row = conn.execute(
                "SELECT value FROM meta WHERE key = 'total_epsilon'"
            ).fetchone()
            if row is None:
                conn.execute(
                    "INSERT INTO meta(key, value) VALUES ('total_epsilon', ?)",
                    (repr(total_epsilon),),
                )
                conn.execute(
                    "INSERT INTO meta(key, value) VALUES ('sensitivity', ?)",
                    (repr(self.sensitivity),),
                )
            else:
                stored = float(row[0])
                if not math.isclose(stored, total_epsilon, rel_tol=0, abs_tol=_EPS_TOL):
                    raise ConflictError(
                        f"ledger already initialised with total_epsilon={stored}"
                    )
        self.total_epsilon = total_epsilon

    # ------------------------------------------------------------------
    # persistence helpers
    # ------------------------------------------------------------------
    def _connect(self) -> sqlite3.Connection:
        conn = sqlite3.connect(self.db_path, timeout=30.0, isolation_level=None)
        conn.row_factory = sqlite3.Row
        conn.execute("PRAGMA journal_mode=WAL")
        conn.execute("PRAGMA synchronous=FULL")
        conn.execute("PRAGMA busy_timeout=30000")
        return conn

    @contextlib.contextmanager
    def _tx(self):
        conn = self._connect()
        try:
            conn.execute("BEGIN IMMEDIATE")
            yield conn
            conn.execute("COMMIT")
        except BaseException:
            with contextlib.suppress(sqlite3.Error):
                conn.execute("ROLLBACK")
            raise
        finally:
            conn.close()

    @staticmethod
    def _get(conn, query_id: str):
        return conn.execute(
            "SELECT * FROM queries WHERE query_id = ?", (query_id,)
        ).fetchone()

    @staticmethod
    def _log_event(conn, query_id: str, event: str, epsilon: float, details=None):
        details = dict(details or {})
        if FORBIDDEN_LOG_KEYS & set(details):
            raise AssertionError("secret/random state must not enter the public log")
        conn.execute(
            "INSERT INTO events(ts, query_id, event, epsilon, details)"
            " VALUES (?, ?, ?, ?, ?)",
            (time.time(), query_id, event, float(epsilon), json.dumps(details)),
        )

    def _sums(self, conn):
        row = conn.execute(
            "SELECT"
            " COALESCE(SUM(CASE WHEN status = ? THEN epsilon END), 0) AS reserved,"
            " COALESCE(SUM(CASE WHEN status = ? THEN epsilon END), 0) AS spent"
            " FROM queries",
            (RESERVED, PUBLISHED),
        ).fetchone()
        return row["reserved"], row["spent"]

    # ------------------------------------------------------------------
    # public API
    # ------------------------------------------------------------------
    def snapshot(self) -> BudgetSnapshot:
        with self._tx() as conn:
            reserved, spent = self._sums(conn)
        return BudgetSnapshot(
            total=self.total_epsilon,
            reserved=reserved,
            spent=spent,
            available=self.total_epsilon - reserved - spent,
        )

    def composition_bound(self) -> float:
        """Explicit epsilon-sum (basic composition) over published queries."""
        return self.snapshot().spent

    def reserve(self, query_id: str, content, epsilon: float) -> Reservation:
        epsilon = float(epsilon)
        if not math.isfinite(epsilon) or epsilon <= 0:
            raise ValueError("epsilon must be a positive finite number")
        content_hash = _hash_content(content)
        with self._tx() as conn:
            row = self._get(conn, query_id)
            if row is not None:
                if row["content_hash"] != content_hash:
                    raise ConflictError(
                        f"query {query_id!r} already exists with different content"
                    )
                if row["status"] == CANCELLED:
                    raise InvalidStateError(
                        f"query {query_id!r} was cancelled; cancellation is terminal"
                    )
                # Idempotent replay of an identical reservation.
                return Reservation(query_id, row["epsilon"], row["status"], reused=True)
            reserved, spent = self._sums(conn)
            available = self.total_epsilon - reserved - spent
            if epsilon > available + _EPS_TOL:
                raise BudgetExhaustedError(
                    f"requested {epsilon} but only {available} available"
                )
            now = time.time()
            conn.execute(
                "INSERT INTO queries(query_id, content_hash, epsilon, status,"
                " created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
                (query_id, content_hash, epsilon, RESERVED, now, now),
            )
            self._log_event(conn, query_id, "RESERVE", epsilon)
            return Reservation(query_id, epsilon, RESERVED, reused=False)

    def commit(self, query_id: str, content=None, true_value: float = 0.0,
               fault_at: str | None = None) -> Publication:
        """Generate noise, publish the result and deduct budget atomically.

        Retrying a commit that already became durable returns the stored
        result and reuses the original noise and budget record.
        """
        if fault_at is not None and fault_at not in FAULT_POINTS:
            raise ValueError(f"unknown fault point {fault_at!r}")
        if fault_at == "before_tx":
            raise FaultInjectedError("before_tx")
        true_value = float(true_value)
        if not math.isfinite(true_value):
            raise ValueError("true_value must be finite")
        publication = None
        with self._tx() as conn:
            row = self._get(conn, query_id)
            if row is None:
                raise UnknownQueryError(f"query {query_id!r} was never reserved")
            if content is not None and _hash_content(content) != row["content_hash"]:
                raise ConflictError(
                    f"query {query_id!r} committed with different content"
                )
            if row["status"] == PUBLISHED:
                # Durable replay: reuse stored noise, result and budget record.
                return Publication(
                    query_id, row["result"], row["publish_seq"], noise_reused=True
                )
            if row["status"] == CANCELLED:
                raise InvalidStateError(f"query {query_id!r} was cancelled")
            # Budget is already held by the reservation; re-verify conservation.
            reserved, spent = self._sums(conn)
            if reserved + spent > self.total_epsilon + _EPS_TOL:
                raise InvalidStateError("budget conservation violated before commit")
            if fault_at == "after_availability_check":
                raise FaultInjectedError("after_availability_check")
            noise = self._laplace(self.sensitivity / row["epsilon"])
            if fault_at == "after_noise_generated":
                raise FaultInjectedError("after_noise_generated")
            result = true_value + noise
            seq = conn.execute(
                "SELECT COALESCE(MAX(publish_seq), 0) + 1 AS seq FROM queries"
            ).fetchone()["seq"]
            conn.execute(
                "UPDATE queries SET status = ?, noise = ?, result = ?,"
                " publish_seq = ?, updated_at = ? WHERE query_id = ?",
                (PUBLISHED, noise, result, seq, time.time(), query_id),
            )
            if fault_at == "after_publish_recorded":
                raise FaultInjectedError("after_publish_recorded")
            # Budget deduction: the reservation converts to spent budget.
            self._log_event(
                conn, query_id, "PUBLISH", row["epsilon"],
                details={"publish_seq": seq, "result": result},
            )
            if fault_at == "after_budget_deducted":
                raise FaultInjectedError("after_budget_deducted")
            publication = Publication(query_id, result, seq, noise_reused=False)
        if fault_at == "after_tx_commit":
            # Crash after the publication became durable but before the
            # caller observed the outcome.
            raise FaultInjectedError("after_tx_commit")
        return publication

    def cancel(self, query_id: str) -> Cancellation:
        """Cancel a reservation.  Published queries can never be cancelled
        and their budget is never refunded."""
        with self._tx() as conn:
            row = self._get(conn, query_id)
            if row is None:
                raise UnknownQueryError(f"query {query_id!r} was never reserved")
            if row["status"] == PUBLISHED:
                raise InvalidStateError(
                    f"query {query_id!r} is already published; budget is not refunded"
                )
            if row["status"] == CANCELLED:
                return Cancellation(query_id, refunded=False, reused=True)
            conn.execute(
                "UPDATE queries SET status = ?, updated_at = ? WHERE query_id = ?",
                (CANCELLED, time.time(), query_id),
            )
            self._log_event(conn, query_id, "CANCEL", row["epsilon"])
            return Cancellation(query_id, refunded=True, reused=False)

    # ------------------------------------------------------------------
    # noise (private; never logged)
    # ------------------------------------------------------------------
    def _laplace(self, scale: float) -> float:
        u = self._rng.random() - 0.5
        while u == 0.0:
            u = self._rng.random() - 0.5
        return -scale * math.copysign(math.log1p(-2.0 * abs(u)), u)
