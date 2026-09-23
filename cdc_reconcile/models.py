"""CDC 变更事件模型。

变更来自本地夹具文件，每条事件包含：事务 ID、表、主键、前后值、提交序号。
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Optional


@dataclass(frozen=True)
class ChangeEvent:
    commit_seq: int          # 提交序号（源端全局唯一、单调递增）
    txn_id: str              # 事务 ID
    table: str               # 表名
    pk: str                  # 主键
    before: Optional[dict]   # 变更前值（插入为 None）
    after: Optional[dict]    # 变更后值（删除为 None）

    @property
    def entity_key(self) -> tuple[str, str]:
        return (self.table, self.pk)

    @classmethod
    def from_dict(cls, raw: dict[str, Any]) -> "ChangeEvent":
        return cls(
            commit_seq=int(raw["commit_seq"]),
            txn_id=str(raw["txn_id"]),
            table=str(raw["table"]),
            pk=str(raw["pk"]),
            before=raw.get("before"),
            after=raw.get("after"),
        )
