"""Bounded replay-deduplication inbox.

Messages are identified by (sender, strictly increasing sequence number).
They may arrive out of order within a bounded window W. Business state,
the dedup cursor, and the out-of-order pending cache are persisted in a
single SQLite transaction, so recovery after a crash yields exactly-once
effects.

CLI:
    python3 inbox.py [--db PATH] [--window N] < commands.txt

Input commands (one per line, whitespace separated):
    recv <sender> <seq> <value>   deliver a message carrying integer value
    state                         print current cursors and balance
    crash before|after            arm a crash for the NEXT recv, either
                                  before the commit or after the commit
    help                          print this summary

Output per recv (single line):
    APPLIED  sender=S seq=N applied=a,b,c cursor=C balance=B
    CACHED   sender=S seq=N cursor=C balance=B
    IGNORED  sender=S seq=N cursor=C balance=B   (seq <= committed cursor)
    REJECTED sender=S seq=N cursor=C balance=B   (seq > cursor + W)
Output for state:
    STATE cursor={sender: seq, ...} balance=B
A crash prints "CRASH <before|after>-commit" to stderr and exits with
code 3; re-run the program on the same DB file to recover.
"""

from __future__ import annotations

import argparse
import sqlite3
import sys

SCHEMA = """
CREATE TABLE IF NOT EXISTS cursors (
    sender TEXT PRIMARY KEY,
    cursor INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS pending (
    sender TEXT NOT NULL,
    seq    INTEGER NOT NULL,
    value  INTEGER NOT NULL,
    PRIMARY KEY (sender, seq)
);
CREATE TABLE IF NOT EXISTS business (
    id      INTEGER PRIMARY KEY CHECK (id = 1),
    balance INTEGER NOT NULL
);
INSERT OR IGNORE INTO business (id, balance) VALUES (1, 0);
"""

APPLIED = "APPLIED"
CACHED = "CACHED"
IGNORED = "IGNORED"
REJECTED = "REJECTED"


class SimulatedCrash(Exception):
    """Raised at an injected fault point to simulate process death."""

    def __init__(self, point: str):
        super().__init__(point)
        self.point = point


class Inbox:
    """Bounded-window dedup inbox backed by a single SQLite database."""

    def __init__(self, db_path: str, window: int):
        if window < 1:
            raise ValueError("window must be >= 1")
        self.window = window
        self._conn = sqlite3.connect(db_path)
        self._conn.executescript(SCHEMA)
        self._conn.commit()
        # Fault injection: None | "before" | "after" (relative to commit).
        self.crash_point: str | None = None

    def close(self) -> None:
        self._conn.close()

    def _cursor_of(self, sender: str) -> int:
        row = self._conn.execute(
            "SELECT cursor FROM cursors WHERE sender = ?", (sender,)
        ).fetchone()
        return row[0] if row else 0

    def _balance(self) -> int:
        return self._conn.execute(
            "SELECT balance FROM business WHERE id = 1"
        ).fetchone()[0]

    def state(self) -> tuple[dict[str, int], int]:
        rows = self._conn.execute("SELECT sender, cursor FROM cursors").fetchall()
        return {sender: cursor for sender, cursor in rows}, self._balance()

    def receive(self, sender: str, seq: int, value: int) -> tuple[str, list[int]]:
        """Deliver one message. Returns (status, applied_seqs).

        The dedup decision, business effect, cursor advance, and pending
        cache update all commit in ONE transaction.
        """
        if seq < 1:
            raise ValueError("seq must be >= 1")
        cursor = self._cursor_of(sender)
        if seq <= cursor:
            return IGNORED, []
        if seq > cursor + self.window:
            return REJECTED, []

        conn = self._conn
        try:
            conn.execute("BEGIN IMMEDIATE")
            conn.execute(
                "INSERT OR IGNORE INTO pending (sender, seq, value) VALUES (?, ?, ?)",
                (sender, seq, value),
            )
            applied: list[int] = []
            next_seq = cursor + 1
            while True:
                row = conn.execute(
                    "SELECT value FROM pending WHERE sender = ? AND seq = ?",
                    (sender, next_seq),
                ).fetchone()
                if row is None:
                    break
                conn.execute(
                    "UPDATE business SET balance = balance + ? WHERE id = 1",
                    (row[0],),
                )
                conn.execute(
                    "DELETE FROM pending WHERE sender = ? AND seq = ?",
                    (sender, next_seq),
                )
                applied.append(next_seq)
                next_seq += 1
            if applied:
                conn.execute(
                    "INSERT INTO cursors (sender, cursor) VALUES (?, ?) "
                    "ON CONFLICT(sender) DO UPDATE SET cursor = excluded.cursor",
                    (sender, applied[-1]),
                )
            if self.crash_point == "before":
                # Process dies before commit: transaction rolls back.
                conn.rollback()
                raise SimulatedCrash("before")
            conn.commit()
            if self.crash_point == "after":
                # Process dies after commit but before reporting success.
                raise SimulatedCrash("after")
        except SimulatedCrash:
            raise
        except Exception:
            conn.rollback()
            raise
        return (APPLIED if applied else CACHED), applied


def run_cli(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Bounded replay-dedup inbox")
    parser.add_argument("--db", default="inbox.db", help="SQLite file path")
    parser.add_argument("--window", type=int, default=3, help="out-of-order window W")
    args = parser.parse_args(argv)

    inbox = Inbox(args.db, args.window)
    for raw in sys.stdin:
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        parts = line.split()
        cmd = parts[0]
        if cmd == "recv":
            sender, seq, value = parts[1], int(parts[2]), int(parts[3])
            try:
                status, applied = inbox.receive(sender, seq, value)
            except SimulatedCrash as crash:
                print(f"CRASH {crash.point}-commit", file=sys.stderr)
                inbox.close()
                return 3
            cursor = inbox._cursor_of(sender)
            balance = inbox._balance()
            if status == APPLIED:
                seqs = ",".join(str(s) for s in applied)
                print(
                    f"APPLIED sender={sender} seq={seq} applied={seqs} "
                    f"cursor={cursor} balance={balance}"
                )
            else:
                print(
                    f"{status} sender={sender} seq={seq} "
                    f"cursor={cursor} balance={balance}"
                )
        elif cmd == "state":
            cursors, balance = inbox.state()
            print(f"STATE cursor={cursors} balance={balance}")
        elif cmd == "crash":
            if parts[1] not in ("before", "after"):
                print("ERROR crash takes before|after", file=sys.stderr)
                return 2
            inbox.crash_point = parts[1]
        elif cmd == "help":
            print(__doc__)
        else:
            print(f"ERROR unknown command: {cmd}", file=sys.stderr)
            return 2
    inbox.close()
    return 0


if __name__ == "__main__":
    sys.exit(run_cli())
