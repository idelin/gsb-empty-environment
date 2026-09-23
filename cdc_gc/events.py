"""CDC 变更事件模型与本地夹具加载（不接消息平台）。"""
from __future__ import annotations

import json
from dataclasses import dataclass
from enum import Enum
from pathlib import Path
from typing import Any


class Op(Enum):
    INSERT = "insert"
    UPDATE = "update"
    DELETE = "delete"


@dataclass(frozen=True)
class ChangeEvent:
    """一条变更：事务 ID、表、主键、前/后值、提交序号。"""

    tx_id: str
    table: str
    pk: str
    before: dict[str, Any] | None
    after: dict[str, Any] | None
    commit_seq: int

    @property
    def op(self) -> Op:
        if self.before is None and self.after is not None:
            return Op.INSERT
        if self.before is not None and self.after is None:
            return Op.DELETE
        if self.before is not None and self.after is not None:
            return Op.UPDATE
        raise ValueError("before 和 after 不能同时为空")

    @classmethod
    def from_dict(cls, raw: dict[str, Any]) -> "ChangeEvent":
        missing = {"tx_id", "table", "pk", "before", "after", "commit_seq"} - raw.keys()
        if missing:
            raise ValueError(f"缺少字段: {sorted(missing)}")
        seq = raw["commit_seq"]
        if isinstance(seq, bool) or not isinstance(seq, int) or seq <= 0:
            raise ValueError(f"commit_seq 必须为正整数, 得到 {seq!r}")
        for field in ("tx_id", "table", "pk"):
            if not isinstance(raw[field], str) or not raw[field]:
                raise ValueError(f"{field} 必须为非空字符串")
        for field in ("before", "after"):
            if raw[field] is not None and not isinstance(raw[field], dict):
                raise ValueError(f"{field} 必须为对象或 null")
        event = cls(
            tx_id=raw["tx_id"],
            table=raw["table"],
            pk=raw["pk"],
            before=raw["before"],
            after=raw["after"],
            commit_seq=seq,
        )
        event.op  # 触发 before/after 组合校验
        return event


def load_fixture(path: str | Path) -> list[ChangeEvent]:
    """从本地 JSONL 夹具加载变更事件。"""
    events: list[ChangeEvent] = []
    for lineno, line in enumerate(
        Path(path).read_text(encoding="utf-8").splitlines(), start=1
    ):
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        try:
            events.append(ChangeEvent.from_dict(json.loads(line)))
        except (json.JSONDecodeError, ValueError) as exc:
            raise ValueError(f"{path}:{lineno}: {exc}") from exc
    return events
