"""Migration coordinator: moves key state between partition-function versions."""
from __future__ import annotations

from dataclasses import dataclass

from .events import EventLog, Key
from .ownership import OwnershipRegistry
from .partition import PartitionFunction
from .store import KeyStateStore


class RoutingError(Exception):
    """Forwarding buffered events to the new owner failed; ownership rolled back."""


@dataclass(frozen=True)
class Migration:
    migration_id: str
    old_fn: PartitionFunction
    new_fn: PartitionFunction


class MigrationCoordinator:
    def __init__(
        self,
        registry: OwnershipRegistry,
        stores: dict[str, KeyStateStore],
        log: EventLog | None = None,
    ):
        self.registry = registry
        self.stores = stores
        self.log = log

    def _log(self, record: dict) -> None:
        if self.log is not None:
            self.log.append(record)

    def affected_keys(self, migration: Migration) -> list[Key]:
        return [
            key
            for key, rec in self.registry.records.items()
            if migration.new_fn.owner_for(key) != rec.owner
        ]

    def begin(self, migration: Migration) -> list[Key]:
        """Start migrating all keys whose owner changes under the new version.

        Idempotent: a migration id that is already active or completed is a
        no-op, so duplicate migration requests never copy state twice.
        """
        if (
            migration.migration_id in self.registry.completed_migrations
            or migration.migration_id in self.registry.active_migrations
        ):
            return []
        self.registry.active_migrations.add(migration.migration_id)
        moved = []
        for key in self.affected_keys(migration):
            self.begin_key(
                migration.migration_id,
                key,
                migration.new_fn.owner_for(key),
                migration.new_fn.version,
            )
            moved.append(key)
        return moved

    def begin_key(self, migration_id: str, key: Key, to_owner: str, new_version: int) -> None:
        pt = self.registry.begin_transfer(migration_id, key, to_owner, new_version)
        slice_ = self.stores[pt.from_owner].export_key(key)
        self.stores[to_owner].import_key(key, slice_)
        self._log(
            {
                "kind": "transfer_begin",
                "migration_id": migration_id,
                "key": list(key),
                "from": pt.from_owner,
                "to": to_owner,
                "version": new_version,
            }
        )

    def commit(self, migration_id: str) -> None:
        """Forward inflight old-route events, then flip ownership per key.

        If forwarding fails for any key, all still-pending transfers are
        rolled back and a RoutingError is raised.
        """
        keys = [
            key
            for key, pt in self.registry.pending.items()
            if pt.migration_id == migration_id
        ]
        try:
            for key in keys:
                self.commit_key(migration_id, key)
        except Exception as exc:
            self.rollback(migration_id)
            raise RoutingError(
                f"migration {migration_id} failed, ownership rolled back: {exc}"
            ) from exc
        self.registry.active_migrations.discard(migration_id)
        self.registry.completed_migrations.add(migration_id)
        self._log({"kind": "migration_done", "migration_id": migration_id})

    def commit_key(self, migration_id: str, key: Key) -> None:
        pt = self.registry.pending[key]
        for event in pt.inflight:
            # Forward old-route events to the new owner; idempotent apply
            # dedups anything already covered by the copied slice.
            self.stores[pt.to_owner].apply(event, epoch=pt.new_epoch)
        self.registry.commit_transfer(migration_id, key)
        self.stores[pt.from_owner].remove_key(key)
        self._log(
            {
                "kind": "transfer_commit",
                "migration_id": migration_id,
                "key": list(key),
                "to": pt.to_owner,
            }
        )

    def rollback(self, migration_id: str) -> None:
        keys = [
            key
            for key, pt in self.registry.pending.items()
            if pt.migration_id == migration_id
        ]
        for key in keys:
            self.rollback_key(migration_id, key)
        self.registry.active_migrations.discard(migration_id)

    def rollback_key(self, migration_id: str, key: Key) -> None:
        pt = self.registry.rollback_transfer(migration_id, key)
        # Discard the copied state at the new owner. Inflight events were
        # already applied at the old owner, so nothing is lost.
        self.stores[pt.to_owner].remove_key(key)
        self._log(
            {
                "kind": "transfer_rollback",
                "migration_id": migration_id,
                "key": list(key),
                "to": pt.to_owner,
            }
        )
