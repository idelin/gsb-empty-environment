"""Change events sourced from local fixtures (no messaging platform involved).

Each event carries: transaction id, table, primary key, before/after row
images and a commit sequence number assigned by the source database.
"""

from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path


@dataclass(frozen=True)
class ChangeEvent:
    txn_id: str
    table: str
    pk: str
    before: dict | None  # None -> row did not exist (insert)
    after: dict | None   # None -> row was removed (retraction/delete)
    commit_seq: int

    @property
    def kind(self) -> str:
        if self.before is None and self.after is not None:
            return "insert"
        if self.before is not None and self.after is None:
            return "delete"
        return "update"


def load_change_fixture(path: str | Path) -> list[ChangeEvent]:
    """Load events from a JSON fixture, returned in commit order."""
    data = json.loads(Path(path).read_text(encoding="utf-8"))
    events = [ChangeEvent(**item) for item in data["events"]]
    return sorted(events, key=lambda e: e.commit_seq)
