"""Per-partition delivery runtime.

A partition owns:

* a cyclic :class:`~rdl.sequence.SequenceTracker` mapping wire (epoch, seq) to
  non-cyclical absolute indices;
* an ordered set of *slots* (one per abs): real events or synthetic skips;
* a skip-proof ledger (epoch + range bound, revocable before crossing);
* a conflict ledger (late events / epoch divergence / post-cross revoke);
* a corrections ledger (explicit decisions delivered out-of-band);
* a durable spool (payload overflow) plus a WAL (state transitions).

Two watermarks are kept deliberately separate:

* ``head_abs``   - the delivery position (next slot to offer downstream);
* ``low_abs``    - the acknowledgement position (everything below is confirmed).

Redelivery after recovery replays every delivered-but-unacked slot; its
delivery id embeds abs and therefore never changes.
"""

from __future__ import annotations

from dataclasses import dataclass, asdict
from pathlib import Path
from typing import Any

from .errors import (
    CapacityExceededError,
    InvalidDeliveryIDError,
    PartitionFrozenError,
    PartitionNotFoundError,
    ProofRejectedError,
)
from .model import (
    AckResult,
    ConflictKind,
    ConflictView,
    CorrectionItem,
    DeliveredItem,
    GrantResult,
    IngestResult,
    PartConfig,
    PumpResult,
    RevokeResult,
    ResolveResult,
)
from .sequence import SequenceTracker, fwd_distance
from .storage import (
    Spool,
    WAL,
    FailureController,
    WatermarkFile,
    write_json_atomic,
    read_json,
)


def delivery_id(partition: str, abs_index: int) -> str:
    return f"dlv:{partition}:{abs_index}"


def correction_id(partition: str, ordinal: int) -> str:
    return f"corr:{partition}:{ordinal}"


def parse_delivery_id(did: str) -> tuple[str, int]:
    try:
        kind, partition, rest = did.split(":", 2)
    except ValueError as exc:
        raise InvalidDeliveryIDError(did, "malformed") from exc
    if kind not in ("dlv", "corr"):
        raise InvalidDeliveryIDError(did, "unknown prefix")
    try:
        return partition, int(rest)
    except ValueError as exc:
        raise InvalidDeliveryIDError(did, "abs is not an integer") from exc


# ---------------------------------------------------------------------------
# slot / ledger entries
# ---------------------------------------------------------------------------


@dataclass
class Slot:
    abs_index: int
    epoch: int
    seq: int
    payload: Any = None
    kind: str = "event"             # event | skip
    status: str = "buffered"        # buffered | delivered | acked
    proof_id: str | None = None
    reinstated: bool = False
    on_spool: bool = True           # payload resident in the durable spool?
    cached: bool = True             # payload resident in memory?

    @property
    def is_skip(self) -> bool:
        return self.kind == "skip"

    def to_dict(self) -> dict:
        return asdict(self)

    @classmethod
    def from_dict(cls, d: dict) -> "Slot":
        return cls(**d)


@dataclass
class Proof:
    proof_id: str
    partition: str
    epoch: int
    start_seq: int
    end_seq: int
    abs_start: int
    abs_end: int
    crossed: bool = False
    revoked: bool = False
    conflict_id: str | None = None

    def to_dict(self) -> dict:
        return asdict(self)

    @classmethod
    def from_dict(cls, d: dict) -> "Proof":
        return cls(**d)


@dataclass
class Conflict:
    conflict_id: str
    partition: str
    ordinal: int
    kind: str
    epoch: int
    seq: int
    abs_index: int | None
    payload: Any
    proof_id: str | None
    detail: str
    open: bool = True
    decision: str | None = None
    correction_id: str | None = None

    def view(self) -> ConflictView:
        return ConflictView(
            conflict_id=self.conflict_id,
            partition=self.partition,
            kind=self.kind,
            epoch=self.epoch,
            seq=self.seq,
            abs_index=self.abs_index,
            payload=self.payload,
            proof_id=self.proof_id,
            detail=self.detail,
            open=self.open,
            decision=self.decision,
            correction_id=self.correction_id,
            ordinal=self.ordinal,
        )

    def to_dict(self) -> dict:
        return asdict(self)

    @classmethod
    def from_dict(cls, d: dict) -> "Conflict":
        return cls(**d)


@dataclass
class Correction:
    correction_id: str
    partition: str
    ordinal: int
    kind: str                       # reinstate | skip-confirmed
    proof_id: str | None
    epoch: int
    seq: int
    abs_index: int | None
    payload: Any
    conflict_id: str
    status: str = "pending"         # pending | delivered | acked

    def to_dict(self) -> dict:
        return asdict(self)

    @classmethod
    def from_dict(cls, d: dict) -> "Correction":
        return cls(**d)


# ---------------------------------------------------------------------------
# partition runtime
# ---------------------------------------------------------------------------


class Partition:
    def __init__(
        self,
        cfg: PartConfig,
        owner: str,
        root: str | Path,
        failures: FailureController,
    ):
        self.cfg = cfg
        self.partition = cfg.partition
        self.owner = owner
        self.root = Path(root) / self.partition
        self.root.mkdir(parents=True, exist_ok=True)
        self.failures = failures
        self.spool = Spool(self.root / "spool.log")
        self.wal = WAL(self.root / "wal.log")
        self.watermark = WatermarkFile(self.root / "watermark.json")
        self.seq = SequenceTracker(
            modulus=cfg.modulus,
            initial_seq=cfg.initial_seq,
            initial_abs=cfg.initial_abs,
            reorder_window=cfg.reorder_window,
        )
        self.low_abs = cfg.initial_abs
        self.head_abs = cfg.initial_abs
        self.slots: dict[int, Slot] = {}
        self.proofs: dict[str, Proof] = {}
        self.conflicts: dict[str, Conflict] = {}
        self.corrections: dict[str, Correction] = {}
        self.conflict_seq = 0
        self.frozen = False
        self._offered: set[int] = set()      # abs offered during this process
        self._corr_offered: set[str] = set()

    # -- lifecycle ---------------------------------------------------------

    def persist_meta(self) -> None:
        write_json_atomic(
            self.root / "partition.json",
            {
                "partition": self.partition,
                "owner": self.owner,
                "cfg": asdict(self.cfg),
            },
        )

    def freeze(self) -> None:
        self.frozen = True

    def unfreeze(self) -> None:
        self.frozen = False

    def check_frozen(self) -> None:
        if self.frozen:
            raise PartitionFrozenError(self.partition, self.owner)

    # -- (de)serialization -------------------------------------------------

    def snapshot_dict(self) -> dict:
        return {
            "partition": self.partition,
            "owner": self.owner,
            "low_abs": self.low_abs,
            "head_abs": self.head_abs,
            "seq": self.seq.to_dict(),
            "slots": {str(a): s.to_dict() for a, s in sorted(self.slots.items())},
            "proofs": {p: x.to_dict() for p, x in sorted(self.proofs.items())},
            "conflicts": {c: x.to_dict() for c, x in sorted(self.conflicts.items())},
            "corrections": {c: x.to_dict() for c, x in sorted(self.corrections.items())},
            "conflict_seq": self.conflict_seq,
        }

    def load_snapshot(self, data: dict) -> None:
        self.owner = data["owner"]
        self.low_abs = data["low_abs"]
        self.head_abs = data["head_abs"]
        self.seq = SequenceTracker.from_dict(data["seq"])
        self.slots = {int(k): Slot.from_dict(v) for k, v in data["slots"].items()}
        self.proofs = {k: Proof.from_dict(v) for k, v in data["proofs"].items()}
        self.conflicts = {k: Conflict.from_dict(v) for k, v in data["conflicts"].items()}
        self.corrections = {
            k: Correction.from_dict(v) for k, v in data["corrections"].items()
        }
        self.conflict_seq = data.get("conflict_seq", 0)
        # nothing is in-flight at the start of a fresh process
        self._offered.clear()
        self._corr_offered.clear()

    def checkpoint(self, *, allow_frozen: bool = False) -> None:
        """Atomically materialize state, then retire the WAL."""
        if self.frozen and not allow_frozen:
            raise PartitionFrozenError(self.partition, self.owner)
        write_json_atomic(self.root / "snapshot.json", self.snapshot_dict())
        self.wal.truncate_all()

    # -- recovery ----------------------------------------------------------

    @classmethod
    def open_dir(cls, part_dir: str | Path, owner: str,
                 failures: FailureController) -> "Partition":
        root = Path(part_dir)
        meta = read_json(root / "partition.json")
        cfg = PartConfig(**meta["cfg"])
        part = cls(cfg=cfg, owner=owner, root=root.parent, failures=failures)
        snap_path = part.root / "snapshot.json"
        if snap_path.exists():
            part.load_snapshot(read_json(snap_path))
        else:
            # first-ever open: anchor the stream at its initial position
            part.low_abs = cfg.initial_abs
            part.head_abs = cfg.initial_abs
        wm = part.watermark.read()
        if wm is not None:
            part.low_abs = max(part.low_abs, wm)
            part.head_abs = max(part.head_abs, part.low_abs)
        part._recover_spool()
        part._replay_wal()
        part._compact_below_low()
        part.owner = owner
        part.persist_meta()
        return part

    def _recover_spool(self) -> None:
        """Rebuild buffered/delivered event slots from durable payloads."""
        for rec in self.spool.iter_events():
            abs_index = rec["abs"]
            if abs_index < self.low_abs:
                continue
            existing = self.slots.get(abs_index)
            if existing is not None and existing.is_skip:
                # A proof granted before a crash can overlap a record that was
                # already durable.  The explicit skip wins; the payload is
                # dropped during compaction and never delivered.
                continue
            if existing is not None:
                continue
            slot = Slot(
                abs_index=abs_index,
                epoch=rec["epoch"],
                seq=rec["seq"],
                payload=rec["payload"],
                kind="event",
                status="buffered",
                on_spool=True,
                cached=False,
            )
            self.slots[abs_index] = slot
            self._anchor_for(slot.epoch, slot.abs_index, slot.seq)
        # Restore delivery status up to the persisted head.  Delivery is
        # written before the WAL commit, so an offer lost to a crash is
        # redelivered (at-least-once, stable id).
        for abs_index in sorted(self.slots):
            if self.low_abs <= abs_index < self.head_abs:
                slot = self.slots[abs_index]
                if not slot.is_skip and slot.status == "buffered":
                    slot.status = "delivered"

    def _replay_wal(self) -> None:
        for rec in self.wal.iter_lines():
            self._apply_wal(rec, replay=True)

    def _anchor_for(self, epoch: int, abs_index: int, seq: int) -> None:
        anchor = self.seq.anchors.get(epoch)
        if anchor is None:
            self.seq.anchor_epoch(epoch, abs_index, seq)
            return
        # keep the earliest witness
        if abs_index < anchor.first_abs:
            self.seq.anchors[epoch] = type(anchor).from_ingress(epoch, abs_index, seq)

    def _compact_below_low(self) -> None:
        for abs_index in [a for a in self.slots if a < self.low_abs]:
            del self.slots[abs_index]
        if self.spool.path.exists():
            self.spool.compact(self.low_abs)

    # -- WAL application ---------------------------------------------------

    def _wal(self, record: dict) -> None:
        self.wal.append_commit(record, crash=self.failures.consume_commit_crash())
        self._apply_wal(record, replay=False)

    def _apply_wal(self, rec: dict, replay: bool) -> None:  # noqa: C901
        t = rec["type"]
        if t == "EPOCH_ANCHOR":
            from .sequence import EpochAnchor

            self.seq.anchors[rec["epoch"]] = EpochAnchor(
                epoch=rec["epoch"],
                first_abs=rec["abs"],
                first_seq=rec["seq"],
                start_abs=rec["start_abs"],
            )
        elif t == "SKIP_GRANTED":
            proof = Proof(
                proof_id=rec["proof_id"],
                partition=self.partition,
                epoch=rec["epoch"],
                start_seq=rec["start_seq"],
                end_seq=rec["end_seq"],
                abs_start=rec["abs_start"],
                abs_end=rec["abs_end"],
            )
            self.proofs[proof.proof_id] = proof
            for abs_index in range(proof.abs_start, proof.abs_end + 1):
                existing = self.slots.get(abs_index)
                if existing is not None and not existing.is_skip:
                    # late real arrival supersedes an un-crossed skip
                    continue
                self.slots[abs_index] = Slot(
                    abs_index=abs_index,
                    epoch=proof.epoch,
                    seq=self.seq.seq_of_abs(abs_index),
                    kind="skip",
                    status="buffered",
                    proof_id=proof.proof_id,
                    on_spool=False,
                    cached=False,
                )
        elif t == "SKIP_REVOKED":
            proof = self.proofs.get(rec["proof_id"])
            if proof is None:
                return
            proof.revoked = True
            proof.conflict_id = rec.get("conflict_id")
            if not proof.crossed:
                # still before the frontier: remove the synthetic slots again
                for abs_index in range(proof.abs_start, proof.abs_end + 1):
                    slot = self.slots.get(abs_index)
                    if slot is not None and slot.is_skip:
                        del self.slots[abs_index]
        elif t == "SKIP_CROSSED":
            proof = self.proofs.get(rec["proof_id"])
            if proof is not None:
                proof.crossed = True
        elif t == "DELIVERED":
            slot = self.slots.get(rec["abs"])
            if slot is not None and slot.status == "buffered":
                slot.status = "delivered"
            if rec["abs"] >= self.head_abs:
                # the head jumps over every (revocable-before-now) skip slot
                # between the old head and this event
                crossed_pids: set[str] = set()
                for crossed_abs in range(self.head_abs, rec["abs"]):
                    skipped = self.slots.get(crossed_abs)
                    if skipped is not None and skipped.is_skip:
                        skipped.status = "acked"
                        proof = self.proofs.get(skipped.proof_id or "")
                        if proof is not None:
                            crossed_pids.add(proof.proof_id)
                self.head_abs = rec["abs"] + 1
                if slot is not None:
                    self.seq.advance_to(slot.abs_index, slot.seq, slot.epoch)
                self._advance_low()
                for pid in sorted(crossed_pids):
                    proof = self.proofs.get(pid)
                    if proof is not None and not proof.crossed:
                        proof.crossed = True
                        self._wal({"type": "SKIP_CROSSED", "proof_id": pid})
        elif t == "ACKED":
            slot = self.slots.get(rec["abs"])
            if slot is not None:
                slot.status = "acked"
            self._advance_low()
        elif t == "CONFLICT":
            conflict = Conflict(
                conflict_id=rec["conflict_id"],
                partition=self.partition,
                ordinal=rec["ordinal"],
                kind=rec["kind"],
                epoch=rec["epoch"],
                seq=rec["seq"],
                abs_index=rec.get("abs"),
                payload=rec.get("payload"),
                proof_id=rec.get("proof_id"),
                detail=rec["detail"],
            )
            self.conflicts[conflict.conflict_id] = conflict
            self.conflict_seq = max(self.conflict_seq, conflict.ordinal)
        elif t == "CONFLICT_RESOLVED":
            conflict = self.conflicts.get(rec["conflict_id"])
            if conflict is None:
                return
            conflict.open = False
            conflict.decision = rec["decision"]
            conflict.correction_id = rec.get("correction_id")
            if rec.get("correction"):
                c = rec["correction"]
                self.corrections[c["correction_id"]] = Correction(
                    correction_id=c["correction_id"],
                    partition=self.partition,
                    ordinal=c["ordinal"],
                    kind=c["kind"],
                    proof_id=c.get("proof_id"),
                    epoch=c["epoch"],
                    seq=c["seq"],
                    abs_index=c.get("abs"),
                    payload=c.get("payload"),
                    conflict_id=conflict.conflict_id,
                    status="pending",
                )
            if (
                conflict.kind == ConflictKind.LATE_EVENT_AFTER_SKIP
                and rec["decision"] == "confirm_skip"
            ):
                pass
        elif t == "CORRECTION_DELIVERED":
            corr = self.corrections.get(rec["correction_id"])
            if corr is not None and corr.status == "pending":
                corr.status = "delivered"
        elif t == "CORRECTION_ACKED":
            corr = self.corrections.get(rec["correction_id"])
            if corr is not None:
                corr.status = "acked"
        elif t == "REINSTATE_SLOT":
            slot = self.slots.get(rec["abs"])
            if slot is not None and slot.is_skip:
                slot.reinstated = True
        elif t == "MIGRATION":
            self.owner = rec["to_owner"]

    def _advance_low(self) -> None:
        previous = self.low_abs
        while True:
            slot = self.slots.get(self.low_abs)
            if slot is None:
                break
            # synthetic skips are confirmed by construction; real events need
            # an explicit downstream acknowledgement.
            if slot.is_skip:
                self.low_abs += 1
                continue
            if slot.status != "acked":
                break
            self.low_abs += 1
        if self.low_abs != previous:
            self.watermark.write(self.low_abs)

    # -- ingestion ---------------------------------------------------------

    def _live_count(self) -> int:
        return len(self.slots)

    def _cached_count(self) -> int:
        return sum(1 for s in self.slots.values() if s.cached)

    def _register_conflict(
        self,
        kind: str,
        epoch: int,
        seq: int,
        abs_index: int | None,
        payload: Any,
        proof_id: str | None,
        detail: str,
    ) -> Conflict:
        self.conflict_seq += 1
        cid = f"conflict:{self.partition}:{self.conflict_seq}"
        conflict = Conflict(
            conflict_id=cid,
            partition=self.partition,
            ordinal=self.conflict_seq,
            kind=kind,
            epoch=epoch,
            seq=seq,
            abs_index=abs_index,
            payload=payload,
            proof_id=proof_id,
            detail=detail,
        )
        self._wal({"type": "CONFLICT", **conflict.to_dict()})
        return conflict

    def ingest(self, event) -> IngestResult:
        self.check_frozen()
        if event.partition != self.partition:
            raise PartitionNotFoundError(event.partition)

        pos = self.seq.resolve(event.seq)
        abs_index = pos.abs_index

        if not self.seq.within_window(pos):
            return IngestResult(
                partition=self.partition,
                accepted=False,
                abs_index=abs_index,
                kind="rejected",
                reason=f"seq {event.seq} outside reorder window "
                f"(distance={pos.distance})",
            )

        existing = self.slots.get(abs_index)

        # -- duplicate / conflict on an occupied slot ---------------------
        if existing is not None:
            if existing.is_skip:
                bound_epoch = existing.epoch
                proof = self.proofs.get(existing.proof_id or "")
                if proof is not None:
                    bound_epoch = proof.epoch
                kind = (
                    ConflictKind.LATE_EVENT_AFTER_SKIP
                    if event.epoch == bound_epoch
                    else ConflictKind.EPOCH_DIVERGENCE_AT_GAP
                )
                detail = (
                    f"event epoch={event.epoch} seq={event.seq} arrived for "
                    f"abs={abs_index} already crossed via proof "
                    f"{existing.proof_id}"
                )
                conflict = self._register_conflict(
                    kind=kind,
                    epoch=event.epoch,
                    seq=event.seq,
                    abs_index=abs_index,
                    payload=event.payload,
                    proof_id=existing.proof_id,
                    detail=detail,
                )
                return IngestResult(
                    partition=self.partition,
                    accepted=False,
                    abs_index=abs_index,
                    kind="conflict",
                    conflict_id=conflict.conflict_id,
                )

            if existing.epoch != event.epoch:
                conflict = self._register_conflict(
                    kind=ConflictKind.EPOCH_DIVERGENCE_AT_GAP,
                    epoch=event.epoch,
                    seq=event.seq,
                    abs_index=abs_index,
                    payload=event.payload,
                    proof_id=None,
                    detail=(
                        f"abs={abs_index} already holds epoch {existing.epoch}; "
                        f"ingress claims epoch {event.epoch}"
                    ),
                )
                return IngestResult(
                    partition=self.partition,
                    accepted=False,
                    abs_index=abs_index,
                    kind="conflict",
                    conflict_id=conflict.conflict_id,
                )

            # same position, same epoch: idempotent duplicate
            return IngestResult(
                partition=self.partition,
                accepted=True,
                abs_index=abs_index,
                kind="duplicate",
                duplicate_of=delivery_id(self.partition, abs_index),
            )

        # redelivery of an already-trimmed, fully acknowledged event
        if abs_index < self.low_abs:
            return IngestResult(
                partition=self.partition,
                accepted=True,
                abs_index=abs_index,
                kind="duplicate",
                duplicate_of=delivery_id(self.partition, abs_index),
            )

        # -- admit a new slot ---------------------------------------------
        if self._live_count() >= self.cfg.capacity_live:
            raise CapacityExceededError(
                self.partition,
                live=self._live_count(),
                cap=self.cfg.capacity_live,
                tier="disk",
            )

        fail = self.failures.consume_spill_failure(self.partition)
        try:
            self.spool.append_event(
                abs_index,
                event.epoch,
                event.seq,
                event.payload,
                fail=fail,
            )
        except Exception:
            # nothing mutated: the source must retry the ingress
            return IngestResult(
                partition=self.partition,
                accepted=False,
                abs_index=abs_index,
                kind="rejected",
                reason="spill_flush_failed",
                spill_failed=True,
            )

        overflow = self._cached_count() >= self.cfg.memory_slots
        slot = Slot(
            abs_index=abs_index,
            epoch=event.epoch,
            seq=event.seq,
            payload=None if overflow else event.payload,
            kind="event",
            status="buffered",
            on_spool=True,
            cached=not overflow,
        )
        if event.epoch not in self.seq.anchors:
            anchor = self.seq.anchor_epoch(event.epoch, abs_index, event.seq)
            self._wal(
                {
                    "type": "EPOCH_ANCHOR",
                    "epoch": anchor.epoch,
                    "abs": anchor.first_abs,
                    "seq": anchor.first_seq,
                    "start_abs": anchor.start_abs,
                }
            )
        if self.seq.frontier_epoch is None:
            self.seq.frontier_epoch = event.epoch
        self.slots[abs_index] = slot
        return IngestResult(
            partition=self.partition,
            accepted=True,
            abs_index=abs_index,
            kind="accepted",
            spilled=overflow,
        )

    # -- skip proofs -------------------------------------------------------

    def proof_seq_next(self) -> int:
        return len(self.proofs) + 1

    def _gap_at_head(self) -> int | None:
        """Return the abs of the first missing slot at/after head, if any."""
        abs_index = self.head_abs
        seen = 0
        while abs_index in self.slots:
            abs_index += 1
            seen += 1
            if seen > self.cfg.modulus:
                return None
        return abs_index

    def grant_skip(
        self,
        epoch: int,
        start_seq: int,
        end_seq: int,
        *,
        proof_id: str | None = None,
    ) -> GrantResult:
        self.check_frozen()
        # Normalize cyclic order of the range (inclusive).
        m = self.cfg.modulus
        length = fwd_distance(end_seq, start_seq, m) + 1
        if length > self.cfg.capacity_live:
            raise ProofRejectedError(self.partition, "range larger than live cap")

        gap_abs = self._gap_at_head()
        if gap_abs is None:
            raise ProofRejectedError(self.partition, "no outstanding gap")
        gap_seq = self.seq.seq_of_abs(gap_abs)
        if start_seq != gap_seq:
            raise ProofRejectedError(
                self.partition,
                f"range must start at current gap seq {gap_seq}, got {start_seq}",
            )
        expected_epoch = self.seq.frontier_epoch
        if expected_epoch is not None and epoch != expected_epoch:
            raise ProofRejectedError(
                self.partition,
                f"proof epoch {epoch} does not bind source epoch {expected_epoch}",
            )

        abs_start = gap_abs
        abs_end = gap_abs + length - 1
        # The proof may only cover slots still missing; reject overlapping
        # real events so it can never silently discard data.
        for abs_index in range(abs_start, abs_end + 1):
            slot = self.slots.get(abs_index)
            if slot is not None and not slot.is_skip:
                raise ProofRejectedError(
                    self.partition,
                    f"abs {abs_index} already has a real event (seq {slot.seq})",
                )

        pid = proof_id or f"proof:{self.partition}:{self.proof_seq_next()}"
        self._wal(
            {
                "type": "SKIP_GRANTED",
                "proof_id": pid,
                "epoch": epoch,
                "start_seq": start_seq,
                "end_seq": end_seq,
                "abs_start": abs_start,
                "abs_end": abs_end,
            }
        )
        proof = self.proofs[pid]
        return GrantResult(
            partition=self.partition,
            proof_id=pid,
            epoch=epoch,
            start_seq=start_seq,
            end_seq=end_seq,
            abs_start=abs_start,
            abs_end=abs_end,
            crossed=self.head_abs > abs_end,
        )

    def revoke_skip(self, proof_id: str) -> RevokeResult:
        self.check_frozen()
        proof = self.proofs.get(proof_id)
        if proof is None:
            return RevokeResult(
                partition=self.partition,
                proof_id=proof_id,
                revoked=False,
                reason="unknown proof",
            )
        if proof.revoked:
            return RevokeResult(
                partition=self.partition,
                proof_id=proof_id,
                revoked=False,
                reason="already revoked",
            )
        if proof.crossed:
            conflict = self._register_conflict(
                kind=ConflictKind.PROOF_REVOKED_AFTER_CROSS,
                epoch=proof.epoch,
                seq=proof.start_seq,
                abs_index=proof.abs_start,
                payload=None,
                proof_id=proof_id,
                detail=(
                    f"proof {proof_id} revoked after head crossed abs "
                    f"{proof.abs_start}..{proof.abs_end}"
                ),
            )
            self._wal(
                {
                    "type": "SKIP_REVOKED",
                    "proof_id": proof_id,
                    "conflict_id": conflict.conflict_id,
                }
            )
            return RevokeResult(
                partition=self.partition,
                proof_id=proof_id,
                revoked=False,
                conflict_id=conflict.conflict_id,
                reason="proof already crossed",
            )

        self._wal({"type": "SKIP_REVOKED", "proof_id": proof_id})
        return RevokeResult(partition=self.partition, proof_id=proof_id, revoked=True)

    # -- conflicts & corrections ------------------------------------------

    def open_conflicts(self) -> list[ConflictView]:
        return [c.view() for c in self.conflicts.values() if c.open]

    def resolve_conflict(
        self, conflict_id: str, decision: str
    ) -> ResolveResult:
        self.check_frozen()
        conflict = self.conflicts.get(conflict_id)
        if conflict is None:
            return ResolveResult(
                partition=self.partition,
                conflict_id=conflict_id,
                decision=decision,
                accepted=False,
                reason="unknown conflict",
            )
        if not conflict.open:
            return ResolveResult(
                partition=self.partition,
                conflict_id=conflict_id,
                decision=decision,
                accepted=False,
                reason="already resolved",
            )
        if decision not in ("confirm_skip", "reinstate"):
            return ResolveResult(
                partition=self.partition,
                conflict_id=conflict_id,
                decision=decision,
                accepted=False,
                reason="decision must be confirm_skip or reinstate",
            )

        correction = None
        corr_id = None
        if decision == "reinstate":
            self.conflict_seq  # ordinal counter shared for stable corr ids
            corr_id = correction_id(self.partition, conflict.ordinal)
            correction = {
                "correction_id": corr_id,
                "ordinal": conflict.ordinal,
                "kind": "reinstate",
                "proof_id": conflict.proof_id,
                "epoch": conflict.epoch,
                "seq": conflict.seq,
                "abs": conflict.abs_index,
                "payload": conflict.payload,
            }
        else:
            corr_id = correction_id(self.partition, conflict.ordinal)
            correction = {
                "correction_id": corr_id,
                "ordinal": conflict.ordinal,
                "kind": "skip-confirmed",
                "proof_id": conflict.proof_id,
                "epoch": conflict.epoch,
                "seq": conflict.seq,
                "abs": conflict.abs_index,
                "payload": None,
            }

        rec = {
            "type": "CONFLICT_RESOLVED",
            "conflict_id": conflict_id,
            "decision": decision,
            "correction_id": corr_id,
            "correction": correction,
        }
        if decision == "reinstate" and conflict.abs_index is not None:
            slot = self.slots.get(conflict.abs_index)
            if slot is not None and slot.is_skip:
                rec_extra = {
                    "type": "REINSTATE_SLOT",
                    "abs": conflict.abs_index,
                }
                self._wal(rec)
                self._wal(rec_extra)
            else:
                self._wal(rec)
        else:
            self._wal(rec)
        return ResolveResult(
            partition=self.partition,
            conflict_id=conflict_id,
            decision=decision,
            accepted=True,
            correction_id=corr_id,
        )

    # -- delivery pump -----------------------------------------------------

    def _load_payload(self, slot: Slot) -> Any:
        if slot.cached:
            return slot.payload
        for rec in self.spool.iter_events():
            if rec["abs"] == slot.abs_index:
                return rec["payload"]
        raise RuntimeError(
            f"payload missing for abs={slot.abs_index} on {self.partition}"
        )

    def pump(self, max_deliveries: int = 100) -> PumpResult:
        if self.frozen:
            return PumpResult(
                partition=self.partition,
                blocked="frozen",
            )
        result = PumpResult(partition=self.partition)

        # 1) recovery redelivery: delivered-but-unacked, never offered yet in
        #    this process, in abs order.
        for abs_index in sorted(self.slots):
            slot = self.slots[abs_index]
            if slot.is_skip:
                continue
            if slot.status == "delivered" and abs_index not in self._offered:
                item = DeliveredItem(
                    delivery_id=delivery_id(self.partition, abs_index),
                    partition=self.partition,
                    epoch=slot.epoch,
                    seq=slot.seq,
                    abs_index=abs_index,
                    payload=self._load_payload(slot),
                    redelivery=True,
                )
                result.redelivered.append(item)
                self._offered.add(abs_index)

        # 2) in-order new delivery.
        #
        # The delivery head advances *only* with real delivered events (the
        # DELIVERED WAL record moves it).  A buffered skip in front of the
        # head is "carried along": when the event after the gap delivers, its
        # DELIVERED record jumps the head over the skip, and the proof is
        # marked crossed at that instant.  Until then the proof stays
        # revocable and the skip behaves exactly like an open gap.
        budget = max_deliveries - len(result.delivered)

        def inflight_count() -> int:
            return sum(
                1
                for s in self.slots.values()
                if not s.is_skip and s.status == "delivered"
            )

        abs_index = self.head_abs
        pending_skips: list[Slot] = []
        obstruction: str | None = None
        while budget > 0:
            slot = self.slots.get(abs_index)
            if slot is None:
                obstruction = "gap"
                break
            if slot.is_skip:
                proof = self.proofs.get(slot.proof_id or "")
                if proof is not None and proof.revoked and not proof.crossed:
                    obstruction = "gap"
                    break
                pending_skips.append(slot)
                abs_index += 1
                continue
            if slot.status == "buffered":
                if inflight_count() >= self.cfg.inflight_max:
                    obstruction = "capacity"
                    break
                # durable offer; the applier jumps head over any skips that
                # precede this event and marks their proofs crossed
                self._wal(
                    {
                        "type": "DELIVERED",
                        "abs": slot.abs_index,
                        "epoch": slot.epoch,
                        "seq": slot.seq,
                    }
                )
                result.delivered.append(
                    DeliveredItem(
                        delivery_id=delivery_id(self.partition, slot.abs_index),
                        partition=self.partition,
                        epoch=slot.epoch,
                        seq=slot.seq,
                        abs_index=slot.abs_index,
                        payload=self._load_payload(slot),
                    )
                )
                self._offered.add(slot.abs_index)
                pending_skips.clear()
                budget -= 1
                abs_index += 1
                continue
            abs_index += 1

        result.blocked = obstruction

        # 3) pending corrections, in arrival (ordinal) order.
        for corr in sorted(self.corrections.values(), key=lambda c: c.ordinal):
            if corr.status == "pending" and corr.correction_id not in self._corr_offered:
                result.corrections.append(
                    CorrectionItem(
                        correction_id=corr.correction_id,
                        partition=self.partition,
                        kind=corr.kind,
                        proof_id=corr.proof_id,
                        epoch=corr.epoch,
                        seq=corr.seq,
                        abs_index=corr.abs_index,
                        payload=corr.payload,
                    )
                )
                self._wal(
                    {"type": "CORRECTION_DELIVERED", "correction_id": corr.correction_id}
                )
                self._corr_offered.add(corr.correction_id)
            elif (
                corr.status == "delivered"
                and corr.correction_id not in self._corr_offered
            ):
                result.corrections.append(
                    CorrectionItem(
                        correction_id=corr.correction_id,
                        partition=self.partition,
                        kind=corr.kind,
                        proof_id=corr.proof_id,
                        epoch=corr.epoch,
                        seq=corr.seq,
                        abs_index=corr.abs_index,
                        payload=corr.payload,
                        redelivery=True,
                    )
                )
                self._corr_offered.add(corr.correction_id)

        result.spilled = any(not s.cached for s in self.slots.values())
        return result

    # -- acknowledgement ---------------------------------------------------

    def ack(self, delivery_id_str: str, *, checkpoint_after: bool = True) -> AckResult:
        self.check_frozen()
        kind, _, key = self._parse_id(delivery_id_str)
        if kind == "corr":
            corr = self.corrections.get(delivery_id_str)
            if corr is None:
                return AckResult(
                    delivery_id=delivery_id_str,
                    accepted=False,
                    unknown=True,
                )
            if corr.status == "acked":
                return AckResult(
                    delivery_id=delivery_id_str,
                    accepted=True,
                    duplicate=True,
                    partition=self.partition,
                )
            self._wal({"type": "CORRECTION_ACKED", "correction_id": corr.correction_id})
            return AckResult(
                delivery_id=delivery_id_str,
                accepted=True,
                partition=self.partition,
            )

        abs_index = key
        slot = self.slots.get(abs_index)
        if slot is None:
            if abs_index < self.low_abs:
                return AckResult(
                    delivery_id=delivery_id_str,
                    accepted=True,
                    duplicate=True,
                    partition=self.partition,
                    abs_index=abs_index,
                )
            return AckResult(
                delivery_id=delivery_id_str,
                accepted=False,
                unknown=True,
                partition=self.partition,
            )
        if slot.status == "acked":
            return AckResult(
                delivery_id=delivery_id_str,
                accepted=True,
                duplicate=True,
                partition=self.partition,
                abs_index=abs_index,
            )
        self._wal({"type": "ACKED", "abs": abs_index})
        self._trim()
        if checkpoint_after:
            self.checkpoint()
        return AckResult(
            delivery_id=delivery_id_str,
            accepted=True,
            partition=self.partition,
            abs_index=abs_index,
        )

    @staticmethod
    def _parse_id(did: str) -> tuple[str, str, int]:
        try:
            kind, partition, rest = did.split(":", 2)
            return kind, partition, int(rest)
        except (ValueError, AttributeError) as exc:
            raise InvalidDeliveryIDError(did, "malformed") from exc

    def _trim(self) -> None:
        removed = [a for a in self.slots if a < self.low_abs]
        for abs_index in removed:
            del self.slots[abs_index]
        if removed:
            self.spool.compact(self.low_abs)
