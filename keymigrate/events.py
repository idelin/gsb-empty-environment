"""CDC change events loaded from local fixtures (no message platform)."""
from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Optional

Key = tuple[str, str]  # (table, primary key)


@dataclass(frozen=True)
class ChangeEvent:
    tx_id: str        # transaction id
    table: str        # source table
    pk: str           # primary key
    before: Optional[dict]  # row image before the change (None for insert)
    after: Optional[dict]   # row image after the change (None for delete)
    commit_seq: int   # commit sequence number (totally ordered)

    @property
    def key(self) -> Key:
        return (self.table, self.pk)

    @classmethod
    def from_dict(cls, raw: dict[str, Any]) -> "ChangeEvent":
        return cls(
            tx_id=raw["tx_id"],
            table=raw["table"],
            pk=raw["pk"],
            before=raw.get("before"),
            after=raw.get("after"),
            commit_seq=int(raw["commit_seq"]),
        )


def load_events(path: str | Path) -> list[ChangeEvent]:
    raw = json.loads(Path(path).read_text(encoding="utf-8"))
    events = [ChangeEvent.from_dict(item) for item in raw]
    events.sort(key=lambda e: e.commit_seq)
    return events
