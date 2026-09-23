"""Recovery by replaying the commit log in commit_seq order."""
from __future__ import annotations

from .events import ChangeEvent
from .partition import PartitionFunction
from .registry import OwnershipRegistry
from .router import Router
from .store import PartitionStore


def build_system(pf: PartitionFunction, num_stores: int):
    registry = OwnershipRegistry(pf)
    stores = {i: PartitionStore(i, registry) for i in range(num_stores)}
    router = Router(registry, stores)
    return registry, stores, router


def replay(
    events: list[ChangeEvent],
    pf: PartitionFunction,
    num_stores: int,
    from_seq: int = 0,
):
    """Rebuild state from the log; safe to re-run (watermark dedupe)."""
    registry, stores, router = build_system(pf, num_stores)
    for event in sorted(events, key=lambda e: e.commit_seq):
        if event.commit_seq > from_seq:
            router.route(event)
    return registry, stores, router
