"""Per-partition delivery state machine.

Delivery position (``deliver_next`` / ``deliver_cycle``) and downstream
ack position (``ack_next`` / ``ack_cycle``) are tracked separately.
Buffered out-of-order events spill to disk past a memory cap; capacity
is bounded and overflow raises backpressure instead of growing without
limit. The cursor is lazily anchored to the first accepted event.
"""
from __future__ import annotations

from collections import OrderedDict
from dataclasses import dataclass
from typing import Optional

from .events import DEFAULT_MODULUS, Event, forward_distance, is_ahead
from .proofs import (
    LATE_EVENT_AFTER_SKIP,
    REVOKE_AFTER_CROSS,
    STALE_EPOCH_EVENT,
    Conflict,
    SkipProof,
)
from .spill import BackpressureError, CapacityError, SpillBackend


@dataclass(frozen=True)
class PartitionConfig:
    modulus: int = DEFAULT_MODULUS
    max_buffered: int = 64   # in-memory reorder buffer cap (events)
    max_spilled: int = 256   # on-disk spill cap (events)


class PartitionState:
    def __init__(self, name: str, config: PartitionConfig, spill: SpillBackend):
        self.name = name
        self.config = config
        self.spill = spill
        self.epoch = 0
        self.deliver_next: Optional[int] = None  # None until the first event
        self.deliver_cycle = 0
        self.ack_next = 0
        self.ack_cycle = 0
        self.buffer: dict[int, Event] = {}            # seq -> event, in memory
        self.spilled: dict[int, int] = {}             # seq -> cycle, on disk
        self.delivered: "OrderedDict[str, Event]" = OrderedDict()
        self.acked: set[str] = set()                  # acked delivery ids
        self.proofs: "OrderedDict[str, SkipProof]" = OrderedDict()
        self.conflicts: list[Conflict] = []
        self.quarantine: list[Event] = []
        self.future: dict[int, list[Event]] = {}      # not-yet-active epochs

    # ------------------------------------------------------------------ ingest

    def ingest(self, event: Event, emit: bool = True) -> list[Event]:
        """Accept an event; return newly deliverable events in order."""
        if event.epoch < self.epoch:
            self._register_conflict(STALE_EPOCH_EVENT, event,
                                    f"epoch {event.epoch} < current {self.epoch}")
            return []
        if event.epoch > self.epoch:
            self.future.setdefault(event.epoch, []).append(event)
            return []
        if self.deliver_next is None:
            self.deliver_next = event.seq
            self.ack_next = event.seq
        cycle = self._cycle_for(event.seq)
        event = event.with_cycle(cycle)
        if event.delivery_id in self.delivered:
            return []  # duplicate of an already delivered event
        if event.seq in self.buffer or event.seq in self.spilled:
            return []  # duplicate of a buffered event
        if self._crossed_proof_covers(event.seq, cycle):
            self._register_conflict(LATE_EVENT_AFTER_SKIP, event,
                                    "arrived after its gap was skipped")
            return []
        if not is_ahead(event.seq, self.deliver_next, self.config.modulus) \
                and event.seq != self.deliver_next:
            return []  # behind the cursor from an older turn: stale duplicate
        self._buffer_put(event)
        return self._drain(emit)

    def _cycle_for(self, seq: int) -> int:
        modulus = self.config.modulus
        base = self.deliver_cycle * modulus + (self.deliver_next or 0)
        if seq == self.deliver_next or is_ahead(seq, self.deliver_next, modulus):
            return (base + forward_distance(self.deliver_next, seq, modulus)) // modulus
        return (base - (self.deliver_next - seq) % modulus) // modulus

    def _buffer_put(self, event: Event) -> None:
        if len(self.buffer) < self.config.max_buffered:
            self.buffer[event.seq] = event
            return
        farthest = max(
            list(self.buffer) + [event.seq],
            key=lambda s: forward_distance(self.deliver_next, s, self.config.modulus),
        )
        if farthest == event.seq:
            self._spill_event(event)
            return
        evicted = self.buffer.pop(farthest)
        try:
            self._spill_event(evicted)
        except BackpressureError:
            self.buffer[farthest] = evicted  # nothing lost on spill failure
            raise
        self.buffer[event.seq] = event

    def _spill_event(self, event: Event) -> None:
        if len(self.spilled) >= self.config.max_spilled:
            raise CapacityError(
                f"partition {self.name}: spill capacity "
                f"{self.config.max_spilled} exceeded"
            )
        self.spill.write(event)  # may raise SpillError (backpressure)
        self.spilled[event.seq] = event.cycle

    def _get(self, seq: int) -> Optional[Event]:
        if seq in self.buffer:
            return self.buffer[seq]
        if seq in self.spilled:
            return self.spill.read(self.name, self.epoch, self.spilled[seq], seq)
        return None

    def _remove(self, seq: int) -> None:
        self.buffer.pop(seq, None)
        cycle = self.spilled.pop(seq, None)
        if cycle is not None:
            self.spill.remove(self.name, self.epoch, cycle, seq)

    def _drain(self, emit: bool) -> list[Event]:
        out: list[Event] = []
        modulus = self.config.modulus
        while self.deliver_next is not None:
            event = self._get(self.deliver_next)
            if event is not None:
                self._remove(self.deliver_next)
                self._mark_delivered(event)
                if emit:
                    out.append(event)
                if self.deliver_next == modulus - 1:
                    self.deliver_cycle += 1
                self.deliver_next = (self.deliver_next + 1) % modulus
                continue
            proof = self._active_proof_covering(self.deliver_next)
            if proof is None:
                break
            proof.crossed = True
            proof.crossed_cycle = self.deliver_cycle
            new_next = (proof.gap_end + 1) % modulus
            if new_next <= self.deliver_next:
                self.deliver_cycle += 1  # skipped across the wrap boundary
            self.deliver_next = new_next
        return out

    def _mark_delivered(self, event: Event) -> None:
        self.delivered[event.delivery_id] = event

    # ------------------------------------------------------------------ proofs

    def add_proof(self, proof: SkipProof, emit: bool = True) -> list[Event]:
        self.proofs[proof.proof_id] = proof
        return self._drain(emit)

    def revoke_proof(self, proof_id: str) -> bool:
        proof = self.proofs[proof_id]
        if proof.crossed:
            self._register_conflict(
                REVOKE_AFTER_CROSS, None,
                f"revoke of {proof_id} after gap was crossed")
            return False
        proof.revoked = True
        return True

    def _active_proof_covering(self, seq: int) -> Optional[SkipProof]:
        for proof in self.proofs.values():
            if (not proof.revoked and not proof.crossed
                    and proof.epoch == self.epoch
                    and proof.covers(seq, self.config.modulus)):
                return proof
        return None

    def _crossed_proof_covers(self, seq: int, cycle: int) -> bool:
        return any(
            p.crossed and not p.revoked and p.epoch == self.epoch
            and p.crossed_cycle == cycle
            and p.covers(seq, self.config.modulus)
            for p in self.proofs.values()
        )

    # ------------------------------------------------------------------ acks

    def ack(self, delivery_id: str) -> bool:
        """Idempotent downstream ack; returns True if it was new."""
        if delivery_id in self.acked or delivery_id not in self.delivered:
            return False
        self.acked.add(delivery_id)
        modulus = self.config.modulus
        while self._ack_id_at(self.ack_next) in self.acked:
            if self.ack_next == modulus - 1:
                self.ack_cycle += 1
            self.ack_next = (self.ack_next + 1) % modulus
        return True

    def _ack_id_at(self, seq: int) -> str:
        return f"{self.name}#{self.epoch}:{self.ack_cycle}:{seq}"

    def unacked(self) -> list[Event]:
        return [ev for did, ev in self.delivered.items() if did not in self.acked]

    # ------------------------------------------------------------------ epoch

    def advance_epoch(self, new_epoch: int, start_seq: int = 0,
                      emit: bool = True) -> list[Event]:
        if new_epoch <= self.epoch:
            raise ValueError(f"epoch must increase: {new_epoch} <= {self.epoch}")
        for seq, cycle in self.spilled.items():
            self.spill.remove(self.name, self.epoch, cycle, seq)
        self.epoch = new_epoch
        self.deliver_next = start_seq
        self.deliver_cycle = 0
        self.ack_next = start_seq
        self.ack_cycle = 0
        self.buffer.clear()
        self.spilled.clear()
        pending = self.future.pop(new_epoch, [])
        delivered: list[Event] = []
        for event in sorted(pending, key=lambda e: e.seq):
            delivered.extend(self.ingest(event, emit=emit))
        return delivered

    # ------------------------------------------------------------------ conflicts

    def _register_conflict(self, kind: str, event: Optional[Event], detail: str) -> Conflict:
        conflict = Conflict(
            conflict_id=f"{self.name}-c{len(self.conflicts)}",
            partition=self.name,
            kind=kind,
            detail=detail,
            event=event,
        )
        self.conflicts.append(conflict)
        if event is not None:
            self.quarantine.append(event)
        return conflict

    def resolve_conflict(self, conflict_id: str, action: str) -> Optional[Event]:
        """Correction flow. ``drop`` discards; ``deliver`` emits the
        quarantined event downstream as an explicit correction."""
        conflict = next(c for c in self.conflicts if c.conflict_id == conflict_id)
        if conflict.resolved:
            raise ValueError(f"conflict {conflict_id} already resolved")
        if action not in ("drop", "deliver"):
            raise ValueError(f"unknown resolution action: {action}")
        conflict.resolved = True
        conflict.resolution = action
        if action == "deliver" and conflict.event is not None:
            self._mark_delivered(conflict.event)
            return conflict.event
        return None

    # ------------------------------------------------------------------ migration

    def to_bundle(self) -> dict:
        buffered = []
        for seq in sorted(set(self.buffer) | set(self.spilled)):
            event = self._get(seq)
            if event is not None:
                buffered.append(event.to_dict())
        return {
            "partition": self.name,
            "epoch": self.epoch,
            "deliver_next": self.deliver_next,
            "deliver_cycle": self.deliver_cycle,
            "ack_next": self.ack_next,
            "ack_cycle": self.ack_cycle,
            "buffered": buffered,
            "delivered": [ev.to_dict() for ev in self.delivered.values()],
            "acked": sorted(self.acked),
            "proofs": [p.to_dict() for p in self.proofs.values()],
            "conflicts": [c.to_dict() for c in self.conflicts],
            "quarantine": [ev.to_dict() for ev in self.quarantine],
        }

    def load_bundle(self, bundle: dict) -> None:
        self.epoch = bundle["epoch"]
        self.deliver_next = bundle["deliver_next"]
        self.deliver_cycle = bundle.get("deliver_cycle", 0)
        self.ack_next = bundle["ack_next"]
        self.ack_cycle = bundle.get("ack_cycle", 0)
        self.buffer.clear()
        self.spilled.clear()
        for data in bundle["buffered"]:
            event = Event.from_dict(data)
            self._buffer_put(event)
        self.delivered = OrderedDict(
            (Event.from_dict(d).delivery_id, Event.from_dict(d))
            for d in bundle["delivered"]
        )
        self.acked = set(bundle["acked"])
        self.proofs = OrderedDict(
            (p.proof_id, p) for p in
            (SkipProof.from_dict(d) for d in bundle["proofs"])
        )
        self.conflicts = [Conflict.from_dict(d) for d in bundle["conflicts"]]
        self.quarantine = [Event.from_dict(d) for d in bundle["quarantine"]]
