"""Runnable sample: enumerate Pareto-optimal plan combinations.

Usage (from the repository root):

    python3.11 examples/run_sample.py [--max-results N]

The data is synthetic and mixes integers and rationals on purpose.
"""

from __future__ import annotations

import argparse
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), os.pardir))

from pareto_plans import Constraints, Plan, enumerate_combinations


def sample_plans():
    # plan_id, task_id, cost, latency, energy  (all objectives minimised)
    rows = [
        ("E1", "encode", 4, 6, 3),
        ("E1b", "encode", 4, 6, 3),   # identical objectives to E1 -> id tie-break
        ("E2", "encode", 6, 4, 3),
        ("E3", "encode", 9, 3, "5/2"),
        ("C1", "compress", 3, 5, 4),
        ("C2", "compress", 5, 3, 4),
        ("C3", "compress", 8, 2, "3/2"),
        ("T1", "transmit", 2, 4, 5),
        ("T2", "transmit", 4, 2, 5),
        ("T3", "transmit", 6, 2, 3),
    ]
    return [Plan.of(*row) for row in rows]


def fmt(value):
    return str(value)  # Fraction prints as "a/b", integers as "n"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--max-results",
        type=int,
        default=100,
        help="per-category result cap (truncation is reported explicitly)",
    )
    args = parser.parse_args()

    plans = sample_plans()
    constraints = Constraints.of(max_cost=14, max_latency=13, max_energy=12)
    result = enumerate_combinations(plans, constraints, max_results=args.max_results)

    print("=== Pareto plan enumeration (exact rational arithmetic, no solver) ===")
    print(f"tasks: 3 (encode, compress, transmit), plans: {len(plans)}")
    print(
        "constraints: "
        f"cost <= {fmt(constraints.max_cost)} (budget), "
        f"latency <= {fmt(constraints.max_latency)} (hard), "
        f"energy <= {fmt(constraints.max_energy)} (hard)"
    )
    print(f"combinations considered: {result.total_combinations}")
    print(f"hard-feasible combinations: {result.hard_feasible_count}")
    print()

    for cat in result.categories():
        flag = "yes" if cat.truncated else "no"
        print(
            f"[{cat.category.value}] showing {len(cat.combinations)} "
            f"of {cat.total_count} (truncated: {flag}, omitted: {cat.omitted_count})"
        )
        for combo in cat.combinations:
            ids = "+".join(combo.combo_id)
            print(
                f"  {ids:<14} cost={fmt(combo.cost):<4} "
                f"latency={fmt(combo.latency):<4} energy={fmt(combo.energy)}"
            )
        print()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
