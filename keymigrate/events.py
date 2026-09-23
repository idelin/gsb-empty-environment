"""Change events from the local fixture feed, plus the append-only event log."""
from __future__ import annotations

import json
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Any, Optional

Key = tuple[str, str]  # (table, primary key)


@dataclass(frozen=True)
class ChangeEvent:
    """One committed change: txid, table, pk, before/after images, commit lsn."""

    txid: str
    table: str
    pk: str
    before: Optional[dict[str, Any]]
    after: Optional[dict[str, Any]]
    lsn: int

    @property
    def key(self) -> Key:
        return (self.table, self.pk)

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> "ChangeEvent":
        return cls(
            txid=str(data["txid"]),
            table=str(data["table"]),
            pk=str(data["pk"]),
            before=data.get("before"),
            after=data.get("after"),
            lsn=int(data["lsn"]),
        )


def load_fixture(path: str | Path) -> list[ChangeEvent]:
    """Load change events from a local JSON fixture, ordered by commit lsn."""
    raw = json.loads(Path(path).read_text(encoding="utf-8"))
    events = [ChangeEvent.from_dict(item) for item in raw]
    events.sort(key=lambda e: (e.lsn, e.txid))
    return events


class EventLog:
    """Append-only log of routing/transfer records; optionally persisted as JSONL."""

    def __init__(self, path: str | Path | None = None):
        self.path = Path(path) if path else None
        self.records: list[dict[str, Any]] = []
        if self.path:
            self.path.write_text("", encoding="utf-8")

    def append(self, record: dict[str, Any]) -> None:
        self.records.append(record)
        if self.path:
            with self.path.open("a", encoding="utf-8") as fh:
                fh.write(json.dumps(record, sort_keys=True) + "\n")

    @staticmethod
    def read(path: str | Path) -> list[dict[str, Any]]:
        return [
            json.loads(line)
            for line in Path(path).read_text(encoding="utf-8").splitlines()
            if line.strip()
        ]
