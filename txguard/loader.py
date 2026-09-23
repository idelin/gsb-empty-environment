"""本地夹具加载：JSON -> Transaction 列表（按 commit_seq 排序）。"""

from __future__ import annotations

import json
from pathlib import Path

from .models import ChangeOp, Transaction


def load_fixture(path: str | Path) -> list[Transaction]:
    """夹具格式：
    {"transactions": [{"tx_id", "commit_seq",
        "ops": [{"table", "pk", "before": {...}, "after": {...}}, ...]}, ...]}
    """
    data = json.loads(Path(path).read_text(encoding="utf-8"))
    txs = []
    for raw in data["transactions"]:
        ops = tuple(
            ChangeOp(table=o["table"], pk=o["pk"],
                     before=o.get("before", {}), after=o.get("after", {}))
            for o in raw["ops"]
        )
        txs.append(Transaction(tx_id=raw["tx_id"],
                               commit_seq=raw["commit_seq"], ops=ops))
    return sorted(txs, key=lambda t: t.commit_seq)
