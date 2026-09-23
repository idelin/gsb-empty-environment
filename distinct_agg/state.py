"""可溢写状态存储：内存缓存有界，超出后按 FIFO 溢写到 sqlite。"""
from __future__ import annotations

import sqlite3
from typing import Iterator, Optional, Tuple


class SpillMap:
    """str->str 映射。缓存中的条目为权威值；溢写条目落 sqlite。

    - get: 先查缓存，再查删除标记，最后查 sqlite。
    - put: 写缓存，超过 max_cached 时把最老条目溢写到 sqlite。
    - delete: 缓存移除 + 记录墓碑，flush 时同步删除 sqlite 行。
    """

    def __init__(self, conn: sqlite3.Connection, table: str, max_cached: int = 10000):
        self._conn = conn
        self._table = table
        self._max = max(1, max_cached)
        self._cache: dict[str, str] = {}
        self._deleted: set[str] = set()
        conn.execute(
            f"CREATE TABLE IF NOT EXISTS {table} (k TEXT PRIMARY KEY, v TEXT)"
        )
        conn.commit()

    def get(self, key: str, default: Optional[str] = None) -> Optional[str]:
        if key in self._cache:
            return self._cache[key]
        # 缓存命中（权威）
        if key in self._deleted:
            return default
        row = self._conn.execute(
            f"SELECT v FROM {self._table} WHERE k = ?", (key,)
        ).fetchone()
        return row[0] if row else default

    def put(self, key: str, value: str) -> None:
        self._cache[key] = value
        self._deleted.discard(key)
        while len(self._cache) > self._max:
            self._evict_oldest()

    def delete(self, key: str) -> None:
        self._cache.pop(key, None)
        self._deleted.add(key)

    def _evict_oldest(self) -> None:
        key = next(iter(self._cache))
        value = self._cache.pop(key)
        self._conn.execute(
            f"INSERT OR REPLACE INTO {self._table} (k, v) VALUES (?, ?)",
            (key, value),
        )

    def flush(self) -> None:
        with self._conn:
            for key in self._deleted:
                self._conn.execute(f"DELETE FROM {self._table} WHERE k = ?", (key,))
            for key, value in self._cache.items():
                self._conn.execute(
                    f"INSERT OR REPLACE INTO {self._table} (k, v) VALUES (?, ?)",
                    (key, value),
                )
        self._deleted.clear()

    def items(self) -> Iterator[Tuple[str, str]]:
        merged = dict(
            self._conn.execute(f"SELECT k, v FROM {self._table}").fetchall()
        )
        for key in self._deleted:
            merged.pop(key, None)
        merged.update(self._cache)
        return iter(merged.items())

    def __len__(self) -> int:
        return sum(1 for _ in self.items())
