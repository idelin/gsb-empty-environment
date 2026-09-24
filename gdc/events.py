"""变更事件模型。

变更来自本地夹具文件（JSON），每条事件携带：
事务 ID、表名、主键、操作类型、前后值镜像与提交序号。
不接入任何消息平台。
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Mapping, Optional

INSERT = "insert"
DELETE = "delete"
UPDATE = "update"
_VALID_OPS = (INSERT, DELETE, UPDATE)


@dataclass(frozen=True)
class ChangeEvent:
    event_id: str                 # 全局唯一，用于输入去重
    tx_id: str                    # 事务 ID
    table: str                    # 来源表
    pk: tuple                     # 主键（可复合）
    op: str                       # insert / delete / update
    before: Optional[Mapping[str, Any]]  # 前行镜像（insert 为 None）
    after: Optional[Mapping[str, Any]]   # 后行镜像（delete 为 None）
    commit_seq: int               # 提交序号

    @staticmethod
    def from_dict(d: Mapping[str, Any]) -> "ChangeEvent":
        op = d["op"]
        if op not in _VALID_OPS:
            raise ValueError(f"unknown op: {op!r}")
        return ChangeEvent(
            event_id=str(d["event_id"]),
            tx_id=str(d["tx_id"]),
            table=str(d["table"]),
            pk=tuple(d["pk"]),
            op=op,
            before=d.get("before"),
            after=d.get("after"),
            commit_seq=int(d["commit_seq"]),
        )
