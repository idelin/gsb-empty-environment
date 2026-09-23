"""变更事件模型：事务ID、表、主键、前后值、提交序号。"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Optional

OP_INSERT = "insert"
OP_UPDATE = "update"
OP_DELETE = "delete"


@dataclass(frozen=True)
class ChangeEvent:
    txn_id: str
    table: str
    pk: Any
    op: str  # insert / update / delete
    before: Optional[dict]
    after: Optional[dict]
    commit_seq: int
