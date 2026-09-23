"""变更事件模型：事务ID、表、主键、前后值、提交序号。"""

from __future__ import annotations

import json
from dataclasses import dataclass, replace
from typing import Any, Optional

OP_INSERT = "insert"
OP_UPDATE = "update"
OP_DELETE = "delete"

_VALID_OPS = frozenset({OP_INSERT, OP_UPDATE, OP_DELETE})


@dataclass(frozen=True)
class ChangeEvent:
    """一条变更记录。

    commit_seq 由源库在提交时分配，全局单调递增；同一事务内的多条
    事件共享同一个 tx_id 与 commit_seq。
    """

    tx_id: str
    table: str
    pk: str
    op: str
    before: Optional[dict[str, Any]]
    after: Optional[dict[str, Any]]
    commit_seq: Optional[int] = None

    def __post_init__(self) -> None:
        if self.op not in _VALID_OPS:
            raise ValueError(f"非法操作类型: {self.op!r}")
        if self.op == OP_DELETE and self.after is not None:
            raise ValueError("delete 事件不应携带 after 值")
        if self.op == OP_INSERT and self.after is None:
            raise ValueError("insert 事件必须携带 after 值")

    def with_seq(self, commit_seq: int) -> "ChangeEvent":
        return replace(self, commit_seq=commit_seq)

    def to_json(self) -> str:
        return json.dumps(
            {
                "tx_id": self.tx_id,
                "table": self.table,
                "pk": self.pk,
                "op": self.op,
                "before": self.before,
                "after": self.after,
                "commit_seq": self.commit_seq,
            },
            ensure_ascii=False,
            sort_keys=True,
        )

    @staticmethod
    def from_json(payload: str) -> "ChangeEvent":
        return ChangeEvent(**json.loads(payload))
