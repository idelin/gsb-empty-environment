"""Raw and parsed change event models."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any


class _Sentinel:
    """Unique marker; distinct from None so null values stay distinguishable."""

    __slots__ = ("_name",)

    def __init__(self, name: str) -> None:
        self._name = name

    def __repr__(self) -> str:
        return self._name


#: Marks "field defined in the resolved schema but not present in the payload".
#: Explicit JSON null parses to None; ABSENT never equals None.
ABSENT = _Sentinel("ABSENT")


@dataclass(frozen=True)
class RawChangeEvent:
    """A change as read from the local fixture (no message platform involved).

    before/after are dicts keyed by the field names that were in effect
    at commit_seq; None means the row did not exist (insert/delete).
    """

    txid: str
    table: str
    pk: dict[str, Any]
    commit_seq: int
    before: dict[str, Any] | None
    after: dict[str, Any] | None


@dataclass(frozen=True)
class ParsedChange:
    """A change normalized to stable field ids.

    before/after map field_id -> value, where value is:
      - the parsed value,
      - None for an explicit null in the payload,
      - ABSENT when the schema defines the field but the payload omits it.
    deleted_field_ids lists stable ids dropped by this schema version
    relative to the previous one (schema-level deletion, not a null value).
    """

    txid: str
    table: str
    pk: dict[str, Any]
    commit_seq: int
    schema_version: int
    before: dict[int, Any] | None
    after: dict[int, Any] | None
    deleted_field_ids: frozenset[int]
