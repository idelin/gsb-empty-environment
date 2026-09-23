"""Revision model.

A revision is a correction to the aggregate output: it records, per
aggregate key, the base version it was computed against plus the delta to
apply. Because one revision may touch several keys (and thus have several
base versions), version dependencies form a DAG, not a single chain.
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Revision:
    id: str
    txn_id: str
    bases: dict[str, str | None]   # agg_key -> base revision id (None = genesis)
    deltas: dict[str, float]       # agg_key -> signed delta
    seq_lo: int                    # lowest commit seq covered
    seq_hi: int                    # highest commit seq covered

    @property
    def parents(self) -> frozenset[str]:
        return frozenset(b for b in self.bases.values() if b is not None)

    def to_dict(self) -> dict:
        return {
            "id": self.id,
            "txn_id": self.txn_id,
            "bases": dict(self.bases),
            "deltas": dict(self.deltas),
            "seq_lo": self.seq_lo,
            "seq_hi": self.seq_hi,
        }

    @staticmethod
    def from_dict(data: dict) -> "Revision":
        return Revision(
            id=data["id"],
            txn_id=data["txn_id"],
            bases=dict(data["bases"]),
            deltas=dict(data["deltas"]),
            seq_lo=data["seq_lo"],
            seq_hi=data["seq_hi"],
        )
