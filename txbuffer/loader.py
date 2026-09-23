"""Load event streams from local JSON fixtures (no message platform)."""
from __future__ import annotations

import json
from typing import Iterable

from .buffer import TransactionBuffer


def load_fixture(path: str) -> list[dict]:
    """Read a fixture file: either a bare list of events or an object with
    an ``events`` list (plus an optional ``description``)."""
    with open(path, encoding="utf-8") as fh:
        doc = json.load(fh)
    events = doc["events"] if isinstance(doc, dict) else doc
    if not isinstance(events, list):
        raise ValueError(f"fixture {path} does not contain an event list")
    return events


def feed(buffer: TransactionBuffer, events: Iterable[dict]) -> list[str]:
    """Ingest events one by one, returning the per-event ingest result."""
    return [buffer.ingest(event) for event in events]
