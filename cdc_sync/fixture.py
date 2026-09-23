"""本地变更夹具：从 JSON 文件加载变更事件，不接任何消息平台。

夹具格式：
{
  "pre_snapshot": [ {"tx_id": ..., "table": ..., "pk": ..., "op": ...,
                      "before": ..., "after": ...}, ... ],
  "during_scan":  [ ... ]
}
同一 tx_id 的连续事件视为一个事务，提交时由源库统一分配 commit_seq。
"""

from __future__ import annotations

import json
from pathlib import Path

from .models import ChangeEvent


def load_fixture(path: str | Path) -> dict[str, list[ChangeEvent]]:
    raw = json.loads(Path(path).read_text(encoding="utf-8"))
    return {
        phase: [ChangeEvent(**item) for item in items]
        for phase, items in raw.items()
    }


def group_transactions(events: list[ChangeEvent]) -> list[list[ChangeEvent]]:
    """把连续的同 tx_id 事件归并为一个事务。"""
    groups: list[list[ChangeEvent]] = []
    for event in events:
        if groups and groups[-1][0].tx_id == event.tx_id:
            groups[-1].append(event)
        else:
            groups.append([event])
    return groups
