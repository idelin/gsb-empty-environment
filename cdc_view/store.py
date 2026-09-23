"""本地源库夹具：保存基线行、应用变更事件，可在任意时刻打固定快照。"""

from __future__ import annotations

import threading

from .events import ChangeEvent


class BaseStore:
    def __init__(
        self, tables: dict[str, list[dict]], watermark: int, pk_field: str = "id"
    ) -> None:
        self._pk = pk_field
        self._tables = {
            name: {row[pk_field]: dict(row) for row in rows}
            for name, rows in tables.items()
        }
        self._watermark = watermark
        self._lock = threading.Lock()

    @property
    def watermark(self) -> int:
        with self._lock:
            return self._watermark

    def apply(self, event: ChangeEvent) -> None:
        """把变更落到源库（模拟上游事务提交）。"""
        with self._lock:
            rows = self._tables.setdefault(event.table, {})
            if event.op == "delete":
                rows.pop(event.pk, None)
            else:  # insert / update 都以后值为准
                rows[event.pk] = dict(event.after)
            self._watermark = max(self._watermark, event.commit_seq)

    def snapshot(self) -> tuple[dict[str, list[dict]], int]:
        """固定快照：返回 (表数据深拷贝, 快照水位)，供后台重建使用。"""
        with self._lock:
            tables = {
                name: [dict(row) for row in rows.values()]
                for name, rows in self._tables.items()
            }
            return tables, self._watermark
