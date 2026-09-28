"""Command line interface.

Usage:
    python -m push_suppress solve INSTANCE.json
    python -m push_suppress check INSTANCE.json SCHEDULE.json
    python -m push_suppress enumerate INSTANCE.json [--limit N]
    python -m push_suppress demo

See README.md for the input/output JSON formats.
"""

from __future__ import annotations

import argparse
import json
import sys
from typing import Any, Dict, List

from .checkers import validate_schedule
from .enumerate import best_by_enumeration, enumerate_schedules, schedule_profit
from .model import Instance, Send, parse_instance, parse_sends
from .solver import solve


def _load_json(path: str) -> Any:
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def _load_instance(path: str) -> Instance:
    try:
        return parse_instance(_load_json(path))
    except (ValueError, OSError, json.JSONDecodeError) as exc:
        raise SystemExit(f"error: invalid instance file {path!r}: {exc}")


def _sends_to_json(sends: List[Send]) -> List[Dict[str, Any]]:
    return [{"id": s.notification_id, "time": s.time} for s in sends]


def cmd_solve(args: argparse.Namespace) -> int:
    instance = _load_instance(args.instance)
    result = solve(instance)
    violations = validate_schedule(instance, result.sends)
    output = {
        "profit": result.profit,
        "optimal": result.optimal and not violations,
        "nodes_explored": result.nodes_explored,
        "sends": _sends_to_json(result.sends),
        "violations": violations,
    }
    json.dump(output, sys.stdout, indent=2, ensure_ascii=False)
    sys.stdout.write("\n")
    return 0


def cmd_check(args: argparse.Namespace) -> int:
    instance = _load_instance(args.instance)
    try:
        sends = parse_sends(_load_json(args.schedule))
    except (ValueError, OSError, json.JSONDecodeError) as exc:
        raise SystemExit(f"error: invalid schedule file {args.schedule!r}: {exc}")
    violations = validate_schedule(instance, sends)
    unknown = any("unknown notification" in v for v in violations)
    output = {
        "feasible": not violations,
        "profit": None if unknown else schedule_profit(instance, sends),
        "violations": violations,
    }
    json.dump(output, sys.stdout, indent=2, ensure_ascii=False)
    sys.stdout.write("\n")
    return 0 if not violations else 1


def cmd_enumerate(args: argparse.Namespace) -> int:
    instance = _load_instance(args.instance)
    best_profit, best_key, _ = best_by_enumeration(instance)
    count = 0
    truncated = False
    rows = []
    for sends in enumerate_schedules(instance):
        if args.limit is not None and count >= args.limit:
            truncated = True
            break
        count += 1
        profit = schedule_profit(instance, sends)
        key = tuple((s.notification_id, s.time) for s in sends)
        rows.append(
            {
                "profit": profit,
                "is_optimal": abs(profit - best_profit) <= 1e-12 and key == best_key,
                "sends": _sends_to_json(sends),
            }
        )
    output = {
        "feasible_schedule_count": count,
        "truncated": truncated,
        "optimal_profit": best_profit,
        "optimal_tie_break_key": [list(pair) for pair in best_key],
        "schedules": rows,
    }
    json.dump(output, sys.stdout, indent=2, ensure_ascii=False)
    sys.stdout.write("\n")
    return 0


DEMO_INSTANCE: Dict[str, Any] = {
    "time_horizon": 6,
    "user_window": {"length": 3, "max_sends": 1},
    "budget": 4,
    "channel_capacity": {"email": 1, "sms": 1},
    "notifications": [
        {"id": "n1", "user": "u1", "channel": "email", "profit": 6,
         "window": [0, 2], "cost": 1, "mutex_group": "campA"},
        {"id": "n2", "user": "u1", "channel": "sms", "profit": 5,
         "window": [1, 4], "cost": 1, "mutex_group": "campA"},
        {"id": "n3", "user": "u1", "channel": "email", "profit": 4,
         "window": [3, 5], "cost": 2},
        {"id": "n4", "user": "u2", "channel": "email", "profit": 3,
         "window": [0, 1], "cost": 1},
        {"id": "n5", "user": "u2", "channel": "sms", "profit": 2,
         "window": [0, 5], "cost": 1},
    ],
}


def cmd_demo(args: argparse.Namespace) -> int:
    instance = parse_instance(DEMO_INSTANCE)
    print("=== demo instance (synthetic) ===")
    print(json.dumps(DEMO_INSTANCE, indent=2, ensure_ascii=False))
    result = solve(instance)
    print("\n=== optimal schedule (branch-and-bound, exact) ===")
    print(json.dumps({
        "profit": result.profit,
        "optimal": result.optimal,
        "nodes_explored": result.nodes_explored,
        "sends": _sends_to_json(result.sends),
        "violations": validate_schedule(instance, result.sends),
    }, indent=2, ensure_ascii=False))
    print("\n=== small-example enumeration (all feasible schedules) ===")
    best_profit, best_key, _ = best_by_enumeration(instance)
    count = 0
    for sends in enumerate_schedules(instance):
        count += 1
        profit = schedule_profit(instance, sends)
        key = tuple((s.notification_id, s.time) for s in sends)
        marker = "  <-- optimal" if (
            abs(profit - best_profit) <= 1e-12 and key == best_key
        ) else ""
        body = ", ".join(f"{s.notification_id}@t={s.time}" for s in sends) or "(empty)"
        print(f"  #{count:3d} profit={profit:5.1f}  {body}{marker}")
    print(f"\ntotal feasible schedules: {count}")
    print(f"optimal profit: {best_profit}")
    print("optimality: verified by exhaustive enumeration of this instance")
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="push_suppress",
        description="Multi-channel push notification suppression decision "
        "(exact max-profit scheduling under rolling-window, capacity, "
        "mutex and budget constraints).",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    p_solve = sub.add_parser("solve", help="solve an instance, print optimal schedule")
    p_solve.add_argument("instance", help="path to instance JSON")
    p_solve.set_defaults(func=cmd_solve)

    p_check = sub.add_parser("check", help="re-check a schedule against all constraints")
    p_check.add_argument("instance", help="path to instance JSON")
    p_check.add_argument("schedule", help="path to schedule JSON")
    p_check.set_defaults(func=cmd_check)

    p_enum = sub.add_parser(
        "enumerate", help="enumerate all feasible schedules (small instances)"
    )
    p_enum.add_argument("instance", help="path to instance JSON")
    p_enum.add_argument("--limit", type=int, default=None,
                        help="max number of schedules to print")
    p_enum.set_defaults(func=cmd_enumerate)

    p_demo = sub.add_parser("demo", help="run the built-in synthetic example")
    p_demo.set_defaults(func=cmd_demo)
    return parser


def main(argv: List[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    raise SystemExit(main())
