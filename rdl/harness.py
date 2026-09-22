"""Controllable event test harness and delivery-trajectory recorder.

The harness is a thin deterministic driver around
:class:`~rdl.manager.DeliveryManager`.  Every meaningful step appends one JSON
line to a *trajectory file*; the verifier (``scripts/verify_trajectory.py``)
replays that file and asserts the delivery-layer invariants.

Trajectory event types::

    START, CONFIG, INGEST, PUMP, DELIVER, REDELIVER, CORRECTION,
    ACK, GRANT, REVOKE, CONFLICT, RESOLVE, SPILL_FAIL,
    MIGRATE, MIGRATE_CHECK, CRASH, RECOVER,
    STUCK_BEGIN, STUCK_END, SAMPLE, NOTE
"""

from __future__ import annotations

import json
import os
import time
from dataclasses import asdict
from pathlib import Path
from typing import Any

from .errors import CapacityExceededError, ProofRejectedError
from .manager import DeliveryManager
from .model import Event, PartConfig
from .storage import CommitCrash, FailureController


class Trajectory:
    def __init__(self, path: str | os.PathLike[str]):
        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self._fh = open(self.path, "w", encoding="utf-8")
        self._step = 0

    def emit(self, etype: str, **fields: Any) -> dict:
        self._step += 1
        record = {"step": self._step, "t": etype, **fields}
        self._fh.write(json.dumps(record, ensure_ascii=False, sort_keys=True) + "\n")
        self._fh.flush()
        return record

    def close(self) -> None:
        self._fh.flush()
        os.fsync(self._fh.fileno())
        self._fh.close()

    def __enter__(self) -> "Trajectory":
        return self

    def __exit__(self, *exc: Any) -> None:
        self.close()


class ScenarioHarness:
    """Driver exposing one method per controllable event."""

    def __init__(self, root: str | Path, trajectory: Trajectory,
                 *, owner: str = "worker-1"):
        self.root = Path(root)
        self.tr = trajectory
        self.failures = FailureController()
        self.mgr = DeliveryManager(self.root, owner=owner, failures=self.failures)
        self.configs: dict[str, PartConfig] = {}
        self.tr.emit("START", root=str(self.root), owner=owner, ts=time.time())

    # -- setup -------------------------------------------------------------

    def create_partition(self, cfg: PartConfig) -> None:
        self.mgr.create_partition(cfg)
        self.configs[cfg.partition] = cfg
        self.tr.emit(
            "CONFIG",
            partition=cfg.partition,
            modulus=cfg.modulus,
            initial_seq=cfg.initial_seq,
            initial_abs=cfg.initial_abs,
            capacity_live=cfg.capacity_live,
            memory_slots=cfg.memory_slots,
            inflight_max=cfg.inflight_max,
            reorder_window=cfg.reorder_window,
        )

    # -- traffic -----------------------------------------------------------

    def ingest(self, partition: str, epoch: int, seq: int,
               payload: Any = None, *, note: str = "") -> dict:
        event = Event(partition, epoch, seq, payload)
        try:
            res = self.mgr.ingest(event)
        except CapacityExceededError as exc:
            rec = self.tr.emit(
                "INGEST",
                partition=partition, epoch=epoch, seq=seq,
                accepted=False, kind="capacity",
                live=exc.live, cap=exc.cap, note=note,
            )
            return rec
        rec = self.tr.emit(
            "INGEST",
            partition=partition,
            epoch=epoch,
            seq=seq,
            accepted=res.accepted,
            kind=res.kind,
            abs=res.abs_index,
            duplicate_of=res.duplicate_of,
            conflict_id=res.conflict_id,
            spilled=res.spilled,
            spill_failed=res.spill_failed,
            reason=res.reason,
            note=note,
        )
        if res.spill_failed:
            self.tr.emit(
                "SPILL_FAIL",
                partition=partition, epoch=epoch, seq=seq, abs=res.abs_index,
            )
        if res.conflict_id:
            view = next(
                c for c in self.mgr.open_conflicts(partition)
                if c.conflict_id == res.conflict_id
            )
            self._emit_conflict(view)
        return rec

    def pump(self, partition: str, *, max_deliveries: int = 100,
             note: str = "") -> dict:
        res = self.mgr.pump(partition, max_deliveries=max_deliveries)
        rec = self.tr.emit(
            "PUMP",
            partition=partition,
            blocked=res.blocked,
            delivered=[i.delivery_id for i in res.delivered],
            redelivered=[i.delivery_id for i in res.redelivered],
            corrections=[c.correction_id for c in res.corrections],
            note=note,
        )
        # proofs carried across by the delivered run are now marked crossed
        for proof_id, proof in sorted(self.mgr._get(partition).proofs.items()):
            if proof.crossed and not getattr(self, f"_x_{proof_id}", False):
                self.tr.emit(
                    "SKIP_CROSSED",
                    partition=partition,
                    proof_id=proof_id,
                    abs_start=proof.abs_start,
                    abs_end=proof.abs_end,
                )
                setattr(self, f"_x_{proof_id}", True)
        for item in res.delivered:
            self.tr.emit(
                "DELIVER",
                delivery_id=item.delivery_id,
                partition=item.partition,
                epoch=item.epoch,
                seq=item.seq,
                abs=item.abs_index,
                payload=item.payload,
            )
        for item in res.redelivered:
            self.tr.emit(
                "REDELIVER",
                delivery_id=item.delivery_id,
                partition=item.partition,
                epoch=item.epoch,
                seq=item.seq,
                abs=item.abs_index,
                payload=item.payload,
            )
        for item in res.corrections:
            self.tr.emit(
                "CORRECTION",
                correction_id=item.correction_id,
                partition=item.partition,
                kind=item.kind,
                proof_id=item.proof_id,
                epoch=item.epoch,
                seq=item.seq,
                abs=item.abs_index,
                payload=item.payload,
                redelivery=item.redelivery,
            )
        return rec

    def ack(self, delivery_id: str, *, note: str = "") -> dict:
        try:
            res = self.mgr.ack(delivery_id)
        except CommitCrash as exc:
            self.tr.emit(
                "ACK",
                delivery_id=delivery_id,
                accepted=False,
                crashed=True,
                reason=str(exc),
                note=note,
            )
            raise
        rec = self.tr.emit(
            "ACK",
            delivery_id=delivery_id,
            accepted=res.accepted,
            duplicate=res.duplicate,
            unknown=res.unknown,
            partition=res.partition,
            abs=res.abs_index,
            note=note,
        )
        return rec

    # -- proofs ------------------------------------------------------------

    def grant_skip(self, partition: str, epoch: int, start_seq: int,
                   end_seq: int, *, proof_id: str | None = None,
                   note: str = "") -> dict:
        try:
            res = self.mgr.grant_skip(
                partition, epoch, start_seq, end_seq, proof_id=proof_id
            )
        except ProofRejectedError as exc:
            return self.tr.emit(
                "GRANT",
                partition=partition, epoch=epoch,
                start_seq=start_seq, end_seq=end_seq,
                accepted=False, reason=exc.reason, note=note,
            )
        return self.tr.emit(
            "GRANT",
            partition=partition,
            proof_id=res.proof_id,
            epoch=res.epoch,
            start_seq=res.start_seq,
            end_seq=res.end_seq,
            abs_start=res.abs_start,
            abs_end=res.abs_end,
            accepted=True,
            note=note,
        )

    def revoke_skip(self, partition: str, proof_id: str, *,
                    note: str = "") -> dict:
        res = self.mgr.revoke_skip(partition, proof_id)
        rec = self.tr.emit(
            "REVOKE",
            partition=partition,
            proof_id=proof_id,
            revoked=res.revoked,
            conflict_id=res.conflict_id,
            reason=res.reason,
            note=note,
        )
        if res.conflict_id:
            view = next(
                c for c in self.mgr.open_conflicts(partition)
                if c.conflict_id == res.conflict_id
            )
            self._emit_conflict(view)
        return rec

    def resolve_conflict(self, partition: str, conflict_id: str,
                         decision: str, *, note: str = "") -> dict:
        res = self.mgr.resolve_conflict(partition, conflict_id, decision)
        rec = self.tr.emit(
            "RESOLVE",
            partition=partition,
            conflict_id=conflict_id,
            decision=decision,
            accepted=res.accepted,
            correction_id=res.correction_id,
            reason=res.reason,
            note=note,
        )
        return rec

    def open_conflicts(self, partition: str):
        return self.mgr.open_conflicts(partition)

    def _emit_conflict(self, view) -> None:
        self.tr.emit(
            "CONFLICT",
            conflict_id=view.conflict_id,
            partition=view.partition,
            kind=view.kind,
            epoch=view.epoch,
            seq=view.seq,
            abs=view.abs_index,
            payload=view.payload,
            proof_id=view.proof_id,
            detail=view.detail,
            ordinal=view.ordinal,
        )

    # -- faults / lifecycle ------------------------------------------------

    def arm_spill_failure(self, partition: str, n: int = 1, *,
                          note: str = "") -> None:
        self.failures.arm_spill_failure(partition, n)
        self.tr.emit("NOTE",
                     message=f"armed {n} spill failure(s) for {partition}",
                     note=note)

    def arm_commit_crash(self, n: int = 1, *, note: str = "") -> None:
        self.failures.arm_commit_crash(n)
        self.tr.emit("NOTE", message=f"armed {n} commit crash(es)", note=note)

    def crash_and_recover(self, *, owner: str | None = None,
                          note: str = "") -> None:
        self.tr.emit("CRASH", note=note)
        self.mgr.crash()
        self.mgr.recover(owner=owner)
        self.tr.emit("RECOVER", owner=owner or self.mgr.owner, note=note)

    def migrate(self, partition: str, bundle_dir: str | Path, new_owner: str,
                *, note: str = "") -> dict:
        info = self.mgr.migrate_partition(partition, bundle_dir, new_owner)
        self.tr.emit(
            "MIGRATE",
            partition=partition,
            bundle=str(bundle_dir),
            from_owner=info["from_owner"],
            to_owner=info["to_owner"],
            before=_migration_view(info["before"]),
            after=_migration_view(info["after"]),
            note=note,
        )
        # explicit cross-check record: the states must be identical apart
        # from the owner label
        same = _states_equivalent(info["before"], info["after"])
        self.tr.emit(
            "MIGRATE_CHECK",
            partition=partition,
            state_equivalent=same,
            note="gaps, proofs, conflicts, corrections and acks carried over",
        )
        return info

    def stuck_begin(self, partition: str, *, note: str = "") -> None:
        self.tr.emit("STUCK_BEGIN", partition=partition, note=note)

    def stuck_end(self, partition: str, *, note: str = "") -> None:
        self.tr.emit("STUCK_END", partition=partition, note=note)

    def sample(self, partition: str | None = None, *, note: str = "") -> None:
        for s in self.mgr.capacity_sample(partition):
            self.tr.emit("SAMPLE", note=note, **asdict(s))

    def note(self, message: str, **fields: Any) -> None:
        self.tr.emit("NOTE", message=message, **fields)

    def state(self, partition: str) -> dict:
        return self.mgr.partition_state(partition)


def _migration_view(state: dict) -> dict:
    return {
        "owner": state["owner"],
        "low_abs": state["low_abs"],
        "head_abs": state["head_abs"],
        "gap_abs": state["gap_abs"],
        "unacked": list(state["unacked"]),
        "proofs": sorted(
            {
                k: {kk: vv for kk, vv in v.items() if kk != "partition"}
                for k, v in state["proofs"].items()
            }.items()
        ),
        "conflicts": sorted(state["conflicts"]),
        "corrections": sorted(state["corrections"]),
    }


def _states_equivalent(before: dict, after: dict) -> bool:
    b = _migration_view(before)
    a = _migration_view(after)
    b.pop("owner", None)
    a.pop("owner", None)
    return b == a
