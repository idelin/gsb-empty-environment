"""Core data model: change events (from local fixtures) and corrections."""
from __future__ import annotations

import json
from dataclasses import dataclass, field

GENESIS_VERSION = "V0"


def version_id_for(revision_id: str) -> str:
    """Version ids derive deterministically from revision ids.

    This lets the downstream know *which* correction would produce a missing
    parent version, which is what makes cycle detection among parked
    corrections possible.
    """
    return f"V:{revision_id}"


@dataclass(frozen=True)
class ChangeEvent:
    """A single captured change: txn id, table, primary key, before/after
    images and the commit sequence number. Loaded from local fixtures."""

    txn_id: str
    source: str
    table: str
    pk: str
    op: str  # "insert" | "update" | "delete"
    before: dict | None
    after: dict | None
    commit_seq: int

    @property
    def delta(self) -> int:
        before_amt = (self.before or {}).get("amount", 0)
        after_amt = (self.after or {}).get("amount", 0)
        return after_amt - before_amt

    @staticmethod
    def from_dict(raw: dict) -> "ChangeEvent":
        return ChangeEvent(
            txn_id=raw["txn_id"],
            source=raw["source"],
            table=raw["table"],
            pk=raw["pk"],
            op=raw["op"],
            before=raw.get("before"),
            after=raw.get("after"),
            commit_seq=raw["commit_seq"],
        )


@dataclass
class Correction:
    """A revision to the aggregated result.

    Carries the base version(s) it was computed against (``parents``) and the
    *delta* per table - never an absolute value, so an old-base correction can
    never overwrite a newer result. ``checkpoints`` records up to which commit
    sequence per source this revision includes.
    """

    revision_id: str
    source: str
    parents: tuple[str, ...]
    deltas: dict[str, int] = field(default_factory=dict)
    checkpoints: dict[str, int] = field(default_factory=dict)
    txn_ids: tuple[str, ...] = ()

    @property
    def version_id(self) -> str:
        return version_id_for(self.revision_id)

    def fingerprint(self) -> str:
        canonical = {
            "revision_id": self.revision_id,
            "source": self.source,
            "parents": sorted(self.parents),
            "deltas": dict(sorted(self.deltas.items())),
            "checkpoints": dict(sorted(self.checkpoints.items())),
            "txn_ids": sorted(self.txn_ids),
        }
        return json.dumps(canonical, sort_keys=True)

    def to_dict(self) -> dict:
        return {
            "revision_id": self.revision_id,
            "source": self.source,
            "parents": list(self.parents),
            "deltas": dict(self.deltas),
            "checkpoints": dict(self.checkpoints),
            "txn_ids": list(self.txn_ids),
        }

    @staticmethod
    def from_dict(raw: dict) -> "Correction":
        return Correction(
            revision_id=raw["revision_id"],
            source=raw["source"],
            parents=tuple(raw["parents"]),
            deltas=dict(raw["deltas"]),
            checkpoints=dict(raw["checkpoints"]),
            txn_ids=tuple(raw.get("txn_ids", ())),
        )
