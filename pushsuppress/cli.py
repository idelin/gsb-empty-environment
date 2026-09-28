"""Command line interface for the push suppression decision component.

Examples
--------
    python -m pushsuppress solve -i examples/small.json
    python -m pushsuppress enumerate -i examples/small.json
    python -m pushsuppress check -i examples/check_example.json

Input may also be piped on stdin when ``-i`` is omitted.
"""

from __future__ import annotations

import argparse
import json
import sys
from typing import Any

from .checkers import verify_schedule
from .model import InputError, parse_instance
from .solver import DEFAULT_NODE_LIMIT, enumerate_schedules, objective_value, solve, tie_key


def _load(path: str | None) -> Any:
    if path:
        with open(path, "r", encoding="utf-8") as handle:
            return json.load(handle)
    return json.load(sys.stdin)


def _schedule_from_assignment(raw: Any) -> dict[str, int]:
    if not isinstance(raw, dict):
        raise InputError("'schedule' must be an object mapping id -> send slot")
    schedule: dict[str, int] = {}
    for nid, slot in raw.items():
        if isinstance(slot, bool) or not isinstance(slot, int) or slot < 0:
            raise InputError(f"schedule entry for {nid!r} must be a non-negative integer")
        schedule[str(nid)] = slot
    return schedule


def _cmd_solve(data: dict, node_limit: int) -> int:
    instance = parse_instance(data)
    result = solve(instance, node_limit=node_limit)
    payload = {
        "feasible": result.feasible,
        "proven_optimal": result.proven_optimal,
        "revenue": result.revenue,
        "schedule": [
            {"id": nid, "send_slot": slot} for nid, slot in result.schedule.items()
        ],
        "tie_key": list(tie_key(instance, result.schedule)),
        "stats": {
            "nodes_explored": result.nodes_explored,
            "nodes_pruned": result.nodes_pruned,
            "node_limit": result.node_limit,
        },
    }
    if result.reason:
        payload["warning"] = result.reason
    json.dump(payload, sys.stdout, indent=2, ensure_ascii=False)
    sys.stdout.write("\n")
    return 0


def _cmd_check(data: dict) -> int:
    if not isinstance(data, dict) or "instance" not in data or "schedule" not in data:
        raise InputError("check input must be an object with 'instance' and 'schedule'")
    instance = parse_instance(data["instance"])
    schedule = _schedule_from_assignment(data["schedule"])
    violations = verify_schedule(instance, schedule)
    payload = {
        "feasible": not violations,
        "revenue": objective_value(instance, schedule) if not violations else None,
        "violations": [
            {
                "constraint": v.constraint,
                "detail": v.detail,
                "notification_ids": list(v.notification_ids),
                **({"slot": v.slot} if v.slot is not None else {}),
                **({"channel": v.channel} if v.channel is not None else {}),
            }
            for v in violations
        ],
    }
    json.dump(payload, sys.stdout, indent=2, ensure_ascii=False)
    sys.stdout.write("\n")
    return 0 if not violations else 1


def _cmd_enumerate(data: dict, node_limit: int, include_infeasible: bool) -> int:
    instance = parse_instance(data)
    schedules = enumerate_schedules(
        instance, only_feasible=not include_infeasible, node_limit=node_limit
    )
    records = []
    best_revenue = None
    for schedule in schedules:
        revenue = objective_value(instance, schedule)
        records.append(
            {
                "schedule": [
                    {"id": nid, "send_slot": schedule[nid]}
                    for nid in sorted(schedule)
                ],
                "revenue": revenue,
                "tie_key": list(tie_key(instance, schedule)),
            }
        )
    best_revenue = max((r["revenue"] for r in records), default=0.0)
    optimal_key = None
    feasible_records = [r for r in records]
    for record in feasible_records:
        if record["revenue"] == best_revenue:
            if optimal_key is None or tuple(record["tie_key"]) < tuple(optimal_key):
                optimal_key = record["tie_key"]
    for record in records:
        record["optimal"] = (
            record["revenue"] == best_revenue and record["tie_key"] == optimal_key
        )
    payload = {
        "count": len(records),
        "optimal_revenue": best_revenue,
        "schedules": records,
    }
    json.dump(payload, sys.stdout, indent=2, ensure_ascii=False)
    sys.stdout.write("\n")
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="python -m pushsuppress",
        description="Multi-channel push suppression decision (exact enumeration).",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    def add_common(p: argparse.ArgumentParser) -> None:
        p.add_argument("-i", "--input", help="input JSON file (default: stdin)")
        p.add_argument(
            "--node-limit",
            type=int,
            default=DEFAULT_NODE_LIMIT,
            help="safety cap on explored search nodes",
        )

    p_solve = sub.add_parser("solve", help="find the revenue-maximal schedule")
    add_common(p_solve)

    p_enum = sub.add_parser("enumerate", help="list every feasible schedule")
    add_common(p_enum)
    p_enum.add_argument(
        "--include-infeasible",
        action="store_true",
        help="also list assignments that violate constraints",
    )

    p_check = sub.add_parser("check", help="re-check a given schedule")
    p_check.add_argument("-i", "--input", help="input JSON file (default: stdin)")
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    try:
        data = _load(args.input)
        if args.command == "solve":
            return _cmd_solve(data, args.node_limit)
        if args.command == "enumerate":
            return _cmd_enumerate(data, args.node_limit, args.include_infeasible)
        if args.command == "check":
            return _cmd_check(data)
    except (InputError, ValueError, OverflowError, FileNotFoundError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2
    parser.error(f"unknown command {args.command!r}")
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
