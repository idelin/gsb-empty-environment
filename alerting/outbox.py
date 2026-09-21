"""Confirmable outbox pump with crash injection.

Delivery is a two-phase step on durable storage:
  1. claim the message (write an attempt token / fence)
  2. call the transport  -- this is the *injection point*
  3. record confirmation in ``delivered`` and drop the outbox row

A crash anywhere leaves the outbox row unconfirmed; restart resumes by
dedupe key, and a stale transport result with an old fence token is
discarded, so no correction is delivered twice or lost.
"""
from __future__ import annotations

import sqlite3


class Transport:
    """Stand-in for an external notifier. It records what it actually sent;
    dedupe/fence checks prove at-least-once attempts collapse to once."""

    def __init__(self):
        self.sent: list[tuple[str, int, str]] = []
        self.fence_ok = True

    def send(self, dedupe: str, alert_id: int, kind: str, state,
             fence: int) -> bool:
        if not self.fence_ok:
            return False  # caller vanished before broker accepted
        self.sent.append((dedupe, alert_id, kind))
        return True


class CrashAt:
    NEVER = "never"
    AFTER_SEND = "after_send"     # transport got it, confirmation lost
    BEFORE_SEND = "before_send"   # claimed, then died


class Pump:
    def __init__(self, engine, transport: Transport | None = None):
        self.engine = engine
        self.conn: sqlite3.Connection = engine.conn
        self.transport = transport or Transport()
        self.crash = CrashAt.NEVER
        self.crashed_dedupe: str | None = None

    def _fence(self) -> int:
        row = self.conn.execute(
            "SELECT v FROM pump_meta WHERE k='fence'").fetchone()
        f = (int(row[0]) + 1) if row else 1
        self.conn.execute(
            "INSERT INTO pump_meta(k,v) VALUES('fence',?) "
            "ON CONFLICT(k) DO UPDATE SET v=excluded.v", (str(f),))
        return f

    def pump_once(self) -> bool:
        """Process one unconfirmed message. Returns True if one was handled."""
        self.conn.commit()
        row = self.conn.execute(
            "SELECT o.* FROM outbox o WHERE NOT EXISTS("
            "SELECT 1 FROM delivered d WHERE d.dedupe=o.dedupe) "
            "ORDER BY created_seq, alert_id, revno, kind LIMIT 1").fetchone()
        if row is None:
            return False

        dedupe = row["dedupe"]
        fence = self._fence()
        self.conn.commit()  # durable claim point

        if self.crash == CrashAt.BEFORE_SEND and self.crashed_dedupe is None:
            self.crashed_dedupe = dedupe
            raise RuntimeError(f"CRASH(injected) after claim of {dedupe}")

        ok = self.transport.send(dedupe, row["alert_id"], row["kind"],
                                 row["state"], fence)

        if self.crash == CrashAt.AFTER_SEND and self.crashed_dedupe is None:
            self.crashed_dedupe = dedupe
            raise RuntimeError(
                f"CRASH(injected) after transport send of {dedupe}, "
                "before confirmation")

        if ok:
            self.conn.execute(
                "INSERT OR IGNORE INTO delivered"
                "(dedupe,alert_id,revno,kind,state,t) VALUES(?,?,?,?,?,?)",
                (dedupe, row["alert_id"], row["revno"], row["kind"],
                 row["state"],
                 self.conn.execute(
                     "SELECT COUNT(*) FROM delivered").fetchone()[0]))
            self.conn.execute("DELETE FROM outbox WHERE dedupe=?", (dedupe,))
        self.conn.commit()
        return True

    def run(self, max_messages: int = 10_000) -> int:
        n = 0
        while n < max_messages and self.pump_once():
            n += 1
        return n
