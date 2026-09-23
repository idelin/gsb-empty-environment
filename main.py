"""Demo: parse fixture change events against schema versions.

Run: python3 main.py
"""

from __future__ import annotations

from pathlib import Path

from cdc import (
    ABSENT,
    BoundedBuffer,
    ChangeParser,
    SchemaRegistry,
    UnknownFieldError,
)
from cdc.loader import load_events, load_schemas
from cdc.schema import FieldDef, SchemaVersion

FIXTURES = Path(__file__).parent / "fixtures"


def show_record(record, names):
    if record is None:
        return "<row absent>"
    parts = []
    for field_id in sorted(record):
        value = record[field_id]
        if value is ABSENT:
            rendered = "ABSENT"
        elif value is None:
            rendered = "NULL"
        else:
            rendered = repr(value)
        parts.append(f"#{field_id}({names.get(field_id, '?')})={rendered}")
    return "{" + ", ".join(parts) + "}"


def main() -> None:
    registry = SchemaRegistry()
    for schema in load_schemas(FIXTURES / "schemas.json"):
        registry.register(schema)
    parser = ChangeParser(registry, BoundedBuffer(max_events=8))
    events = load_events(FIXTURES / "events.json")

    print("== registered schemas ==")
    for version in registry.versions("users"):
        names = {f.field_id: f.name for f in version.fields}
        print(f"users v{version.version} effective_from={version.effective_from}: {names}")

    print("\n== parsing events (arrival order) ==")
    for raw in events:
        try:
            parsed = parser.submit(raw)
        except UnknownFieldError as exc:
            print(f"{raw.txid} seq={raw.commit_seq}: REJECTED unknown field: {exc}")
            continue
        if parsed is None:
            print(f"{raw.txid} seq={raw.commit_seq}: schema unknown -> buffered "
                  f"(buffered={len(parser.buffer)})")
            continue
        resolved = registry.resolve(parsed.table, parsed.commit_seq)
        names = {f.field_id: f.name for f in resolved.fields}
        print(f"{parsed.txid} seq={parsed.commit_seq} -> schema v{parsed.schema_version}")
        print(f"   before: {show_record(parsed.before, names)}")
        print(f"   after:  {show_record(parsed.after, names)}")
        if parsed.deleted_field_ids:
            print(f"   deleted field ids at this version: {sorted(parsed.deleted_field_ids)}")

    print("\n== orders schema arrives; replay buffer ==")
    registry.register(SchemaVersion(
        table="orders",
        version=1,
        effective_from=0,
        fields=(
            FieldDef(field_id=1, name="id", type="int"),
            FieldDef(field_id=2, name="total", type="int"),
        ),
    ))
    for parsed in parser.on_schema_registered("orders"):
        names = {1: "id", 2: "total"}
        print(f"{parsed.txid} seq={parsed.commit_seq} -> schema v{parsed.schema_version}")
        print(f"   after:  {show_record(parsed.after, names)}")
    print(f"buffered remaining: {len(parser.buffer)}")


if __name__ == "__main__":
    main()
