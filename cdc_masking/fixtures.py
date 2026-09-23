"""Load local JSON fixtures: schema, policies and change events."""
from __future__ import annotations

import json

from .engine import ChangeEvent
from .policy import (
    FieldPolicy,
    ForeignKey,
    PolicyVersion,
    SchemaRegistry,
    TablePolicy,
)


def _load_json(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def load_schema(path):
    tables = {
        table: {name: {"sensitive": bool(meta.get("sensitive"))}
                for name, meta in fields.items()}
        for table, fields in _load_json(path).items()
    }
    return SchemaRegistry(tables)


def load_policy(path):
    data = _load_json(path)
    tables = {}
    for table, spec in data["tables"].items():
        tables[table] = TablePolicy(
            primary_key=tuple(spec["primary_key"]),
            fields={name: FieldPolicy(kind) for name, kind in spec["fields"].items()},
            foreign_keys=tuple(ForeignKey(**fk) for fk in spec.get("foreign_keys", [])),
        )
    return PolicyVersion(data["version"], tables)


def load_events(path):
    return [ChangeEvent.from_dict(item) for item in _load_json(path)]
