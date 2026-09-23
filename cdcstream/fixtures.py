"""Loading of change fixtures from local JSON files.

No message platform is involved: changes are declared in a local fixture
file with transaction id, table, primary key, before/after values and a
commit sequence number.  The loader assigns event ids in file order and
fills in ``tx_size`` so every event knows how large its transaction is.
"""

from __future__ import annotations

import json
from dataclasses import replace
from pathlib import Path

from .model import ChangeEvent

_REQUIRED = ("tx_id", "table", "pk", "before", "after", "commit_seq")


class FixtureError(ValueError):
    """Raised when a fixture file is malformed."""


def load_events(path: str | Path) -> list[ChangeEvent]:
    """Load change events from a local JSON fixture file."""
    raw = json.loads(Path(path).read_text(encoding="utf-8"))
    changes = raw.get("changes")
    if not isinstance(changes, list) or not changes:
        raise FixtureError("fixture must contain a non-empty 'changes' list")

    events: list[ChangeEvent] = []
    for index, entry in enumerate(changes):
        missing = [k for k in _REQUIRED if k not in entry]
        if missing:
            raise FixtureError(f"change #{index} misses keys: {missing}")
        events.append(
            ChangeEvent(
                event_id=index + 1,
                tx_id=int(entry["tx_id"]),
                table=str(entry["table"]),
                pk=str(entry["pk"]),
                before=entry["before"],
                after=entry["after"],
                commit_seq=int(entry["commit_seq"]),
            )
        )

    sizes: dict[int, int] = {}
    for ev in events:
        sizes[ev.tx_id] = sizes.get(ev.tx_id, 0) + 1
    return [replace(ev, tx_size=sizes[ev.tx_id]) for ev in events]


def group_transactions(events: list[ChangeEvent]) -> list[list[ChangeEvent]]:
    """Group events into transactions ordered by commit sequence."""
    txs: dict[int, list[ChangeEvent]] = {}
    for ev in events:
        txs.setdefault(ev.tx_id, []).append(ev)
    ordered = [sorted(tx, key=lambda e: e.commit_seq) for tx in txs.values()]
    ordered.sort(key=lambda tx: tx[0].commit_seq)
    return ordered
