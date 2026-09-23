"""CDC 事件模型与本地夹具加载（不接消息平台）。"""

from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Optional


@dataclass(frozen=True)
class ChangeEvent:
    """一条变更：事务 ID、表、主键、前后值、提交序号。"""

    txn_id: str
    table: str
    pk: Any
    before: Optional[dict]
    after: Optional[dict]
    commit_seq: int

    @property
    def op(self) -> str:
        if self.before is None:
            return "insert"
        if self.after is None:
            return "delete"
        return "update"

    @property
    def dedup_key(self) -> tuple:
        """幂等键：同一事务内同一主键至多一条变更。"""
        return (self.txn_id, self.table, self.pk)


def load_events(path: str | Path) -> list[ChangeEvent]:
    """从 JSON 夹具加载变更流，并按提交序号排序。"""
    raw = json.loads(Path(path).read_text(encoding="utf-8"))
    events = [
        ChangeEvent(
            txn_id=item["txn_id"],
            table=item["table"],
            pk=item["pk"],
            before=item.get("before"),
            after=item.get("after"),
            commit_seq=int(item["commit_seq"]),
        )
        for item in raw
    ]
    events.sort(key=lambda event: event.commit_seq)
    return events


def load_snapshot(path: str | Path) -> tuple[dict[str, list[dict]], int]:
    """从 JSON 夹具加载基线快照，返回 (表数据, 快照水位)。"""
    raw = json.loads(Path(path).read_text(encoding="utf-8"))
    tables = {
        name: [dict(row) for row in rows] for name, rows in raw["tables"].items()
    }
    return tables, int(raw["watermark"])
