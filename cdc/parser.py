"""Parse raw change records against the schema version effective at their
commit sequence. Field renames resolve to stable ids; deleted fields are
distinguished from explicit nulls via the DELETED sentinel; unknown schemas
are buffered (bounded) instead of guessed.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from .buffer import BoundedBuffer
from .schema import SchemaRegistry, SchemaVersion


class UnknownSchemaError(Exception):
    """No registered schema version covers the change's commit sequence."""


class UnknownFieldError(Exception):
    """Payload contains a field name not defined in the resolved schema."""


class _Deleted:
    """Singleton sentinel marking a field removed from the after-image.

    Distinct from None, which means an explicit NULL value.
    """

    _instance: "_Deleted | None" = None

    def __new__(cls) -> "_Deleted":
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:
        return "DELETED"

    def __bool__(self) -> bool:
        return False

    def __copy__(self) -> "_Deleted":
        return self

    def __deepcopy__(self, memo: dict) -> "_Deleted":
        return self


DELETED = _Deleted()


@dataclass
class ParsedChange:
    """A change resolved against one schema version.

    ``before``/``after`` map stable field id -> value. ``after`` values may
    be None (explicit null) or DELETED (field absent from the after-image).
    """

    txn_id: str
    table: str
    pk: Any
    commit_seq: int
    schema_version: int
    before: dict[int, Any]
    after: dict[int, Any]


class ChangeParser:
    """Parses raw change dicts using commit-seq-effective schema versions.

    Raw change shape (from local fixtures, no message platform involved):
        {"txn_id": str, "table": str, "pk": any, "commit_seq": int,
         "before": {field_name: value}, "after": {field_name: value}}
    """

    def __init__(self, registry: SchemaRegistry, buffer_capacity: int = 1024):
        self.registry = registry
        self.buffer = BoundedBuffer(buffer_capacity)

    def register_schema(self, version: SchemaVersion) -> list[ParsedChange]:
        """Register a schema version, then retry buffered changes."""
        self.registry.register(version)
        return self._drain_buffer()

    def submit(self, raw: dict) -> ParsedChange | None:
        """Parse one raw change. Returns None if it was buffered because no
        registered schema covers its commit sequence."""
        try:
            return self._parse(raw)
        except UnknownSchemaError:
            self.buffer.add(raw)
            return None

    def _drain_buffer(self) -> list[ParsedChange]:
        parsed: list[ParsedChange] = []
        still_unknown: list[dict] = []
        for raw in self.buffer.take_all():
            try:
                parsed.append(self._parse(raw))
            except UnknownSchemaError:
                still_unknown.append(raw)
        for raw in still_unknown:
            self.buffer.add(raw)
        return parsed

    def _parse(self, raw: dict) -> ParsedChange:
        table = raw["table"]
        commit_seq = raw["commit_seq"]
        version = self.registry.resolve(table, commit_seq)
        if version is None:
            raise UnknownSchemaError(
                f"no registered schema for table={table!r} covering "
                f"commit_seq={commit_seq}"
            )
        by_name = version.by_name
        before = self._map_image(raw.get("before") or {}, by_name, version)
        after = self._map_image(raw.get("after") or {}, by_name, version)
        for stable_id in before:
            if stable_id not in after:
                after[stable_id] = DELETED
        return ParsedChange(
            txn_id=raw["txn_id"],
            table=table,
            pk=raw.get("pk"),
            commit_seq=commit_seq,
            schema_version=version.version,
            before=before,
            after=after,
        )

    @staticmethod
    def _map_image(
        image: dict, by_name: dict, version: SchemaVersion
    ) -> dict[int, Any]:
        mapped: dict[int, Any] = {}
        for name, value in image.items():
            field = by_name.get(name)
            if field is None:
                raise UnknownFieldError(
                    f"field {name!r} not defined in "
                    f"{version.table} v{version.version}; refusing to guess"
                )
            mapped[field.stable_id] = value
        return mapped
