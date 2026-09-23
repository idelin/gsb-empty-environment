"""Change records replayed from a local fixture.

Each change describes one primary-key row of one table within one
transaction:  an INSERT has only an after-image, a DELETE only a
before-image and an UPDATE both.  No messaging platform is involved.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any


@dataclass(frozen=True, slots=True)
class Change:
    """A single row change inside a committed transaction."""

    txn_id: str
    table: str
    pk: tuple[Any, ...]
    before: dict[str, Any] | None
    after: dict[str, Any] | None
    commit_seq: int

    def __post_init__(self) -> None:
        if self.table not in ("left", "right"):
            raise ValueError(f"unknown table: {self.table!r}")
        if self.before is None and self.after is None:
            raise ValueError("a change must carry a before- or after-image")
        if self.commit_seq < 0:
            raise ValueError("commit_seq must be non-negative")

    @property
    def op(self) -> str:
        if self.before is None:
            return "insert"
        if self.after is None:
            return "delete"
        return "update"

    @classmethod
    def from_dict(cls, raw: dict[str, Any]) -> "Change":
        try:
            txn_id = str(raw["txn_id"])
            table = str(raw["table"])
            pk = tuple(raw["pk"]) if isinstance(raw["pk"], (list, tuple)) else (raw["pk"],)
            before = raw.get("before")
            after = raw.get("after")
            commit_seq = int(raw["commit_seq"])
        except (KeyError, TypeError, ValueError) as exc:
            raise ValueError(f"invalid change record {raw!r}: {exc}") from exc
        return cls(txn_id, table, pk, before, after, commit_seq)
