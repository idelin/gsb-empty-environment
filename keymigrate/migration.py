"""Ownership + state migration across partition-function versions.

Per key, a migration is a two-phase transfer:
  1. begin_transfer: router starts buffering new events for the key;
  2. stage the state snapshot and any buffered pre-switchover events into the
     new partition (idempotent: deduped by commit-seq watermarks);
  3. commit_transfer: the new owner becomes the single effective owner, the
     old partition drops the key.

If delivery to the new route fails, the transfer rolls back: staged state is
discarded, ownership stays with the old owner, and buffered events are flushed
back to it. Re-running a finished migration is a no-op.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Callable, Optional

from .events import Key
from .partition import PartitionFunction
from .registry import OwnershipRegistry
from .router import Router
from .store import PartitionStore


class RouteDeliveryError(Exception):
    """Delivery of events/state to the new route failed."""


@dataclass
class MigrationReport:
    target_version: int
    migrated: list[Key] = field(default_factory=list)
    skipped: list[Key] = field(default_factory=list)
    rolled_back: list[Key] = field(default_factory=list)
    aborted: bool = False


class Migrator:
    def __init__(
        self,
        registry: OwnershipRegistry,
        stores: dict[int, PartitionStore],
        router: Router,
        fail_on: Optional[Callable[[Key], bool]] = None,
        while_transferring: Optional[Callable[[Key], None]] = None,
    ):
        self._registry = registry
        self._stores = stores
        self._router = router
        self._fail_on = fail_on or (lambda key: False)
        self._while_transferring = while_transferring or (lambda key: None)

    def migrate(self, pf: PartitionFunction) -> MigrationReport:
        report = MigrationReport(target_version=pf.version)
        for key in self._registry.known_keys():
            record = self._registry.effective_owner(key)
            if record.version >= pf.version:
                report.skipped.append(key)  # already migrated: idempotent
                continue
            try:
                self._migrate_key(key, record.partition, pf)
                report.migrated.append(key)
            except RouteDeliveryError:
                report.rolled_back.append(key)
                report.aborted = True
                break
        if not report.aborted:
            self._registry.update_partition_function(pf)
        return report

    def _migrate_key(self, key: Key, old_partition: int, pf: PartitionFunction) -> None:
        old_store = self._stores[old_partition]
        new_partition = pf.partition_for(*key)
        new_store = self._stores[new_partition]
        same_store = new_store is old_store

        value, watermark = old_store.export_key(key)
        transfer = self._registry.begin_transfer(
            key, new_partition, pf.version, cutover_seq=watermark
        )
        try:
            # In-flight events arriving now are buffered by the router.
            self._while_transferring(key)
            if value is not None and not same_store:
                new_store.stage(key, value, watermark)
            # Deliver pre-switchover buffered events to the new route.
            for event in self._router.peek_buffer(key):
                new_store.apply(event, transfer.new_epoch)
            if self._fail_on(key):
                raise RouteDeliveryError(f"delivery to new route failed for {key}")
            self._registry.commit_transfer(key)
            self._router.clear_buffer(key)
            if not same_store:
                old_store.remove(key)
        except Exception as exc:
            if not same_store:
                new_store.discard(key)
            self._registry.rollback_transfer(key)
            self._router.flush_buffer(key)  # buffered events return to old owner
            raise RouteDeliveryError(str(exc)) from exc
