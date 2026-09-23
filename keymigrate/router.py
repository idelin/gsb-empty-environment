"""Event router: delivers change events to the current valid owner."""
from __future__ import annotations

from .events import ChangeEvent, EventLog
from .ownership import OwnershipRegistry
from .partition import PartitionFunction
from .store import KeyStateStore


class Router:
    def __init__(
        self,
        registry: OwnershipRegistry,
        stores: dict[str, KeyStateStore],
        log: EventLog | None = None,
    ):
        self.registry = registry
        self.stores = stores
        self.log = log

    def route(self, event: ChangeEvent, fn: PartitionFunction) -> str:
        """Route one event; returns the owner that accepted it.

        While a transfer for the key is pending, the event still goes to the
        old (only valid) owner and is additionally captured in the transfer's
        inflight buffer, so old-route events issued before the switch are
        forwarded on commit instead of being lost.
        """
        key = event.key
        rec = self.registry.records.get(key)
        if rec is None:
            rec = self.registry.ensure_key(key, fn.owner_for(key), fn.version)
        pt = self.registry.pending.get(key)
        if pt is not None:
            pt.inflight.append(event)
        self.stores[rec.owner].apply(event, epoch=rec.epoch)
        if self.log is not None:
            self.log.append(
                {
                    "kind": "route",
                    "event": event.to_dict(),
                    "owner": rec.owner,
                    "epoch": rec.epoch,
                    "version": fn.version,
                }
            )
        return rec.owner
