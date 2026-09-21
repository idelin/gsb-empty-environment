"""Shared data model for the comparator and the independent verifier.

This module only holds declarative data structures and the evidence value
encoding; it contains no comparison logic.
"""

from __future__ import annotations

import enum
from dataclasses import dataclass
from decimal import Decimal
from typing import Any

EVIDENCE_FORMAT = "reprodiff-evidence/1"

# Integers beyond this magnitude are stored as decimal strings so that the
# evidence JSON never routes them through binary floating point.
_SAFE_INT = 2**53


class Verdict(enum.Enum):
    """Outcome of a comparison."""

    IDENTICAL = "identical"        # raw inputs are identical
    EQUIVALENT = "equivalent"      # only rule-allowed differences exist
    DIFFERENT = "different"        # substantive differences exist
    UNDETERMINED = "undetermined"  # comparison could not be completed


@dataclass(frozen=True)
class Limits:
    """Safety budgets for archive recursion."""

    max_depth: int = 4
    max_bytes: int = 64 * 1024 * 1024
    max_entries: int = 10000

    def to_dict(self) -> dict:
        return {
            "max_depth": self.max_depth,
            "max_bytes": self.max_bytes,
            "max_entries": self.max_entries,
        }

    @classmethod
    def from_dict(cls, data: dict) -> "Limits":
        return cls(
            max_depth=int(data["max_depth"]),
            max_bytes=int(data["max_bytes"]),
            max_entries=int(data["max_entries"]),
        )


class _Missing:
    def __repr__(self) -> str:
        return "MISSING"


MISSING = _Missing()


@dataclass
class Difference:
    """A substantive difference."""

    path: str
    kind: str
    detail: dict

    def to_dict(self) -> dict:
        return {"path": self.path, "kind": self.kind, "detail": self.detail}


@dataclass
class IgnoredRecord:
    """One rule-allowed difference, preserved as evidence."""

    rule_id: str
    path: str
    field_path: str
    kind: str
    old: Any
    new: Any
    old_digest: str
    new_digest: str

    def to_dict(self) -> dict:
        return {
            "rule_id": self.rule_id,
            "path": self.path,
            "field_path": self.field_path,
            "kind": self.kind,
            "old": self.old,
            "new": self.new,
            "old_digest": self.old_digest,
            "new_digest": self.new_digest,
        }


@dataclass
class Undetermined:
    """A location where the comparison could not reach a conclusion."""

    path: str
    reason: str

    def to_dict(self) -> dict:
        return {"path": self.path, "reason": self.reason}


def encode_value(value: Any) -> Any:
    """Encode a compared value into evidence-safe JSON form.

    Numbers never pass through binary floating point: large integers and
    decimals are stored as their exact decimal strings.
    """
    if value is MISSING:
        return {"@type": "missing"}
    if value is None or isinstance(value, (bool, str)):
        return value
    if isinstance(value, int):
        if -_SAFE_INT <= value <= _SAFE_INT:
            return value
        return {"@type": "bigint", "value": str(value)}
    if isinstance(value, Decimal):
        return {"@type": "decimal", "value": str(value)}
    if isinstance(value, list):
        return [encode_value(item) for item in value]
    if isinstance(value, dict):
        return {key: encode_value(item) for key, item in value.items()}
    raise TypeError(f"cannot encode value of type {type(value).__name__}")


def render_key_path(key_path: tuple) -> str:
    """Render a JSON key path such as ("a", 0, "b") as ``a[0].b``."""
    out = ""
    for elem in key_path:
        if isinstance(elem, int):
            out += f"[{elem}]"
        else:
            out += ("." if out else "") + elem
    return out
