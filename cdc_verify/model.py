"""Data model for the verify-before-apply change processor.

Semantics encoded here:

- ``before`` is a *partial* precondition: only the fields it lists are
  validated against the current row. Fields it omits are neither checked
  nor touched.
- ``after`` is a *partial* update: it is merged into the current row, so
  fields it omits keep their current values.
- ``before is None`` means "insert": the row must be absent.
- ``after is None`` means "delete": the row is removed after validation.
- A field that is absent from the current row is NOT equal to null; the
  ``MISSING`` sentinel keeps the two cases distinguishable in evidence.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Optional


class _Missing:
    """Sentinel for 'field absent from the row' (distinct from JSON null)."""

    _instance: Optional["_Missing"] = None

    def __new__(cls) -> "_Missing":
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:
        return "<missing>"

    def __eq__(self, other: object) -> bool:
        return isinstance(other, _Missing)

    def __hash__(self) -> int:
        return hash(_Missing)


MISSING = _Missing()


def encode(value: Any) -> Any:
    """Make a value safe to print/serialize in reports."""
    return "<missing>" if value is MISSING else value


@dataclass(frozen=True)
class Event:
    """One row change inside a transaction.

    Carries exactly what the local fixtures provide: transaction id,
    table, primary key, partial before/after values and commit sequence.
    """

    txn_id: str
    table: str
    pk: str
    commit_seq: int
    before: Optional[dict] = None
    after: Optional[dict] = None

    @property
    def key(self) -> tuple[str, str]:
        return (self.table, self.pk)

    @property
    def fingerprint(self) -> tuple:
        """Identity used for duplicate-commit detection."""
        return (self.txn_id, self.table, self.pk, self.commit_seq)


@dataclass(frozen=True)
class FieldConflict:
    """Minimal per-field evidence: only fields that actually mismatch."""

    field: str
    expected: Any
    actual: Any

    def as_dict(self) -> dict:
        return {
            "field": self.field,
            "expected": encode(self.expected),
            "actual": encode(self.actual),
        }


@dataclass
class ConflictRecord:
    """Why a transaction was rejected; the key becomes quarantined."""

    txn_id: str
    table: str
    pk: str
    commit_seq: int
    kind: str
    fields: list[FieldConflict] = field(default_factory=list)
    detail: str = ""

    @property
    def key(self) -> tuple[str, str]:
        return (self.table, self.pk)

    def as_dict(self) -> dict:
        return {
            "txn_id": self.txn_id,
            "table": self.table,
            "pk": self.pk,
            "commit_seq": self.commit_seq,
            "kind": self.kind,
            "fields": [f.as_dict() for f in self.fields],
            "detail": self.detail,
        }


@dataclass
class Report:
    """Everything the processor decided, in processing order."""

    applied: list[str] = field(default_factory=list)
    duplicates: list[tuple] = field(default_factory=list)
    conflicts: list[ConflictRecord] = field(default_factory=list)
    blocked: list[dict] = field(default_factory=list)
    resolutions: list[dict] = field(default_factory=list)
