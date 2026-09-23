"""目标端表存储：快照装入暂存区，成功后一次性原子发布。"""

from __future__ import annotations

from typing import Any

from .events import OP_DELETE, ChangeEvent


class TableStore:
    def __init__(self) -> None:
        self._tables: dict[str, dict[Any, dict]] = {}

    def load_snapshot_row(self, table: str, pk: Any, row: dict) -> None:
        self._tables.setdefault(table, {})[pk] = dict(row)

    def apply_event(self, event: ChangeEvent) -> None:
        rows = self._tables.setdefault(event.table, {})
        if event.op == OP_DELETE:
            rows.pop(event.pk, None)
        else:
            rows[event.pk] = dict(event.after)

    def publish_from(self, staging: "TableStore") -> None:
        """原子发布：单次引用替换，调用方永远不会看到半新半旧的混合状态。"""
        self._tables = {
            table: dict(rows) for table, rows in staging._tables.items()
        }

    def state(self) -> dict:
        return {table: dict(rows) for table, rows in self._tables.items()}
