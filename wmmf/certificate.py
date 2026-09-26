"""Weighted max-min fairness certificate.

Certificate claim
-----------------
For every *unsatisfied* demand d (rate < cap) there is a witness: a link l on
d's route such that

1. l is saturated (load == capacity), and
2. d attains the maximum normalized rate on l
   (rate_d / w_d >= rate_o / w_o for every demand o routed over l).

Consequently, raising any unsatisfied demand forces some demand whose
normalized rate is **no higher** to decrease: link l is full, so extra rate
for d must be taken from other demands on l, all of which have normalized
rate <= rate_d / w_d.  Demands already at their cap are satisfied and need
no witness.

The checker is independent of the allocator: it recomputes loads from the
given rates and never trusts allocator internals.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Mapping

from .model import Network


@dataclass(frozen=True)
class Witness:
    demand: str
    link: str
    normalized_rate: Fraction


@dataclass
class Certificate:
    witnesses: dict[str, Witness]  # one entry per unsatisfied demand


def check_feasible(network: Network, rates: Mapping[str, Fraction]) -> list[str]:
    """Return a list of feasibility violations (empty iff feasible).

    Checks: rates are non-negative, caps are respected, and no link's
    capacity is exceeded (multi-link demands are charged once per link).
    """
    errors: list[str] = []
    for demand_id, demand in network.demands.items():
        rate = Fraction(rates.get(demand_id, Fraction(0)))
        if rate < 0:
            errors.append(f"demand {demand_id!r} has negative rate {rate}")
        if rate > demand.cap:
            errors.append(
                f"demand {demand_id!r} rate {rate} exceeds cap {demand.cap}"
            )
    unknown = set(rates) - set(network.demands)
    for demand_id in sorted(unknown):
        errors.append(f"rate given for unknown demand {demand_id!r}")
    loads = network.link_loads(rates)
    for link, cap in network.capacities.items():
        if loads[link] > cap:
            errors.append(
                f"link {link!r} load {loads[link]} exceeds capacity {cap}"
            )
    return errors


def _is_witness(network: Network, rates: Mapping[str, Fraction], demand_id: str, link: str) -> bool:
    demand = network.demands[demand_id]
    if link not in demand.links:
        return False
    loads = network.link_loads(rates)
    if loads[link] != network.capacities[link]:
        return False  # link must be saturated
    norm_d = demand.normalized(Fraction(rates[demand_id]))
    for other_id, other in network.demands.items():
        if link in other.links:
            if other.normalized(Fraction(rates[other_id])) > norm_d:
                return False
    return True


def build_certificate(network: Network, rates: Mapping[str, Fraction]) -> Certificate:
    """Construct the certificate for an allocation; raises if none exists."""
    errors = check_feasible(network, rates)
    if errors:
        raise ValueError("allocation is infeasible: " + "; ".join(errors))
    witnesses: dict[str, Witness] = {}
    for demand_id, demand in network.demands.items():
        rate = Fraction(rates[demand_id])
        if rate >= demand.cap:
            continue  # satisfied at its cap; no witness required
        for link in sorted(demand.links):
            if _is_witness(network, rates, demand_id, link):
                witnesses[demand_id] = Witness(
                    demand_id, link, demand.normalized(rate)
                )
                break
        else:
            raise ValueError(
                f"no bottleneck witness for unsatisfied demand {demand_id!r}: "
                "allocation is not weighted max-min fair"
            )
    return Certificate(witnesses)


def verify_certificate(
    network: Network, rates: Mapping[str, Fraction], certificate: Certificate
) -> bool:
    """Independently verify a certificate against a candidate allocation."""
    if check_feasible(network, rates):
        return False
    for demand_id, demand in network.demands.items():
        rate = Fraction(rates[demand_id])
        if rate >= demand.cap:
            continue
        witness = certificate.witnesses.get(demand_id)
        if witness is None or witness.demand != demand_id:
            return False
        if not _is_witness(network, rates, demand_id, witness.link):
            return False
    return True
