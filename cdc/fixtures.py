"""Load schema versions and change records from local fixture files."""

from __future__ import annotations

import json
from pathlib import Path

from .schema import FieldDef, SchemaVersion


def load_schema_versions(path: str | Path) -> list[SchemaVersion]:
    entries = json.loads(Path(path).read_text(encoding="utf-8"))
    return [
        SchemaVersion(
            table=entry["table"],
            version=entry["version"],
            effective_from=entry["effective_from"],
            fields=tuple(
                FieldDef(
                    stable_id=f["id"], name=f["name"], type=f["type"]
                )
                for f in entry["fields"]
            ),
        )
        for entry in entries
    ]


def load_changes(path: str | Path) -> list[dict]:
    return json.loads(Path(path).read_text(encoding="utf-8"))
