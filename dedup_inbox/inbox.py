"""Core implementation of the bounded replay-deduplicating inbox."""

from __future__ import annotations

import os
import sqlite3
from dataclasses import dataclass, field
from typing import Callable, Optional


@dataclass(frozen=True)
class DeliveryResult:
    """Outcome of a single :meth:`BoundedInbox.deliver` call.

    Attributes:
        status: One of ``"committed"`` (contiguous prefix applied),
            ``"cached"`` (held in the bounded out-of-order window),
            ``"duplicate"`` (already applied or already cached),
            ``"rejected"`` (``seq`` is beyond the window).
        sender: Message sender.
        seq: Message sequence number.
        value: Business value carried by the message (``None`` for a
            duplicate/rejected replay).
        committed_seqs: Sequence numbers newly applied to business state by
            this call (more than one when cached messages are flushed).
        cursor: Committed contiguous cursor after the call.
        total: Committed business total for the sender after the call.
        rejected_reason: Explanation when ``status == "rejected"``.
    """

    status: str
    sender: str
    seq: int
    value: Optional[int] = None
    committed_seqs: tuple[int, ...] = field(default_factory=tuple)
    cursor: int = 0
    total: int = 0
    rejected_reason: Optional[str] = None

    def to_dict(self) -> dict:
        data = {
            "status": self.status,
            "sender": self.sender,
            "seq": self.seq,
            "committed_seqs": list(self.committed_seqs),
            "cursor": self.cursor,
            "total": self.total,
        }
        if self.status in ("committed", "cached"):
            data["value"] = self.value
        if self.rejected_reason is not None:
            data["rejected_reason"] = self.rejected_reason
        return data


CrashHook = Callable[[str], None]


def _hard_exit(point: str) -> None:
    """Default crash hook: terminate the process immediately, like power loss."""
    os._exit(99)


class BoundedInbox:
    """Transactional bounded replay-deduplicating inbox backed by SQLite."""

    def __init__(
        self,
        path: str,
        window: int = 3,
        crash_hook: Optional[CrashHook] = None,
        autoflush: bool = True,
    ) -> None:
        if window < 1:
            raise ValueError("window must be >= 1")
        self.path = path
        self.window = window
        self.autoflush = autoflush
        self._crash_hook = crash_hook
        self.conn = sqlite3.connect(path)
        self.conn.execute("PRAGMA journal_mode=DELETE")
        self.conn.execute("PRAGMA synchronous=FULL")
        self.conn.execute("PRAGMA foreign_keys=ON")
        self._init_schema()
        self._check_window()

    # ------------------------------------------------------------------ setup

    def _init_schema(self) -> None:
        self.conn.executescript(
            """
            CREATE TABLE IF NOT EXISTS meta (
                key   TEXT PRIMARY KEY,
                value TEXT NOT NULL
            );

            CREATE TABLE IF NOT EXISTS senders (
                sender  TEXT PRIMARY KEY,
                cursor  INTEGER NOT NULL DEFAULT 0,
                total   INTEGER NOT NULL DEFAULT 0
            );

            CREATE TABLE IF NOT EXISTS pending (
                sender TEXT NOT NULL,
                seq    INTEGER NOT NULL,
                value  INTEGER NOT NULL,
                PRIMARY KEY (sender, seq)
            );

            CREATE TABLE IF NOT EXISTS ledger (
                id     INTEGER PRIMARY KEY AUTOINCREMENT,
                sender TEXT NOT NULL,
                seq    INTEGER NOT NULL,
                value  INTEGER NOT NULL,
                UNIQUE (sender, seq)
            );
            """
        )
        self.conn.commit()

    def _check_window(self) -> None:
        row = self.conn.execute(
            "SELECT value FROM meta WHERE key = 'window'"
        ).fetchone()
        if row is None:
            self.conn.execute(
                "INSERT INTO meta(key, value) VALUES ('window', ?)",
                (str(self.window),),
            )
            self.conn.commit()
        elif int(row[0]) != self.window:
            raise ValueError(
                f"existing inbox was created with window={row[0]}, "
                f"got window={self.window}"
            )

    def close(self) -> None:
        self.conn.close()

    def __enter__(self) -> "BoundedInbox":
        return self

    def __exit__(self, exc_type, exc, tb) -> None:
        self.close()

    # -------------------------------------------------------------- internals

    def _get_sender(self, cur: sqlite3.Cursor, sender: str) -> tuple[int, int]:
        row = cur.execute(
            "SELECT cursor, total FROM senders WHERE sender = ?", (sender,)
        ).fetchone()
        if row is None:
            cur.execute(
                "INSERT INTO senders(sender, cursor, total) VALUES (?, 0, 0)",
                (sender,),
            )
            return 0, 0
        return int(row[0]), int(row[1])

    def _crash(self, point: str) -> None:
        if self._crash_hook is not None:
            self._crash_hook(point)

    # ------------------------------------------------------------- public API

    def deliver(self, sender: str, seq: int, value: int) -> DeliveryResult:
        """Deliver one message; returns a :class:`DeliveryResult`.

        Business state and dedup cursor are persisted in a single transaction.
        """
        if not isinstance(seq, int) or isinstance(seq, bool):
            raise TypeError("seq must be an int")
        if not isinstance(value, int) or isinstance(value, bool):
            raise TypeError("value must be an int")
        if seq < 1:
            raise ValueError("seq must be >= 1 (strictly increasing from 1)")

        self.conn.execute("BEGIN IMMEDIATE")
        try:
            cursor, total = self._get_sender(self.conn.cursor(), sender)

            # Already committed -> idempotent ignore, cursor untouched.
            if seq <= cursor:
                self.conn.commit()
                return DeliveryResult(
                    status="duplicate",
                    sender=sender,
                    seq=seq,
                    cursor=cursor,
                    total=total,
                    rejected_reason=f"seq {seq} <= committed cursor {cursor}",
                )

            # Already held in the window -> idempotent ignore.
            cached = self.conn.execute(
                "SELECT 1 FROM pending WHERE sender = ? AND seq = ?",
                (sender, seq),
            ).fetchone()
            if cached is not None:
                self.conn.commit()
                return DeliveryResult(
                    status="duplicate",
                    sender=sender,
                    seq=seq,
                    value=value,
                    cursor=cursor,
                    total=total,
                    rejected_reason=f"seq {seq} is already cached",
                )

            # Beyond the bounded window -> explicit reject, no state change.
            if seq > cursor + self.window:
                self.conn.commit()
                return DeliveryResult(
                    status="rejected",
                    sender=sender,
                    seq=seq,
                    value=value,
                    cursor=cursor,
                    total=total,
                    rejected_reason=(
                        f"seq {seq} is beyond window W={self.window} "
                        f"of cursor {cursor} (max acceptable {cursor + self.window})"
                    ),
                )

            # Fault injection: before any write for this message.
            self._crash("before_write")

            # Cache inside the window.
            self.conn.execute(
                "INSERT INTO pending(sender, seq, value) VALUES (?, ?, ?)",
                (sender, seq, value),
            )

            # Fault injection: after the write but before commit.
            self._crash("after_write")

            committed_seqs: list[int] = []
            new_cursor, new_total = cursor, total
            if self.autoflush:
                while True:
                    row = self.conn.execute(
                        "SELECT seq, value FROM pending "
                        "WHERE sender = ? AND seq = ?",
                        (sender, new_cursor + 1),
                    ).fetchone()
                    if row is None:
                        break
                    next_seq, next_value = int(row[0]), int(row[1])
                    self.conn.execute(
                        "DELETE FROM pending WHERE sender = ? AND seq = ?",
                        (sender, next_seq),
                    )
                    self.conn.execute(
                        "INSERT INTO ledger(sender, seq, value) VALUES (?, ?, ?)",
                        (sender, next_seq, next_value),
                    )
                    new_cursor = next_seq
                    new_total += next_value
                    committed_seqs.append(next_seq)

                if new_cursor != cursor or new_total != total:
                    self.conn.execute(
                        "UPDATE senders SET cursor = ?, total = ? WHERE sender = ?",
                        (new_cursor, new_total, sender),
                    )

            self.conn.commit()
        except Exception:
            self.conn.rollback()
            raise

        # Fault injection: durable commit succeeded, but the result is lost
        # (e.g. process dies before replying). Retries must be idempotent.
        self._crash("after_commit")

        if committed_seqs:
            return DeliveryResult(
                status="committed",
                sender=sender,
                seq=seq,
                value=value,
                committed_seqs=tuple(committed_seqs),
                cursor=new_cursor,
                total=new_total,
            )
        return DeliveryResult(
            status="cached",
            sender=sender,
            seq=seq,
            value=value,
            cursor=new_cursor,
            total=new_total,
        )

    def state(self, sender: str) -> dict:
        """Return committed cursor/total plus the cached window contents."""
        cur = self.conn.execute(
            "SELECT cursor, total FROM senders WHERE sender = ?", (sender,)
        ).fetchone()
        cursor, total = (int(cur[0]), int(cur[1])) if cur else (0, 0)
        pending = [
            (int(seq), int(value))
            for seq, value in self.conn.execute(
                "SELECT seq, value FROM pending WHERE sender = ? ORDER BY seq",
                (sender,),
            )
        ]
        return {
            "sender": sender,
            "window": self.window,
            "cursor": cursor,
            "total": total,
            "max_acceptable": cursor + self.window,
            "pending": [{"seq": seq, "value": value} for seq, value in pending],
        }
