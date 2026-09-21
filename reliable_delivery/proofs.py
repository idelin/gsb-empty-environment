"""Skip proofs and conflict records."""
from __future__ import annotations

from dataclasses import dataclass
from typing import Optional

from .events import Event, range_covers

# Conflict kinds
LATE_EVENT_AFTER_SKIP = "late_event_after_skip"
REVOKE_AFTER_CROSS = "revoke_after_cross"
STALE_EPOCH_EVENT = "stale_epoch_event"


@dataclass
class SkipProof:
    """Authorization to skip a gap range, bound to a source epoch.

    Revocable only while the delivery cursor has not crossed the gap.
    ``crossed_cycle`` records which ring turn the crossing happened on, so
    that the same seq in a later wrap turn is a legitimate new event.
    """

    proof_id: str
    partition: str
    epoch: int
    gap_start: int
    gap_end: int
    revoked: bool = False
    crossed: bool = False
    crossed_cycle: int = -1

    def covers(self, seq: int, modulus: int) -> bool:
        return range_covers(self.gap_start, self.gap_end, seq, modulus)

    def to_dict(self) -> dict:
        return {
            "proof_id": self.proof_id,
            "partition": self.partition,
            "epoch": self.epoch,
            "gap_start": self.gap_start,
            "gap_end": self.gap_end,
            "revoked": self.revoked,
            "crossed": self.crossed,
            "crossed_cycle": self.crossed_cycle,
        }

    @staticmethod
    def from_dict(data: dict) -> "SkipProof":
        return SkipProof(
            proof_id=data["proof_id"],
            partition=data["partition"],
            epoch=data["epoch"],
            gap_start=data["gap_start"],
            gap_end=data["gap_end"],
            revoked=data.get("revoked", False),
            crossed=data.get("crossed", False),
            crossed_cycle=data.get("crossed_cycle", -1),
        )


@dataclass
class Conflict:
    """Explicit conflict requiring the correction flow."""

    conflict_id: str
    partition: str
    kind: str
    detail: str
    event: Optional[Event] = None
    resolved: bool = False
    resolution: Optional[str] = None

    def to_dict(self) -> dict:
        return {
            "conflict_id": self.conflict_id,
            "partition": self.partition,
            "kind": self.kind,
            "detail": self.detail,
            "event": self.event.to_dict() if self.event else None,
            "resolved": self.resolved,
            "resolution": self.resolution,
        }

    @staticmethod
    def from_dict(data: dict) -> "Conflict":
        return Conflict(
            conflict_id=data["conflict_id"],
            partition=data["partition"],
            kind=data["kind"],
            detail=data["detail"],
            event=Event.from_dict(data["event"]) if data.get("event") else None,
            resolved=data.get("resolved", False),
            resolution=data.get("resolution"),
        )
