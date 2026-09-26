"""Progressive-filling (water-filling) weighted max-min fair allocator.

All unfrozen demands raise their *normalized* rate rate/weight at the same
speed.  A demand freezes when

* a link on its route saturates (bottleneck), or
* it reaches its cap -- capped demands stop receiving allocation.

The output records the freeze order and, for each round, the bottleneck links
that saturated.  Arithmetic is exact (Fraction); no solver is involved.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .model import Network


@dataclass(frozen=True)
class FreezeEvent:
    """One filling round.

    level            normalized rate shared by all demands still active in
                     this round (they all fill at the same normalized speed).
    demands          demands frozen in this round.
    bottleneck_links links that saturated in this round.
    capped           demands frozen because they hit their cap.
    """

    round_index: int
    level: Fraction
    demands: tuple[str, ...]
    bottleneck_links: tuple[str, ...]
    capped: tuple[str, ...]


@dataclass
class Allocation:
    """Result of the water-filling.

    rates         final rate per demand.
    freeze_order  sequence of FreezeEvent, in the order demands froze.
    bottleneck_of demand -> saturated link that froze it (None if it stopped
                  purely because of its cap).
    """

    rates: dict[str, Fraction]
    freeze_order: tuple[FreezeEvent, ...]
    bottleneck_of: dict[str, str | None]

    def normalized_rates(self, network: Network) -> dict[str, Fraction]:
        return network.normalized_rates(self.rates)


def allocate(network: Network) -> Allocation:
    demands = network.demands
    rates: dict[str, Fraction] = {d: Fraction(0) for d in demands}
    residual: dict[str, Fraction] = dict(network.capacities)
    frozen: set[str] = set()
    events: list[FreezeEvent] = []
    bottleneck_of: dict[str, str | None] = {}

    round_index = 0
    while True:
        active = [d for d in demands if d not in frozen]
        if not active:
            break

        # Normalized time until each link saturates, given active demands.
        link_delta: dict[str, Fraction] = {}
        for link in network.capacities:
            wsum = sum(
                (demands[d].weight for d in active if link in demands[d].links),
                Fraction(0),
            )
            if wsum > 0:
                link_delta[link] = residual[link] / wsum

        # Normalized time until each active demand hits its cap.
        delta: Fraction | None = min(link_delta.values(), default=None)
        for d in active:
            to_cap = (demands[d].cap - rates[d]) / demands[d].weight
            if delta is None or to_cap < delta:
                delta = to_cap

        # Advance every active demand by delta * weight.
        for d in active:
            rates[d] += delta * demands[d].weight
        for link in network.capacities:
            wsum = sum(
                (demands[d].weight for d in active if link in demands[d].links),
                Fraction(0),
            )
            residual[link] -= delta * wsum

        saturated = tuple(sorted(link for link, dt in link_delta.items() if dt == delta))
        saturated_set = set(saturated)
        capped = tuple(sorted(d for d in active if rates[d] == demands[d].cap))
        newly: set[str] = set(capped)
        for d in active:
            if d not in newly and demands[d].links & saturated_set:
                newly.add(d)

        for d in newly:
            witness = next(
                (link for link in saturated if link in demands[d].links), None
            )
            bottleneck_of[d] = witness  # None => stopped at its cap

        level = rates[active[0]] / demands[active[0]].weight
        events.append(
            FreezeEvent(
                round_index=round_index,
                level=level,
                demands=tuple(sorted(newly)),
                bottleneck_links=saturated,
                capped=capped,
            )
        )
        frozen |= newly
        round_index += 1

    return Allocation(rates, tuple(events), bottleneck_of)
