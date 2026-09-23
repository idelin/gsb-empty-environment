"""Load schema versions and change events from local JSON fixtures."""

from __future__ import annotations

import json
from pathlib import Path

from .events import RawChangeEvent
from .schema import FieldDef, SchemaVersion


def load_schemas(path: str | Path) -> list[SchemaVersion]:
    raw = json.loads(Path(path).read_text(encoding="utf-8"))
    return [
        SchemaVersion(
            table=item["table"],
            version=item["version"],
            effective_from=item["effective_from"],
            fields=tuple(
                FieldDef(field_id=f["id"], name=f["name"], type=f.get("type", "any"))
                for f in item["fields"]
            ),
        )
        for item in raw
    ]


def load_events(path: str | Path) -> list[RawChangeEvent]:
    raw = json.loads(Path(path).read_text(encoding="utf-8"))
    return [
        RawChangeEvent(
            txid=item["txid"],
            table=item["table"],
            pk=item["pk"],
            commit_seq=item["commit_seq"],
            before=item["before"],
            after=item["after"],
        )
        for item in raw
    ]
