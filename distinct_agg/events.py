"""本地夹具变更事件：事务ID、表、主键、前后值、提交序号。不接消息平台。"""
from __future__ import annotations

import json
from dataclasses import dataclass
from typing import Any, Iterator, Optional


@dataclass(frozen=True)
class ChangeEvent:
    txn_id: str
    table: str
    pk: str
    before: Optional[dict]  # None => INSERT
    after: Optional[dict]   # None => DELETE
    commit_seq: int

    @property
    def event_id(self) -> tuple:
        return (self.txn_id, self.table, self.pk, self.commit_seq)

    @property
    def kind(self) -> str:
        if self.before is None:
            return "INSERT"
        if self.after is None:
            return "DELETE"
        return "UPDATE"

    @classmethod
    def from_dict(cls, raw: dict) -> "ChangeEvent":
        missing = {"txn_id", "table", "pk", "commit_seq"} - raw.keys()
        if missing:
            raise ValueError(f"event missing fields: {sorted(missing)}")
        if "before" not in raw or "after" not in raw:
            raise ValueError("event must carry explicit before/after (null allowed)")
        return cls(
            txn_id=str(raw["txn_id"]),
            table=str(raw["table"]),
            pk=str(raw["pk"]),
            before=raw["before"],
            after=raw["after"],
            commit_seq=int(raw["commit_seq"]),
        )


def load_events(path: str) -> list[ChangeEvent]:
    events: list[ChangeEvent] = []
    with open(path, "r", encoding="utf-8") as fh:
        for lineno, line in enumerate(fh, 1):
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            try:
                events.append(ChangeEvent.from_dict(json.loads(line)))
            except (ValueError, TypeError) as exc:
                raise ValueError(f"{path}:{lineno}: bad event: {exc}") from exc
    return events


def dump_events(events: Iterator[ChangeEvent], path: str) -> None:
    with open(path, "w", encoding="utf-8") as fh:
        for e in events:
            fh.write(json.dumps({
                "txn_id": e.txn_id, "table": e.table, "pk": e.pk,
                "before": e.before, "after": e.after,
                "commit_seq": e.commit_seq,
            }, ensure_ascii=False) + "\n")
