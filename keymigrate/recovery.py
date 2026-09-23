"""Replay recovery: rebuild registry and stores from the event log."""
from __future__ import annotations

from .events import ChangeEvent
from .migration import MigrationCoordinator
from .ownership import OwnershipRegistry
from .partition import PartitionFunction
from .router import Router
from .store import KeyStateStore


def replay(
    records: list[dict],
    registry: OwnershipRegistry,
    stores: dict[str, KeyStateStore],
    functions: dict[int, PartitionFunction],
) -> None:
    """Deterministically rebuild state by feeding log records through the same
    router/coordinator code paths as the live system (logging disabled)."""
    router = Router(registry, stores, log=None)
    coordinator = MigrationCoordinator(registry, stores, log=None)
    for rec in records:
        kind = rec["kind"]
        if kind == "route":
            router.route(ChangeEvent.from_dict(rec["event"]), functions[rec["version"]])
        elif kind == "transfer_begin":
            coordinator.begin_key(
                rec["migration_id"], tuple(rec["key"]), rec["to"], rec["version"]
            )
        elif kind == "transfer_commit":
            coordinator.commit_key(rec["migration_id"], tuple(rec["key"]))
        elif kind == "transfer_rollback":
            coordinator.rollback_key(rec["migration_id"], tuple(rec["key"]))
        elif kind == "migration_done":
            registry.active_migrations.discard(rec["migration_id"])
            registry.completed_migrations.add(rec["migration_id"])
        else:
            raise ValueError(f"unknown log record kind: {kind!r}")
