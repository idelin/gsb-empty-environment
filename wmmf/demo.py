"""Runnable sample: python3.11 -m wmmf.demo

Scenario
--------
Links: L1=10, L2=4 (plus L3=9, L4=5 used by a weighted multi-path demand).
Demands:
  A  w=1 cap=100 route {L1}
  B  w=1 cap=100 route {L1, L2}
  C  w=1 cap=100 route {L2}
  D  w=2 cap=6   paths [[L3, L4], [L4]]  (multi-path; L4 counted once)

Then: demand C withdraws and link L2 fails, showing incremental
re-allocation with unfreezing, followed by exhaustive grid verification.
"""

from __future__ import annotations

from fractions import Fraction

from .allocator import allocate
from .certificate import build_certificate, check_feasible, verify_certificate
from .gridverify import Status, suggest_step, verify_optimal
from .model import Demand, Network
from .reallocator import Reallocator


def build_network() -> Network:
    return Network(
        capacities={"L1": 10, "L2": 4, "L3": 9, "L4": 5},
        demands={
            "A": Demand("A", weight=1, cap=100, links={"L1"}),
            "B": Demand("B", weight=1, cap=100, links={"L1", "L2"}),
            "C": Demand("C", weight=1, cap=100, links={"L2"}),
            "D": Demand.from_paths("D", weight=2, cap=6, paths=[["L3", "L4"], ["L4"]]),
        },
    )


def show_allocation(network: Network, allocation, title: str) -> None:
    print(f"== {title} ==")
    norms = allocation.normalized_rates(network)
    for demand_id in sorted(allocation.rates):
        demand = network.demands[demand_id]
        bottleneck = allocation.bottleneck_of[demand_id]
        reason = f"link {bottleneck}" if bottleneck else "cap"
        print(
            f"  {demand_id}: rate={str(allocation.rates[demand_id]):>4} "
            f"normalized={str(norms[demand_id]):>4} "
            f"cap={demand.cap} frozen-by={reason}"
        )
    for link, load in sorted(network.link_loads(allocation.rates).items()):
        print(f"  link {link}: load={load}/{network.capacities[link]}")
    print("  freeze order:")
    for event in allocation.freeze_order:
        print(
            f"    round {event.round_index}: level={event.level} "
            f"frozen={list(event.demands)} "
            f"bottlenecks={list(event.bottleneck_links)} "
            f"capped={list(event.capped)}"
        )
    errors = check_feasible(network, allocation.rates)
    print(f"  feasibility: {'OK' if not errors else errors}")


def show_certificate(network: Network, allocation) -> None:
    certificate = build_certificate(network, allocation.rates)
    ok = verify_certificate(network, allocation.rates, certificate)
    print(f"== fairness certificate (verified={ok}) ==")
    for demand_id in sorted(certificate.witnesses):
        witness = certificate.witnesses[demand_id]
        print(
            f"  {demand_id}: unsatisfied; bottleneck link {witness.link} is "
            f"saturated and {demand_id} holds the max normalized rate "
            f"{witness.normalized_rate} on it -> raising {demand_id} forces "
            f"down a demand with normalized rate <= {witness.normalized_rate}"
        )
    capped = sorted(set(network.demands) - set(certificate.witnesses))
    print(f"  satisfied at cap (no witness needed): {capped}")


def show_grid_verification(network: Network, allocation, budget: int = 2_000_000) -> None:
    step = suggest_step(network, allocation.rates)
    result = verify_optimal(network, allocation.rates, step=step, budget=budget)
    print("== exhaustive grid verification ==")
    print(f"  grid step: {step}, nodes visited: {result.nodes_visited}")
    print(f"  candidate lex key: {result.candidate_key}")
    print(f"  best grid lex key: {result.best_grid_key}")
    print(f"  status: {result.status.value}")


def main() -> None:
    network = build_network()
    allocation = allocate(network)
    show_allocation(network, allocation, "initial weighted max-min fair allocation")
    show_certificate(network, allocation)
    show_grid_verification(network, allocation)

    print()
    reallocator = Reallocator(build_network())

    result = reallocator.withdraw_demand("C")
    print("== incremental: demand C withdraws (saturated set changes -> unfreeze) ==")
    for demand_id, (old, new) in sorted(result.changed.items()):
        print(f"  {demand_id}: {old} -> {new}")
    show_allocation(reallocator.network, result.allocation, "after withdrawal of C")
    show_certificate(reallocator.network, result.allocation)

    result = reallocator.fail_link("L2")
    print("== incremental: link L2 fails (capacity -> 0) ==")
    for demand_id, (old, new) in sorted(result.changed.items()):
        print(f"  {demand_id}: {old} -> {new}")
    show_allocation(reallocator.network, result.allocation, "after failure of L2")
    show_certificate(reallocator.network, result.allocation)

    result = reallocator.set_capacity("L2", 3)
    print("== incremental: link L2 partially recovers (capacity -> 3) ==")
    for demand_id, (old, new) in sorted(result.changed.items()):
        print(f"  {demand_id}: {old} -> {new}")
    show_allocation(reallocator.network, result.allocation, "after L2 recovery to 3")
    show_certificate(reallocator.network, result.allocation)

    print()
    cap_net = Network(
        {"L1": 10},
        {"A": Demand("A", 1, 3, {"L1"}), "B": Demand("B", 1, 100, {"L1"})},
    )
    cap_alloc = allocate(cap_net)
    show_allocation(cap_net, cap_alloc, "demand at its cap stops; the rest fills on")
    show_certificate(cap_net, cap_alloc)


if __name__ == "__main__":
    main()
