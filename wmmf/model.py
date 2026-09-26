"""Data model: shared links with capacities, demands with weights and caps.

All numeric data are exact rationals (fractions.Fraction); synthetic integer
inputs are converted automatically.  A demand occupies a *route*: a set of
shared links.  The demand's rate x_d is counted **once per link** no matter
how many of its paths traverse that link, so multi-path demands are never
double charged.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Iterable, Mapping, Sequence


class Demand:
    """A single traffic demand.

    Attributes
    ----------
    demand_id : unique name.
    weight    : positive weight; fairness is on the *normalized* rate
                rate / weight.
    cap       : finite upper bound; a demand at its cap receives no more.
    links     : frozenset of link ids forming the route (union of paths).
    """

    __slots__ = ("demand_id", "weight", "cap", "links")

    def __init__(self, demand_id: str, weight, cap, links: Iterable[str]):
        self.demand_id = str(demand_id)
        self.weight = Fraction(weight)
        self.cap = Fraction(cap)
        self.links = frozenset(links)

    @classmethod
    def from_paths(cls, demand_id: str, weight, cap, paths: Sequence[Sequence[str]]) -> "Demand":
        """Build a demand from several paths; shared links are merged so the
        rate is counted once per link (never once per path)."""
        merged: set[str] = set()
        for path in paths:
            if not path:
                raise ValueError("empty path is not a valid route")
            merged.update(path)
        return cls(demand_id, weight, cap, merged)

    def normalized(self, rate: Fraction) -> Fraction:
        return Fraction(rate) / self.weight

    def __repr__(self) -> str:  # pragma: no cover - debugging aid
        return (
            f"Demand({self.demand_id!r}, w={self.weight}, cap={self.cap}, "
            f"links={sorted(self.links)})"
        )


class Network:
    """Capacitated shared links plus the set of competing demands."""

    def __init__(self, capacities: Mapping[str, object], demands: Mapping[str, Demand]):
        self.capacities: dict[str, Fraction] = {str(k): Fraction(v) for k, v in capacities.items()}
        self.demands: dict[str, Demand] = dict(demands)
        self.validate()

    def validate(self) -> None:
        for link, cap in self.capacities.items():
            if cap < 0:
                raise ValueError(f"link {link!r} has negative capacity {cap}")
        for demand in self.demands.values():
            if demand.demand_id not in self.demands:
                raise ValueError(f"demand key mismatch for {demand.demand_id!r}")
            if demand.weight <= 0:
                raise ValueError(f"demand {demand.demand_id!r} has non-positive weight")
            if demand.cap < 0:
                raise ValueError(f"demand {demand.demand_id!r} has negative cap")
            if not demand.links:
                raise ValueError(f"demand {demand.demand_id!r} has an empty route")
            unknown = demand.links - self.capacities.keys()
            if unknown:
                raise ValueError(
                    f"demand {demand.demand_id!r} uses unknown links {sorted(unknown)}"
                )

    def link_loads(self, rates: Mapping[str, Fraction]) -> dict[str, Fraction]:
        """Load on every link; each demand contributes its rate once per link."""
        loads = {link: Fraction(0) for link in self.capacities}
        for demand_id, rate in rates.items():
            demand = self.demands[demand_id]
            for link in demand.links:
                loads[link] += Fraction(rate)
        return loads

    def normalized_rates(self, rates: Mapping[str, Fraction]) -> dict[str, Fraction]:
        return {
            demand_id: self.demands[demand_id].normalized(rate)
            for demand_id, rate in rates.items()
        }
