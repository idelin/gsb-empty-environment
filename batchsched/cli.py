"""Command line interface: solve an instance or decide a budget question."""

from __future__ import annotations

import argparse
import json
import sys
from fractions import Fraction

from .model import InstanceError, load_instance, parse_number
from . import solver


def _frac_str(value):
    return str(value) if value is not None else None


def _schedule_lines(inst, batch_ids):
    lines = []
    by_id = {j.jid: j for j in inst.jobs}
    t = Fraction(0)
    for k, ids in enumerate(batch_ids, start=1):
        jobs = [by_id[i] for i in ids]
        ptime = max(j.time for j in jobs)
        size = sum((j.size for j in jobs), Fraction(0))
        lines.append(
            f"  batch {k}: jobs=[{', '.join(ids)}] family={jobs[0].family} "
            f"size={size} start={t} end={t + ptime}"
        )
        t += ptime
    return lines


def _emit(result, inst, as_json):
    if as_json:
        payload = {
            "status": result.status,
            "total_cost": _frac_str(result.cost),
            "batches": result.batches,
            "nodes_explored": result.nodes,
            "truncated": result.truncated,
            "message": result.message,
        }
        print(json.dumps(payload, indent=2))
        return
    print(f"status: {result.status}")
    if result.cost is not None:
        print(f"total_cost: {result.cost}")
    if result.batches:
        print(f"batches: {len(result.batches)}")
        for line in _schedule_lines(inst, result.batches):
            print(line)
    print(f"nodes_explored: {result.nodes}")
    if result.message:
        print(f"note: {result.message}")


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        prog="batchsched",
        description="Exact batch scheduling: compatibility families, batch "
        "setup cost, capacity limit, indivisible jobs, deadlines.",
    )
    parser.add_argument("instance", help="path to a JSON instance file")
    parser.add_argument(
        "--node-limit", type=int, default=solver.DEFAULT_NODE_LIMIT,
        help="max DP transitions before reporting feasible/undecided",
    )
    parser.add_argument(
        "--budget", type=str, default=None,
        help="decide whether minimum total cost <= budget (int or p/q)",
    )
    parser.add_argument("--json", action="store_true", help="machine-readable output")
    args = parser.parse_args(argv)

    try:
        with open(args.instance, "r", encoding="utf-8") as fh:
            data = json.load(fh)
    except (OSError, json.JSONDecodeError) as exc:
        print(f"error: cannot read instance: {exc}", file=sys.stderr)
        return 2

    try:
        inst = load_instance(data)
    except InstanceError as exc:
        # covers oversize-job rejection and any invalid field
        if args.json:
            print(json.dumps({"status": "rejected", "reason": str(exc)}, indent=2))
        else:
            print("status: rejected")
            print(f"reason: {exc}")
        return 2

    if args.budget is not None:
        try:
            budget = parse_number(args.budget)
        except InstanceError as exc:
            print(f"error: invalid --budget: {exc}", file=sys.stderr)
            return 2
        result = solver.decide_budget(inst, budget, args.node_limit)
    else:
        result = solver.solve(inst, args.node_limit)
    _emit(result, inst, args.json)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
