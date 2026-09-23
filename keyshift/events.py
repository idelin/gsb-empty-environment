"""本地夹具事件：事务 ID、表、主键前后值与提交序号。不接入任何消息平台。"""
from __future__ import annotations

import json
from dataclasses import dataclass

REQUIRED_FIELDS = ("tx_id", "table", "old_key", "new_key", "commit_seq")


@dataclass(frozen=True)
class Event:
    tx_id: str
    table: str
    old_key: object
    new_key: object
    commit_seq: int


def load_fixture(path):
    """从本地 JSON 夹具加载事件流。

    接受 {"events": [...]} 或顶层数组；逐条校验必需字段。
    """
    with open(path, "r", encoding="utf-8") as fh:
        payload = json.load(fh)
    raw_events = payload["events"] if isinstance(payload, dict) else payload
    events = []
    for pos, raw in enumerate(raw_events):
        missing = [f for f in REQUIRED_FIELDS if f not in raw]
        if missing:
            raise ValueError(
                f"{path}: event #{pos} missing fields {missing}")
        seq = raw["commit_seq"]
        if not isinstance(seq, int) or seq < 0:
            raise ValueError(f"{path}: event #{pos} bad commit_seq {seq!r}")
        events.append(Event(
            tx_id=str(raw["tx_id"]),
            table=str(raw["table"]),
            old_key=raw["old_key"],
            new_key=raw["new_key"],
            commit_seq=seq,
        ))
    return events
