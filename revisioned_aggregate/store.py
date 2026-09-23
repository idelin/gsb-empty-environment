"""Durable store for revisions, checkpoints, downstream acks and GC log.

Backed by SQLite so that revision publish, checkpoint advance and ack
recording commit atomically: either the whole unit is durable or none of
it is. Re-publishing the same revision id is a no-op (idempotent).
"""

from __future__ import annotations

import json
import sqlite3

from .model import Revision

SCHEMA = """
CREATE TABLE IF NOT EXISTS revisions (
    id      TEXT PRIMARY KEY,
    txn_id  TEXT NOT NULL,
    bases   TEXT NOT NULL,
    deltas  TEXT NOT NULL,
    seq_lo  INTEGER NOT NULL,
    seq_hi  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS checkpoints (
    name        TEXT PRIMARY KEY,
    commit_seq  INTEGER NOT NULL CHECK (typeof(commit_seq) = 'integer'),
    revision_id TEXT
);
CREATE TABLE IF NOT EXISTS acks (
    ack_id      TEXT PRIMARY KEY,
    revision_id TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS gc_log (
    revision_id TEXT PRIMARY KEY,
    ack_id      TEXT NOT NULL,
    reason      TEXT NOT NULL
);
"""


class RevisionStore:
    def __init__(self, path: str = ":memory:"):
        self.conn = sqlite3.connect(path)
        self.conn.executescript(SCHEMA)

    def publish(self, rev: Revision, checkpoint_seq: int,
                checkpoint_name: str = "ingest") -> bool:
        """Persist revision + advance checkpoint in ONE transaction.

        Returns False when the revision id already exists (re-publish of
        the same id must not double-apply); nothing is written then.
        """
        with self.conn:
            row = self.conn.execute(
                "SELECT 1 FROM revisions WHERE id = ?", (rev.id,)).fetchone()
            if row:
                return False
            self.conn.execute(
                "INSERT INTO revisions (id, txn_id, bases, deltas, seq_lo, seq_hi)"
                " VALUES (?, ?, ?, ?, ?, ?)",
                (rev.id, rev.txn_id, json.dumps(rev.bases),
                 json.dumps(rev.deltas), rev.seq_lo, rev.seq_hi))
            self._set_checkpoint(checkpoint_name, checkpoint_seq, rev.id)
        return True

    def advance_checkpoint(self, checkpoint_seq: int,
                           checkpoint_name: str = "ingest") -> None:
        with self.conn:
            self._set_checkpoint(checkpoint_name, checkpoint_seq, None)

    def _set_checkpoint(self, name: str, seq: int, rev_id) -> None:
        self.conn.execute(
            "INSERT INTO checkpoints (name, commit_seq, revision_id)"
            " VALUES (?, ?, ?)"
            " ON CONFLICT(name) DO UPDATE SET"
            "   commit_seq = excluded.commit_seq,"
            "   revision_id = excluded.revision_id",
            (name, seq, rev_id))

    def get_revision(self, rev_id: str) -> Revision | None:
        row = self.conn.execute(
            "SELECT id, txn_id, bases, deltas, seq_lo, seq_hi"
            " FROM revisions WHERE id = ?", (rev_id,)).fetchone()
        return self._row_to_revision(row) if row else None

    def revisions(self) -> list[Revision]:
        rows = self.conn.execute(
            "SELECT id, txn_id, bases, deltas, seq_lo, seq_hi"
            " FROM revisions ORDER BY seq_hi, id").fetchall()
        return [self._row_to_revision(r) for r in rows]

    @staticmethod
    def _row_to_revision(row) -> Revision:
        return Revision(id=row[0], txn_id=row[1], bases=json.loads(row[2]),
                        deltas=json.loads(row[3]), seq_lo=row[4], seq_hi=row[5])

    def checkpoint(self, name: str = "ingest"):
        row = self.conn.execute(
            "SELECT commit_seq, revision_id FROM checkpoints WHERE name = ?",
            (name,)).fetchone()
        return (row[0], row[1]) if row else None

    def record_ack(self, ack_id: str, revision_id: str) -> None:
        with self.conn:
            self.conn.execute(
                "INSERT OR IGNORE INTO acks (ack_id, revision_id) VALUES (?, ?)",
                (ack_id, revision_id))

    def acked_revision_ids(self) -> set:
        return {r[0] for r in self.conn.execute("SELECT revision_id FROM acks")}

    def gc(self) -> list:
        """Collect old revisions that downstream has acknowledged.

        A revision is collectable only when (a) there is ack evidence from
        the downstream for it and (b) it is no longer the head version of
        any aggregate key. Collected ids are kept in gc_log together with
        the ack that justifies the collection.
        """
        revisions = self.revisions()
        heads = {}
        for rev in revisions:
            for key in rev.deltas:
                heads[key] = rev.id
        head_ids = set(heads.values())
        ack_rows = self.conn.execute(
            "SELECT revision_id, MIN(ack_id) FROM acks GROUP BY revision_id"
        ).fetchall()
        ack_by_rev = dict(ack_rows)
        collected = []
        with self.conn:
            for rev in revisions:
                if rev.id in head_ids or rev.id not in ack_by_rev:
                    continue
                self.conn.execute("DELETE FROM revisions WHERE id = ?", (rev.id,))
                self.conn.execute(
                    "INSERT OR IGNORE INTO gc_log (revision_id, ack_id, reason)"
                    " VALUES (?, ?, ?)",
                    (rev.id, ack_by_rev[rev.id],
                     "acked by downstream and superseded on all keys"))
                collected.append(rev.id)
        return collected

    def gc_log(self) -> list:
        return self.conn.execute(
            "SELECT revision_id, ack_id, reason FROM gc_log ORDER BY revision_id"
        ).fetchall()
