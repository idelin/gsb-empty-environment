"""Core data model: versioned facts with field-level provenance."""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

WILDCARD = "*"
DATA = "data"
REMARK = "remark"


@dataclass(frozen=True)
class FieldValue:
    """A single field value plus its provenance (where it came from)."""

    value: Any
    source: str


@dataclass(frozen=True)
class ChangeSet:
    """What one version transition changed on a fact."""

    updated: frozenset = frozenset()
    deleted: frozenset = frozenset()
    remarks: frozenset = frozenset()

    @property
    def data_fields(self) -> frozenset:
        return self.updated | self.deleted

    def merge(self, other: "ChangeSet") -> "ChangeSet":
        return ChangeSet(
            updated=self.updated | other.updated,
            deleted=self.deleted | other.deleted,
            remarks=self.remarks | other.remarks,
        )


@dataclass(frozen=True)
class FactSnapshot:
    """One immutable version of a fact. Old versions are never discarded."""

    fact_id: str
    version: int
    fields: dict = field(default_factory=dict)    # name -> FieldValue
    remarks: dict = field(default_factory=dict)   # key -> free-text note
    change: ChangeSet = ChangeSet()               # transition that produced it
