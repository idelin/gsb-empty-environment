"""SQLite-backed persistence.

One database file is the single persistence decision point: sign-off
records and the notification outbox are written inside the same
transaction, so a crash can never persist one without the other.
"""

from __future__ import annotations

import sqlite3
from contextlib import contextmanager

SCHEMA = """
CREATE TABLE IF NOT EXISTS manifests (
    package_id TEXT NOT NULL,
    version INTEGER NOT NULL,
    digest TEXT NOT NULL,
    json TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (package_id, version)
);
CREATE TABLE IF NOT EXISTS chunks (
    package_id TEXT NOT NULL,
    manifest_version INTEGER NOT NULL,
    file_id TEXT NOT NULL,
    chunk_index INTEGER NOT NULL,
    digest TEXT NOT NULL,
    data BLOB NOT NULL,
    PRIMARY KEY (manifest_version, file_id, chunk_index)
);
CREATE TABLE IF NOT EXISTS signoffs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    package_id TEXT NOT NULL,
    manifest_version INTEGER NOT NULL,
    manifest_digest TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS outbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    signoff_id INTEGER,
    kind TEXT NOT NULL,
    payload TEXT NOT NULL,
    delivered INTEGER NOT NULL DEFAULT 0,
    retracted INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL
);
"""


class Store:
    def __init__(self, path):
        self.path = str(path)
        conn = self.connect()
        try:
            conn.executescript(SCHEMA)
        finally:
            conn.close()

    def connect(self) -> sqlite3.Connection:
        conn = sqlite3.connect(self.path, isolation_level=None)
        conn.row_factory = sqlite3.Row
        conn.execute("PRAGMA journal_mode=WAL")
        conn.execute("PRAGMA foreign_keys=ON")
        return conn

    @contextmanager
    def transaction(self):
        """Explicit BEGIN IMMEDIATE / COMMIT / ROLLBACK block."""
        conn = self.connect()
        try:
            conn.execute("BEGIN IMMEDIATE")
            yield conn
            conn.execute("COMMIT")
        except BaseException:
            conn.execute("ROLLBACK")
            raise
        finally:
            conn.close()

    def query(self, sql, params=()):
        conn = self.connect()
        try:
            return conn.execute(sql, params).fetchall()
        finally:
            conn.close()
