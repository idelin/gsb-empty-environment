"""SQLite persistence. Sign-off rows and outbox rows are written inside one
transaction, so a notification can never exist without a valid sign-off."""
from __future__ import annotations

import sqlite3
from contextlib import contextmanager


SCHEMA = """
CREATE TABLE IF NOT EXISTS manifests (
    manifest_id TEXT NOT NULL,
    version INTEGER NOT NULL,
    body TEXT NOT NULL,
    digest TEXT NOT NULL,
    status TEXT NOT NULL,
    received_at REAL NOT NULL,
    PRIMARY KEY (manifest_id, version)
);
CREATE TABLE IF NOT EXISTS chunks (
    manifest_id TEXT NOT NULL,
    manifest_version INTEGER NOT NULL,
    file_id TEXT NOT NULL,
    idx INTEGER NOT NULL,
    digest TEXT NOT NULL,
    data BLOB NOT NULL,
    PRIMARY KEY (manifest_id, manifest_version, file_id, idx)
);
CREATE TABLE IF NOT EXISTS signoffs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    manifest_id TEXT NOT NULL,
    manifest_version INTEGER NOT NULL,
    manifest_digest TEXT NOT NULL,
    decision TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS outbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    signoff_id INTEGER,
    kind TEXT NOT NULL,
    payload TEXT NOT NULL,
    delivered INTEGER NOT NULL DEFAULT 0,
    created_at REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
"""


class Store:
    def __init__(self, path: str = ":memory:"):
        # Autocommit mode: transactions are controlled explicitly via
        # atomic(), so sign-off and outbox truly share one commit.
        self.conn = sqlite3.connect(path, isolation_level=None)
        self.conn.row_factory = sqlite3.Row
        self.conn.execute("PRAGMA foreign_keys = ON")
        self.conn.executescript(SCHEMA)

    def close(self) -> None:
        self.conn.close()

    @contextmanager
    def atomic(self):
        self.conn.execute("BEGIN IMMEDIATE")
        try:
            yield
            self.conn.commit()
        except BaseException:
            self.conn.rollback()
            raise
