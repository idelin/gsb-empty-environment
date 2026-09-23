"""Domain model: a local change fixture and the output it produces."""

from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from .errors import PipelineError


@dataclass(frozen=True, slots=True)
class Change:
    """One row change from a local fixture.

    ``before`` is ``None`` for an insert; ``after`` is ``None`` for a delete.
    ``commit_seq`` is the global, gap-free logical commit order; every
    transaction carries one strictly increasing commit sequence.
    """

    txn_id: str
    table: str
    key: str
    before: dict[str, Any] | None
    after: dict[str, Any] | None
    commit_seq: int

    @classmethod
    def from_dict(cls, raw: dict[str, Any]) -> "Change":
        required = ("txn_id", "table", "key", "before", "after", "commit_seq")
        missing = [name for name in required if name not in raw]
        if missing:
            raise PipelineError(f"change is missing fields: {', '.join(missing)}")
        txn_id = raw["txn_id"]
        table = raw["table"]
        key = raw["key"]
        commit_seq = raw["commit_seq"]
        before = raw["before"]
        after = raw["after"]
        if not isinstance(txn_id, str) or not txn_id:
            raise PipelineError("txn_id must be a non-empty string")
        if not isinstance(table, str) or not table:
            raise PipelineError("table must be a non-empty string")
        if not isinstance(key, str) or not key:
            raise PipelineError("key must be a non-empty string")
        if not isinstance(commit_seq, int) or isinstance(commit_seq, bool):
            raise PipelineError("commit_seq must be an integer")
        if commit_seq < 1:
            raise PipelineError("commit_seq must start at 1")
        if before is not None and not isinstance(before, dict):
            raise PipelineError("before must be an object or null")
        if after is not None and not isinstance(after, dict):
            raise PipelineError("after must be an object or null")
        if before is None and after is None:
            raise PipelineError("before and after cannot both be null")
        return cls(
            txn_id=txn_id,
            table=table,
            key=key,
            before=before,
            after=after,
            commit_seq=commit_seq,
        )

    def kind(self) -> str:
        if self.before is None:
            return "insert"
        if self.after is None:
            return "delete"
        return "update"


@dataclass(frozen=True, slots=True)
class Output:
    """A business output promised to the output side by one input change."""

    uid: str
    idem_id: str
    event_type: str
    payload: dict[str, Any]


def change_uid(change: Change) -> str:
    """Stable logical identity of a change (transaction + table + key)."""
    return f"{change.txn_id}:{change.table}:{change.key}"


def output_id(change: Change) -> str:
    """Deterministic output-side idempotency id for a change.

    Including ``commit_seq`` keeps the id self-contained and unique even when a
    key is touched by multiple transactions.
    """
    raw = f"seq={change.commit_seq}|uid={change_uid(change)}"
    digest = hashlib.sha256(raw.encode("utf-8")).hexdigest()[:16]
    return f"out-{change.commit_seq:010d}-{digest}"


def load_changes(path: str | Path) -> list[Change]:
    """Load a fixture file::

        {"changes": [ {txn_id, table, key, before, after, commit_seq}, ... ]}

    Rows are returned sorted by ``commit_seq`` then logical identity.
    """
    data = json.loads(Path(path).read_text(encoding="utf-8"))
    if not isinstance(data, dict) or not isinstance(data.get("changes"), list):
        raise PipelineError("fixture must be an object with a 'changes' list")
    changes = [Change.from_dict(row) for row in data["changes"]]
    changes.sort(key=lambda c: (c.commit_seq, change_uid(c)))
    seen: set[tuple[int, str]] = set()
    for change in changes:
        marker = (change.commit_seq, change_uid(change))
        if marker in seen:
            raise PipelineError(
                f"duplicate change in fixture: seq={change.commit_seq} "
                f"uid={change_uid(change)}"
            )
        seen.add(marker)
    return changes
