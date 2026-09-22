"""Value objects for the reliable delivery layer.

Coordinate system
-----------------
Each partition is an event stream with:

* ``epoch``  - monotonically increasing source epoch (failover / source reset);
* ``seq``    - wire sequence number, cyclical modulo ``PartConfig.modulus``;
* ``abs``    - the delivery layer's own non-cyclical absolute index.

``abs`` starts at ``PartConfig.initial_abs`` (default 0, meaning wire seq 1 maps
to abs 0) and never wraps.  The stable delivery id embeds ``abs`` so redelivery
after a crash keeps the same id forever.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any


# ---------------------------------------------------------------------------
# Configuration / input
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class PartConfig:
    """Static configuration of one partition.

    ``modulus`` is intentionally small in tests so wrap-around is observable;
    production deployments would use 2**64.
    """

    partition: str
    modulus: int = 1 << 32
    initial_seq: int = 1
    initial_abs: int = 0
    # Maximum number of live (un-trimmed) slots - the hard backpressure cap.
    capacity_live: int = 1024
    # Maximum number of slot payloads kept resident in memory; the rest are
    # evicted to the durable spool ("overflow to disk").
    memory_slots: int = 4
    # Maximum unacknowledged deliveries outstanding at once.
    inflight_max: int = 4

    @property
    def reorder_window(self) -> int:
        """How far ahead of the frontier an ingress is allowed to be.

        Half the modulus (exclusive) is the unambiguous zone: a wire seq and
        the same seq one lap later are distinguishable as long as live span
        stays below modulus/2.  The hard live cap makes that invariant true.
        """
        return self.modulus // 2 - 1


@dataclass(frozen=True)
class Event:
    partition: str
    epoch: int
    seq: int
    payload: Any = None


# ---------------------------------------------------------------------------
# Results
# ---------------------------------------------------------------------------


@dataclass
class IngestResult:
    partition: str
    accepted: bool
    abs_index: int | None = None
    kind: str = "accepted"          # accepted | duplicate | conflict | rejected
    duplicate_of: str | None = None # delivery id when kind == duplicate
    conflict_id: str | None = None
    reason: str = ""
    spilled: bool = False           # payload had to be evicted to disk
    spill_failed: bool = False


@dataclass
class DeliveredItem:
    delivery_id: str
    partition: str
    epoch: int
    seq: int
    abs_index: int
    payload: Any
    redelivery: bool = False


@dataclass
class PumpResult:
    partition: str
    delivered: list[DeliveredItem] = field(default_factory=list)
    corrections: list["CorrectionItem"] = field(default_factory=list)
    blocked: str | None = None      # gap | capacity | frozen | None
    # "delivered" counts only newly offered deliveries; redeliveries separate
    redelivered: list[DeliveredItem] = field(default_factory=list)
    spilled: bool = False


@dataclass
class CorrectionItem:
    correction_id: str
    partition: str
    kind: str                       # reinstate | skip-confirmed
    proof_id: str | None
    epoch: int
    seq: int
    abs_index: int | None
    payload: Any
    redelivery: bool = False


@dataclass
class AckResult:
    delivery_id: str
    accepted: bool
    duplicate: bool = False
    unknown: bool = False
    partition: str | None = None
    abs_index: int | None = None


@dataclass
class GrantResult:
    partition: str
    proof_id: str
    epoch: int
    start_seq: int
    end_seq: int
    abs_start: int
    abs_end: int
    crossed: bool = False           # frontier already moved over every slot


@dataclass
class RevokeResult:
    partition: str
    proof_id: str
    revoked: bool
    conflict_id: str | None = None  # set when revoked after crossing
    reason: str = ""


@dataclass
class ResolveResult:
    partition: str
    conflict_id: str
    decision: str
    accepted: bool
    correction_id: str | None = None
    reason: str = ""


# ---------------------------------------------------------------------------
# Diagnostics
# ---------------------------------------------------------------------------


@dataclass
class ConflictView:
    conflict_id: str
    partition: str
    kind: str                       # see ConflictKind constants
    epoch: int
    seq: int
    abs_index: int | None
    payload: Any
    proof_id: str | None
    detail: str
    open: bool
    decision: str | None = None
    correction_id: str | None = None
    # monotonic ordinal per partition, the deterministic arrival id
    ordinal: int = 0


class ConflictKind:
    LATE_EVENT_AFTER_SKIP = "late_event_after_skip"
    EPOCH_DIVERGENCE_AT_GAP = "epoch_divergence_at_gap"
    PROOF_REVOKED_AFTER_CROSS = "proof_revoked_after_cross"


@dataclass
class CapacitySample:
    partition: str
    live_slots: int
    live_cap: int
    cached_payloads: int
    memory_cap: int
    spool_records: int
    inflight: int
    inflight_cap: int
    delivered_head_abs: int | None
    ack_abs: int
    gap_start_seq: int | None
    owner: str
