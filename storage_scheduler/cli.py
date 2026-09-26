"""Command-line interface for the storage scheduler.

Usage:
    python -m storage_scheduler INSTANCE.json [--budget B]
        [--iteration-budget N] [--config-budget M] [--json]
"""

from __future__ import annotations

import argparse
import json
import sys
from fractions import Fraction

from .model import Instance, to_fraction
from .solver import SolveResult, solve


def result_to_dict(result: SolveResult) -> dict:
    return {
        "status": result.status,
        "objective": (str(result.objective)
                      if result.objective is not None else None),
        "budget_decision": result.budget_decision,
        "iterations": result.iterations,
        "configs_evaluated": result.configs_evaluated,
        "configs_total": result.configs_total,
        "schedule": None if result.schedule is None else [
            {
                "period": t + 1,
                "price": str(ps.price),
                "demand": str(ps.demand),
                "charge_grid": str(ps.charge_grid),
                "discharge_delivered": str(ps.discharge_delivered),
                "soc_after": str(ps.soc_after),
            }
            for t, ps in enumerate(result.schedule)
        ],
    }


def _approx(value: Fraction) -> str:
    return f"{float(value):.4f}"


def print_human(result: SolveResult, budget: Fraction | None) -> None:
    print(f"Status: {result.status}")
    if result.objective is not None:
        print(f"Net cost (objective): {result.objective}"
              f"  (~{_approx(result.objective)})")
    if budget is not None:
        print(f"Budget {budget}: {result.budget_decision}")
    print(f"Iterations: {result.iterations}, "
          f"configs: {result.configs_evaluated}/{result.configs_total}")
    if result.schedule is None:
        return
    header = ("Period", "Price", "Demand", "Charge(grid)",
              "Discharge(delivered)", "SoC")
    rows = [
        (str(t + 1), str(ps.price), str(ps.demand), str(ps.charge_grid),
         str(ps.discharge_delivered), str(ps.soc_after))
        for t, ps in enumerate(result.schedule)
    ]
    widths = [max(len(header[i]), *(len(r[i]) for r in rows))
              for i in range(len(header))]
    fmt = " | ".join(f"{{:>{w}}}" for w in widths)
    print(fmt.format(*header))
    print("-+-".join("-" * w for w in widths))
    for row in rows:
        print(fmt.format(*row))


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        prog="storage_scheduler",
        description="Exact storage charge/discharge scheduler "
                    "(no external solver, exact rational arithmetic).")
    parser.add_argument("instance", help="path to the instance JSON file")
    parser.add_argument("--budget",
                        help="ask: is there a schedule with cost <= budget? "
                             "Accepts integers, decimals, or fractions like -9/2")
    parser.add_argument("--iteration-budget", type=int, default=100_000,
                        help="total cycle-canceling iterations (default 100000)")
    parser.add_argument("--config-budget", type=int, default=4096,
                        help="max number of negative-price mode configurations "
                             "to enumerate (default 4096)")
    parser.add_argument("--json", action="store_true",
                        help="print the result as JSON")
    args = parser.parse_args(argv)

    try:
        with open(args.instance, "r", encoding="utf-8") as fh:
            inst = Instance.from_dict(json.load(fh))
        budget = to_fraction(args.budget) if args.budget is not None else None
    except (OSError, ValueError, KeyError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2

    result = solve(inst, budget=budget,
                   iteration_budget=args.iteration_budget,
                   config_budget=args.config_budget)
    if args.json:
        print(json.dumps(result_to_dict(result), indent=2))
    else:
        print_human(result, budget)
    return 0


if __name__ == "__main__":
    sys.exit(main())
