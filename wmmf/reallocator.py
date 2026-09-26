"""Incremental re-allocation after link failures and demand withdrawals.

Correctness rule: when the saturated set can change, previously frozen
demands must be *unfrozen* -- the water-filling restarts from zero on the
updated network.  Simply continuing to deduct from stale residuals would
strand demands that were frozen by bottlenecks which no longer exist.

Each mutating operation therefore recomputes the full allocation and reports
exactly which demand rates changed.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .allocator import Allocation, allocate
from .model import Demand, Network


@dataclass
class Reallocation:
    event: str
    allocation: Allocation
    changed: dict[str, tuple[Fraction, Fraction]]  # demand -> (old, new)
    withdrawn: tuple[str, ...] = ()


class Reallocator:
    """Stateful wrapper that re-solves after each network change."""

    def __init__(self, network: Network):
        self.network = network
        self.allocation = allocate(network)

    def _recompute(self, event: str, withdrawn: tuple[str, ...] = ()) -> Reallocation:
        old_rates = self.allocation.rates
        # Full unfreeze: allocate() restarts the water-filling from zero.
        self.allocation = allocate(self.network)
        changed = {
            d: (old_rates.get(d, Fraction(0)), new)
            for d, new in self.allocation.rates.items()
            if old_rates.get(d, Fraction(0)) != new
        }
        return Reallocation(event, self.allocation, changed, withdrawn)

    def set_capacity(self, link: str, capacity) -> Reallocation:
        capacity = Fraction(capacity)
        if capacity < 0:
            raise ValueError("capacity must be non-negative")
        if link not in self.network.capacities:
            raise KeyError(f"unknown link {link!r}")
        self.network.capacities[link] = capacity
        return self._recompute(f"set_capacity({link}={capacity})")

    def fail_link(self, link: str) -> Reallocation:
        """Link failure == capacity drops to zero; routes are unchanged."""
        if link not in self.network.capacities:
            raise KeyError(f"unknown link {link!r}")
        self.network.capacities[link] = Fraction(0)
        return self._recompute(f"fail_link({link})")

    def withdraw_demand(self, demand_id: str) -> Reallocation:
        if demand_id not in self.network.demands:
            raise KeyError(f"unknown demand {demand_id!r}")
        del self.network.demands[demand_id]
        return self._recompute(f"withdraw_demand({demand_id})", withdrawn=(demand_id,))

    def add_demand(self, demand: Demand) -> Reallocation:
        if demand.demand_id in self.network.demands:
            raise KeyError(f"demand {demand.demand_id!r} already exists")
        unknown = demand.links - self.network.capacities.keys()
        if unknown:
            raise ValueError(f"demand uses unknown links {sorted(unknown)}")
        self.network.demands[demand.demand_id] = demand
        return self._recompute(f"add_demand({demand.demand_id})")
