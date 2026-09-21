"""Multi-partition reliable delivery layer.

Durability model: every state-changing operation is journaled; ``flush``
persists, ``crash`` drops what was not flushed, ``recover`` replays the
journal and returns delivered-but-unacked events for redelivery with
their original (stable) delivery ids.
"""
from __future__ import annotations

import hashlib
import json
from pathlib import Path
from typing import Optional

from .events import Event
from .journal import Journal
from .partition import PartitionConfig, PartitionState
from .proofs import SkipProof
from .spill import SpillBackend


class MigratedAwayError(Exception):
    pass


class DeliveryLayer:
    def __init__(self, workdir, config: Optional[PartitionConfig] = None):
        self.workdir = Path(workdir)
        self.workdir.mkdir(parents=True, exist_ok=True)
        self.config = config or PartitionConfig()
        self.journal = Journal(self.workdir / "journal.jsonl")
        self.spill = SpillBackend(self.workdir / "spill")
        self._trace_fh = open(self.workdir / "trace.jsonl", "a", encoding="utf-8")
        self.partitions: dict[str, PartitionState] = {}
        self.migrated_away: set[str] = set()
        self._trace({"type": "open", "modulus": self.config.modulus})

    # ------------------------------------------------------------- partitions

    def _partition(self, name: str) -> PartitionState:
        if name in self.migrated_away:
            raise MigratedAwayError(f"partition {name} was migrated away")
        if name not in self.partitions:
            self.partitions[name] = PartitionState(name, self.config, self.spill)
        return self.partitions[name]

    def deliver_position(self, name: str) -> int:
        return self._partition(name).deliver_next

    def ack_position(self, name: str) -> int:
        return self._partition(name).ack_next

    def conflicts(self, name: str):
        return list(self._partition(name).conflicts)

    # ------------------------------------------------------------- operations

    def ingest(self, event: Event) -> list[Event]:
        state = self._partition(event.partition)  # raises if migrated away
        self.journal.append({"type": "ingest", "event": event.to_dict()})
        delivered = state.ingest(event)
        for ev in delivered:
            self._trace_deliver(ev, redelivery=False, correction=False)
        return delivered

    def authorize_skip(self, partition: str, epoch: int,
                       gap_start: int, gap_end: int):
        state = self._partition(partition)
        proof = SkipProof(
            proof_id=f"{partition}-sk{len(state.proofs)}",
            partition=partition, epoch=epoch,
            gap_start=gap_start, gap_end=gap_end,
        )
        self.journal.append({"type": "skip_proof", **proof.to_dict()})
        delivered = state.add_proof(proof)
        self._trace({"type": "skip", **proof.to_dict()})
        for ev in delivered:
            self._trace_deliver(ev, redelivery=False, correction=False)
        return proof, delivered

    def revoke_skip(self, partition: str, proof_id: str) -> bool:
        state = self._partition(partition)
        self.journal.append({"type": "revoke", "partition": partition,
                             "proof_id": proof_id})
        ok = state.revoke_proof(proof_id)
        if ok:
            self._trace({"type": "revoke", "partition": partition,
                         "proof_id": proof_id})
        else:
            self._trace_conflicts(partition)
        return ok

    def ack(self, delivery_id: str) -> bool:
        partition = delivery_id.split("#", 1)[0]
        state = self._partition(partition)
        self.journal.append({"type": "ack", "delivery_id": delivery_id})
        accepted = state.ack(delivery_id)
        self._trace({"type": "ack", "delivery_id": delivery_id,
                     "accepted": accepted})
        return accepted

    def advance_epoch(self, partition: str, new_epoch: int,
                      start_seq: int = 0) -> list[Event]:
        state = self._partition(partition)
        self.journal.append({"type": "epoch", "partition": partition,
                             "epoch": new_epoch, "start_seq": start_seq})
        delivered = state.advance_epoch(new_epoch, start_seq)
        self._trace({"type": "epoch", "partition": partition,
                     "epoch": new_epoch, "start_seq": start_seq})
        for ev in delivered:
            self._trace_deliver(ev, redelivery=False, correction=False)
        return delivered

    def resolve_conflict(self, partition: str, conflict_id: str,
                         action: str) -> Optional[Event]:
        state = self._partition(partition)
        self.journal.append({"type": "resolve", "partition": partition,
                             "conflict_id": conflict_id, "action": action})
        event = state.resolve_conflict(conflict_id, action)
        self._trace({"type": "resolve", "partition": partition,
                     "conflict_id": conflict_id, "action": action})
        if event is not None:
            self._trace_deliver(event, redelivery=False, correction=True)
        return event

    # ------------------------------------------------------------- lifecycle

    def flush(self) -> None:
        self.journal.flush()

    def crash(self) -> None:
        """Simulate process death: unflushed journal records are lost."""
        self.journal.crash()
        self.partitions = {}

    def recover(self) -> list[Event]:
        """Replay the journal; return unacked events for redelivery."""
        self.partitions = {}
        self.migrated_away = set()
        self.spill.clear()
        for record in self.journal.replay():
            self._apply(record)
        redeliveries: list[Event] = []
        for name in sorted(self.partitions):
            for event in self.partitions[name].unacked():
                redeliveries.append(event)
                self._trace_deliver(event, redelivery=True, correction=False)
        return redeliveries

    def _apply(self, record: dict) -> None:
        rtype = record["type"]
        if rtype == "ingest":
            event = Event.from_dict(record["event"])
            if event.partition not in self.migrated_away:
                self._partition(event.partition).ingest(event, emit=False)
        elif rtype == "skip_proof":
            self._partition(record["partition"]).add_proof(
                SkipProof.from_dict(record), emit=False)
        elif rtype == "revoke":
            self._partition(record["partition"]).revoke_proof(record["proof_id"])
        elif rtype == "ack":
            partition = record["delivery_id"].split("#", 1)[0]
            self._partition(partition).ack(record["delivery_id"])
        elif rtype == "epoch":
            self._partition(record["partition"]).advance_epoch(
                record["epoch"], record["start_seq"], emit=False)
        elif rtype == "resolve":
            self._partition(record["partition"]).resolve_conflict(
                record["conflict_id"], record["action"])
        elif rtype == "snapshot":
            bundle = record["bundle"]
            state = self._partition(bundle["partition"])
            state.load_bundle(bundle)
        elif rtype == "migrate_out":
            self.partitions.pop(record["partition"], None)
            self.migrated_away.add(record["partition"])
        else:
            raise ValueError(f"unknown journal record: {rtype}")

    # ------------------------------------------------------------- migration

    def export_partition(self, name: str) -> dict:
        """Hot-migrate out: snapshot gaps, proofs, deliveries and acks."""
        state = self._partition(name)
        bundle = state.to_bundle()
        # Spill files are embedded in the bundle; remove them from the old home.
        for seq, cycle in state.spilled.items():
            self.spill.remove(name, state.epoch, cycle, seq)
        self.journal.append({"type": "migrate_out", "partition": name})
        self.journal.flush()
        self.partitions.pop(name, None)
        self.migrated_away.add(name)
        return bundle

    def import_partition(self, bundle: dict) -> None:
        state = self._partition(bundle["partition"])
        self.journal.append({"type": "snapshot", "bundle": bundle})
        self.journal.flush()
        state.load_bundle(bundle)
        # Make the target trace self-contained: epoch cursor, carried proofs,
        # carried deliveries (as redeliveries) and carried acks.
        self._trace({"type": "epoch", "partition": state.name,
                     "epoch": state.epoch, "start_seq": state.deliver_next})
        for proof in state.proofs.values():
            if not proof.revoked:
                self._trace({"type": "skip", **proof.to_dict()})
        for event in state.delivered.values():
            self._trace_deliver(event, redelivery=True, correction=False)
        for delivery_id in sorted(state.acked):
            self._trace({"type": "ack", "delivery_id": delivery_id,
                         "accepted": True})

    # ------------------------------------------------------------- trace

    @staticmethod
    def _payload_hash(event: Event) -> str:
        return hashlib.sha1(event.payload.encode("utf-8")).hexdigest()[:12]

    def _trace_deliver(self, event: Event, redelivery: bool, correction: bool) -> None:
        self._trace({
            "type": "deliver",
            "partition": event.partition,
            "epoch": event.epoch,
            "seq": event.seq,
            "delivery_id": event.delivery_id,
            "payload_hash": self._payload_hash(event),
            "redelivery": redelivery,
            "correction": correction,
        })

    def _trace_conflicts(self, partition: str) -> None:
        for conflict in self._partition(partition).conflicts:
            self._trace({"type": "conflict", **conflict.to_dict(),
                         "event": conflict.event.to_dict() if conflict.event else None})

    def _trace(self, record: dict) -> None:
        self._trace_fh.write(json.dumps(record, sort_keys=True) + "\n")
        self._trace_fh.flush()

    def close(self) -> None:
        self._trace_fh.close()
