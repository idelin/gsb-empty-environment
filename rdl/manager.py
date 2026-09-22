"""Multi-partition delivery manager.

Owns one :class:`~rdl.partition.Partition` runtime per partition plus shared
fault injection.  Provides the controllable lifecycle operations required by
the tests:

* ``create_partition`` / ``open``                 - provisioning & recovery;
* ``ingest`` / ``pump`` / ``ack``                 - steady-state traffic;
* ``grant_skip`` / ``revoke_skip``                - gap proofs;
* ``resolve_conflict`` / ``open_conflicts``       - explicit correction flow;
* ``export_partition`` / ``import_partition`` /
  ``migrate_partition``                           - controlled hot migration;
* ``crash`` / ``recover``                         - simulated process crash;
* ``capacity_sample``                             - boundedness observations.
"""

from __future__ import annotations

import shutil
from pathlib import Path

from .errors import MigrationError, PartitionNotFoundError
from .model import (
    CapacitySample,
    PartConfig,
)
from .partition import Partition
from .storage import FailureController, fsync_dir, read_json, write_json_atomic


class DeliveryManager:
    def __init__(
        self,
        root: str | Path,
        *,
        owner: str = "worker-1",
        failures: FailureController | None = None,
    ):
        self.root = Path(root)
        self.root.mkdir(parents=True, exist_ok=True)
        self.owner = owner
        self.failures = failures or FailureController()
        self._partitions: dict[str, Partition] = {}
        self._load_registry()

    # -- registry ----------------------------------------------------------

    def _registry_path(self) -> Path:
        return self.root / "registry.json"

    def _load_registry(self) -> None:
        path = self._registry_path()
        if not path.exists():
            write_json_atomic(path, {"partitions": {}})
            return
        data = read_json(path)
        for name, owner in data.get("partitions", {}).items():
            if (self.root / name / "partition.json").exists():
                part = Partition.open_dir(self.root / name, owner, self.failures)
                part.unfreeze()
                self._partitions[name] = part

    def _save_registry(self) -> None:
        write_json_atomic(
            self._registry_path(),
            {"partitions": {name: p.owner for name, p in self._partitions.items()}},
        )

    def _get(self, partition: str) -> Partition:
        part = self._partitions.get(partition)
        if part is None:
            raise PartitionNotFoundError(partition)
        return part

    def partitions(self) -> list[str]:
        return sorted(self._partitions)

    # -- provisioning / recovery ------------------------------------------

    def create_partition(self, cfg: PartConfig) -> Partition:
        if cfg.partition in self._partitions:
            raise MigrationError(f"partition {cfg.partition!r} already exists")
        part = Partition(
            cfg=cfg,
            owner=self.owner,
            root=self.root,
            failures=self.failures,
        )
        part.persist_meta()
        part.checkpoint()
        self._partitions[cfg.partition] = part
        self._save_registry()
        return part

    @classmethod
    def open(cls, root: str | Path, *, owner: str = "worker-1",
             failures: FailureController | None = None) -> "DeliveryManager":
        return cls(root, owner=owner, failures=failures)

    # -- traffic -----------------------------------------------------------

    def ingest(self, event):
        return self._get(event.partition).ingest(event)

    def pump(self, partition: str | None = None, *, max_deliveries: int = 100):
        if partition is not None:
            return self._get(partition).pump(max_deliveries=max_deliveries)
        return {
            name: part.pump(max_deliveries=max_deliveries)
            for name, part in sorted(self._partitions.items())
        }

    def ack(self, delivery_id: str, *, checkpoint_after: bool = True):
        _, part_name, _ = Partition._parse_id(delivery_id)
        return self._get(part_name).ack(
            delivery_id, checkpoint_after=checkpoint_after
        )

    # -- proofs / conflicts ------------------------------------------------

    def grant_skip(self, partition: str, epoch: int, start_seq: int, end_seq: int,
                   *, proof_id: str | None = None):
        return self._get(partition).grant_skip(
            epoch, start_seq, end_seq, proof_id=proof_id
        )

    def revoke_skip(self, partition: str, proof_id: str):
        return self._get(partition).revoke_skip(proof_id)

    def open_conflicts(self, partition: str | None = None):
        if partition is not None:
            return self._get(partition).open_conflicts()
        out = []
        for part in self._partitions.values():
            out.extend(part.open_conflicts())
        return out

    def resolve_conflict(self, partition: str, conflict_id: str, decision: str):
        return self._get(partition).resolve_conflict(conflict_id, decision)

    def partition_state(self, partition: str) -> dict:
        part = self._get(partition)
        return {
            "partition": partition,
            "owner": part.owner,
            "frozen": part.frozen,
            "low_abs": part.low_abs,
            "head_abs": part.head_abs,
            "gap_abs": part._gap_at_head(),
            "proofs": {pid: p.to_dict() for pid, p in part.proofs.items()},
            "conflicts": {cid: c.to_dict() for cid, c in part.conflicts.items()},
            "corrections": {cid: c.to_dict() for cid, c in part.corrections.items()},
            "unacked": [
                a for a, s in sorted(part.slots.items())
                if not s.is_skip and s.status == "delivered"
            ],
        }

    # -- capacity ----------------------------------------------------------

    def capacity_sample(self, partition: str | None = None) -> list[CapacitySample]:
        names = [partition] if partition is not None else sorted(self._partitions)
        samples = []
        for name in names:
            part = self._get(name)
            gap_abs = part._gap_at_head()
            gap_seq = (
                part.seq.seq_of_abs(gap_abs) if gap_abs is not None else None
            )
            samples.append(
                CapacitySample(
                    partition=name,
                    live_slots=part._live_count(),
                    live_cap=part.cfg.capacity_live,
                    cached_payloads=part._cached_count(),
                    memory_cap=part.cfg.memory_slots,
                    spool_records=sum(1 for _ in part.spool.iter_events()),
                    inflight=sum(
                        1
                        for s in part.slots.values()
                        if not s.is_skip and s.status == "delivered"
                    ),
                    inflight_cap=part.cfg.inflight_max,
                    delivered_head_abs=part.head_abs,
                    ack_abs=part.low_abs,
                    gap_start_seq=gap_seq,
                    owner=part.owner,
                )
            )
        return samples

    # -- controlled hot migration -----------------------------------------

    def export_partition(self, partition: str, bundle_dir: str | Path) -> Path:
        """Freeze a partition and snapshot every durable file to a bundle."""
        part = self._get(partition)
        part.freeze()
        part.checkpoint(allow_frozen=True)
        bundle = Path(bundle_dir)
        if bundle.exists():
            shutil.rmtree(bundle)
        bundle.mkdir(parents=True, exist_ok=True)
        for name in ("partition.json", "snapshot.json", "spool.log", "wal.log",
                     "watermark.json"):
            src = part.root / name
            if src.exists():
                shutil.copy2(src, bundle / name)
        fsync_dir(bundle)
        return bundle

    def import_partition(
        self,
        bundle_dir: str | Path,
        *,
        new_owner: str,
        target_root: str | Path | None = None,
    ) -> str:
        """Import a bundle under ``new_owner`` into (possibly another) manager.

        Returns the partition name.  The old owner stays frozen/fenced; the
        caller is expected to delete it only after the import succeeded.
        """
        bundle = Path(bundle_dir)
        meta = read_json(bundle / "partition.json")
        name = meta["partition"]
        root = Path(target_root) if target_root is not None else self.root
        dest = root / name
        if dest.exists() and (dest / "partition.json").exists():
            raise MigrationError(f"destination {dest} already hosts {name!r}")
        dest.mkdir(parents=True, exist_ok=True)
        for fname in ("partition.json", "snapshot.json", "spool.log", "wal.log",
                      "watermark.json"):
            src = bundle / fname
            if src.exists():
                shutil.copy2(src, dest / fname)
        part = Partition.open_dir(dest, new_owner, self.failures)
        part.unfreeze()
        part.persist_meta()
        if root == self.root:
            self._partitions[name] = part
            self._save_registry()
        return name

    def migrate_partition(
        self,
        partition: str,
        bundle_dir: str | Path,
        new_owner: str,
    ) -> dict:
        """In-process controlled hot migration.

        Steps (each is a distinct, inspectable event in the trajectory):

        1. freeze the source (ingress is fenced);
        2. checkpoint + export durable state to a bundle;
        3. import the bundle under ``new_owner`` in this manager;
        4. dispose of the old fenced directory;
        5. resume traffic under the new owner.

        Gap proofs, skip slots, open conflicts, corrections, the delivery head
        and the acknowledgement watermark all travel with the snapshot.
        """
        part = self._get(partition)
        before = self.partition_state(partition)
        bundle = self.export_partition(partition, bundle_dir)
        # remove the old fenced runtime before re-homing the bundle
        old_root = part.root
        del self._partitions[partition]
        if old_root.exists():
            shutil.rmtree(old_root)
        name = self.import_partition(bundle, new_owner=new_owner)
        after = self.partition_state(name)
        self._save_registry()
        return {"partition": name, "bundle": str(bundle), "before": before,
                "after": after, "from_owner": part.owner, "to_owner": new_owner}

    # -- crash / recovery --------------------------------------------------

    def crash(self) -> None:
        """Simulate process death: drop every in-memory runtime.

        Durable files are untouched; injected commit-crashes already rolled
        their bytes back, so recovery cannot see "phantom" commits.
        """
        names = list(self._partitions)
        for name in names:
            part = self._partitions.pop(name)
            # closing the file handles is enough; reopen later from disk
            part.spool = None  # type: ignore[assignment]
            part.wal = None  # type: ignore[assignment]

    def recover(self, *, owner: str | None = None) -> None:
        """Rebuild every partition from spool + WAL (+ snapshot)."""
        owner = owner or self.owner
        data = read_json(self._registry_path())
        for name in data.get("partitions", {}):
            part = Partition.open_dir(self.root / name, owner, self.failures)
            part.unfreeze()
            self._partitions[name] = part

    def shutdown(self) -> None:
        for part in self._partitions.values():
            part.checkpoint(allow_frozen=part.frozen)
