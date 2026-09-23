"""Parse raw change events against the schema effective at their commit_seq."""

from __future__ import annotations

from collections import deque
from typing import Any

from .events import ABSENT, ParsedChange, RawChangeEvent
from .schema import FieldDef, SchemaRegistry, SchemaVersion, UnknownSchemaError


class UnknownFieldError(Exception):
    """Payload carries a field the resolved schema does not define.

    Never guess a mapping: an unrecognized name is an error, not a hint.
    """


class FieldTypeError(Exception):
    """Payload value does not match the declared field type."""


class BufferOverflowError(Exception):
    """The bounded buffer for unknown-schema events is full."""


def _type_ok(value: Any, type_name: str) -> bool:
    if type_name == "any":
        return True
    if type_name == "bool":
        return isinstance(value, bool)
    if type_name == "int":
        return isinstance(value, int) and not isinstance(value, bool)
    if type_name == "float":
        return isinstance(value, (int, float)) and not isinstance(value, bool)
    if type_name == "str":
        return isinstance(value, str)
    raise ValueError(f"unknown field type {type_name!r}")


def _check_type(value: Any, field_def: FieldDef) -> None:
    if not _type_ok(value, field_def.type):
        raise FieldTypeError(
            f"field {field_def.name!r} (id={field_def.field_id}) expects "
            f"{field_def.type}, got {type(value).__name__}: {value!r}"
        )


class BoundedBuffer:
    """Bounded FIFO buffer for events whose schema is not known yet."""

    def __init__(self, max_events: int = 1024) -> None:
        if max_events < 1:
            raise ValueError("max_events must be >= 1")
        self.max_events = max_events
        self._items: deque[RawChangeEvent] = deque()

    def put(self, event: RawChangeEvent) -> None:
        if len(self._items) >= self.max_events:
            raise BufferOverflowError(
                f"buffer full ({self.max_events} events); "
                f"cannot buffer txid={event.txid} table={event.table!r}"
            )
        self._items.append(event)

    def take(self, table: str) -> list[RawChangeEvent]:
        """Remove and return buffered events for table, preserving order."""
        matched = [e for e in self._items if e.table == table]
        self._items = deque(e for e in self._items if e.table != table)
        return matched

    def __len__(self) -> int:
        return len(self._items)


class ChangeParser:
    """Resolves the schema version per event and normalizes to stable ids."""

    def __init__(self, registry: SchemaRegistry, buffer: BoundedBuffer) -> None:
        self.registry = registry
        self.buffer = buffer

    def parse(self, raw: RawChangeEvent) -> ParsedChange:
        schema = self.registry.resolve(raw.table, raw.commit_seq)
        return ParsedChange(
            txid=raw.txid,
            table=raw.table,
            pk=dict(raw.pk),
            commit_seq=raw.commit_seq,
            schema_version=schema.version,
            before=self._normalize(schema, raw.before),
            after=self._normalize(schema, raw.after),
            deleted_field_ids=self.registry.deleted_field_ids(schema),
        )

    def submit(self, raw: RawChangeEvent) -> ParsedChange | None:
        """Parse now, or buffer when the schema is unknown. None if buffered."""
        try:
            return self.parse(raw)
        except UnknownSchemaError:
            self.buffer.put(raw)
            return None

    def on_schema_registered(self, table: str) -> list[ParsedChange]:
        """Replay buffered events for a table after its schema arrives.

        Events still unresolvable (e.g. commit_seq predates every registered
        version) are re-buffered, not guessed.
        """
        parsed: list[ParsedChange] = []
        for raw in self.buffer.take(table):
            try:
                parsed.append(self.parse(raw))
            except UnknownSchemaError:
                self.buffer.put(raw)
        return parsed

    @staticmethod
    def _normalize(
        schema: SchemaVersion, record: dict[str, Any] | None
    ) -> dict[int, Any] | None:
        if record is None:
            return None
        by_name = schema.by_name()
        out: dict[int, Any] = {}
        for name, value in record.items():
            field_def = by_name.get(name)
            if field_def is None:
                raise UnknownFieldError(
                    f"table {schema.table!r} v{schema.version} has no field "
                    f"named {name!r}; refusing to guess"
                )
            if value is not None:
                _check_type(value, field_def)
            out[field_def.field_id] = value
        for field_def in schema.fields:
            out.setdefault(field_def.field_id, ABSENT)
        return out
