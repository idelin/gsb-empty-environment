"""Event model and validation for the transaction buffer.

Every event carries a global sequence number (``seq``) and a transaction
id (``tx_id``). Change events additionally carry the target table, primary
key, operation and before/after values. Commit markers carry the number of
changes the transaction is expected to contain so an early commit marker
can wait for the complete change list.
"""
from __future__ import annotations

import json
from dataclasses import dataclass
from typing import Any, Optional

CHANGE = "change"
COMMIT = "commit"
ROLLBACK = "rollback"

_KINDS = (CHANGE, COMMIT, ROLLBACK)
_OPS = ("insert", "update", "delete")


@dataclass(frozen=True)
class Event:
    kind: str
    seq: int
    tx_id: str
    table: Optional[str] = None
    pk: Optional[str] = None
    op: Optional[str] = None
    before: Any = None
    after: Any = None
    expected_changes: Optional[int] = None

    def to_dict(self) -> dict:
        raw: dict[str, Any] = {"kind": self.kind, "seq": self.seq, "tx_id": self.tx_id}
        if self.kind == CHANGE:
            raw.update(
                table=self.table,
                pk=self.pk,
                op=self.op,
                before=self.before,
                after=self.after,
            )
        elif self.kind == COMMIT:
            raw["expected_changes"] = self.expected_changes
        return raw


def _check_seq(value: Any) -> int:
    if not isinstance(value, int) or isinstance(value, bool) or value < 1:
        raise ValueError("seq must be a positive integer")
    return value


def _check_tx_id(value: Any) -> str:
    if not isinstance(value, str) or not value:
        raise ValueError("tx_id must be a non-empty string")
    return value


def parse_event(raw: Any) -> Event:
    """Validate a raw JSON-decoded mapping and return an :class:`Event`."""
    if not isinstance(raw, dict):
        raise ValueError("event must be a JSON object")
    kind = raw.get("kind")
    if kind not in _KINDS:
        raise ValueError(f"unknown event kind: {kind!r}")
    seq = _check_seq(raw.get("seq"))
    tx_id = _check_tx_id(raw.get("tx_id"))

    if kind == CHANGE:
        table = raw.get("table")
        if not isinstance(table, str) or not table:
            raise ValueError("change event requires a non-empty 'table'")
        pk = raw.get("pk")
        if pk is None or (isinstance(pk, str) and not pk):
            raise ValueError("change event requires a primary key 'pk'")
        op = raw.get("op")
        if op not in _OPS:
            raise ValueError(f"unknown op: {op!r}")
        if op in ("insert", "update") and "after" not in raw:
            raise ValueError(f"{op} requires an 'after' value")
        if op == "delete" and "before" not in raw:
            raise ValueError("delete requires a 'before' value")
        return Event(
            kind=kind,
            seq=seq,
            tx_id=tx_id,
            table=table,
            pk=str(pk),
            op=op,
            before=raw.get("before"),
            after=raw.get("after"),
        )

    if kind == COMMIT:
        expected = raw.get("expected_changes")
        if not isinstance(expected, int) or isinstance(expected, bool) or expected < 0:
            raise ValueError("commit requires 'expected_changes' >= 0")
        return Event(kind=kind, seq=seq, tx_id=tx_id, expected_changes=expected)

    return Event(kind=kind, seq=seq, tx_id=tx_id)


def event_from_payload(payload: str) -> Event:
    return parse_event(json.loads(payload))
