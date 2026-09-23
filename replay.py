"""Replay a local change fixture (no message platform involved).

The fixture is a JSON document:
    {
      "modulus": 1000000007,
      "events": [
        {"tx_id": "...", "table": "...", "pk": "...", "commit_seq": 1,
         "before": null | {"group", "sort_key", "matrix"},
         "after":  null | {"group", "sort_key", "matrix"}}
      ]
    }
Events are applied in commit_seq order. After every event the incremental
tree aggregate of each affected group is checked against a full ordered
scan, so any divergence is caught at the exact event that caused it.
"""

from __future__ import annotations

import json

from store import ChangeEvent, GroupStore


def load_fixture(path: str):
    with open(path, "r", encoding="utf-8") as fh:
        data = json.load(fh)
    events = [ChangeEvent(**raw) for raw in data["events"]]
    events.sort(key=lambda e: e.commit_seq)
    return data["modulus"], events


class VerificationError(AssertionError):
    pass


def replay(modulus: int, events, verify: bool = True):
    """Apply events; return (store, history) where history maps each event
    to the affected groups and their post-event incremental products."""
    store = GroupStore(modulus)
    history = []
    for event in events:
        store.apply(event)
        affected = {
            side["group"] for side in (event.before, event.after) if side is not None
        }
        products = {}
        for group in sorted(affected):
            incremental = store.product(group)
            if verify:
                scanned = store.full_scan_product(group)
                if incremental != scanned:
                    raise VerificationError(
                        f"commit_seq={event.commit_seq} tx={event.tx_id} "
                        f"group={group!r}: incremental {incremental} != full scan {scanned}"
                    )
            products[group] = incremental
        history.append((event, products))
    return store, history
