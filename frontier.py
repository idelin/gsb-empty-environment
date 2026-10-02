"""Tiered FIFO frontier: bounded in-memory tier + sqlite disk tier.

Ordering guarantee: pops are globally FIFO. Once the disk tier is
non-empty, new pushes spill to disk so that older on-disk items are
always popped before newer in-memory ones. This keeps BFS layer order
exact regardless of the memory limit.
"""
from __future__ import annotations

import os
import pickle
import sqlite3
from collections import deque
from typing import Any, Iterable


class TieredFrontier:
    def __init__(self, memory_limit: int = 4096, disk_path: str | None = None,
                 batch_size: int = 512) -> None:
        self.memory_limit = max(1, memory_limit)
        self.batch_size = max(1, batch_size)
        self._mem: deque = deque()
        self._disk_path = disk_path
        self._conn: sqlite3.Connection | None = None
        self._disk_count = 0
        self.spills = 0

    def _ensure_conn(self) -> sqlite3.Connection:
        if self._conn is None:
            if self._disk_path is None:
                raise RuntimeError("disk tier requested but no disk_path configured")
            self._conn = sqlite3.connect(self._disk_path)
            self._conn.execute(
                "CREATE TABLE IF NOT EXISTS frontier ("
                "id INTEGER PRIMARY KEY AUTOINCREMENT, payload BLOB NOT NULL)"
            )
        return self._conn

    def push(self, item: Any) -> None:
        if self._disk_count == 0 and len(self._mem) < self.memory_limit:
            self._mem.append(item)
            return
        conn = self._ensure_conn()
        conn.execute("INSERT INTO frontier(payload) VALUES (?)", (pickle.dumps(item),))
        self._disk_count += 1
        self.spills += 1
        if self._disk_count % 256 == 0:
            conn.commit()

    def pop(self) -> Any:
        if not self._mem and self._disk_count:
            self._load_batch()
        if not self._mem:
            raise IndexError("pop from empty frontier")
        return self._mem.popleft()

    def _load_batch(self) -> None:
        conn = self._ensure_conn()
        rows = conn.execute(
            "SELECT id, payload FROM frontier ORDER BY id LIMIT ?",
            (self.batch_size,),
        ).fetchall()
        if not rows:
            return
        ids = [row[0] for row in rows]
        conn.execute(
            "DELETE FROM frontier WHERE id IN (%s)" % ",".join("?" * len(ids)), ids
        )
        conn.commit()
        for _, payload in rows:
            self._mem.append(pickle.loads(payload))
        self._disk_count -= len(rows)

    def __len__(self) -> int:
        return len(self._mem) + self._disk_count

    def __bool__(self) -> bool:
        return len(self) > 0

    def drain(self) -> list:
        """Pop everything in FIFO order (used for checkpoints/evidence)."""
        items = []
        while self:
            items.append(self.pop())
        return items

    def extend(self, items: Iterable) -> None:
        for item in items:
            self.push(item)

    def close(self, remove_file: bool = True) -> None:
        if self._conn is not None:
            self._conn.commit()
            self._conn.close()
            self._conn = None
        if remove_file and self._disk_path and os.path.exists(self._disk_path):
            os.unlink(self._disk_path)
