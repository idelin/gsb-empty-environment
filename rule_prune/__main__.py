"""Command line interface: solve and verify subcommands.

Usage:
    python -m rule_prune solve INPUT.json [--budget N] [--time-limit S] [-o OUT.json]
    python -m rule_prune verify INPUT.json SOLUTION.json

Use "-" as a path to read from stdin.
"""
from __future__ import annotations

import argparse
import json
import sys

from .solver import parse_instance, solve
from .verifier import verify_solution


def _read_json(path):
    try:
        if path == "-":
            return json.load(sys.stdin)
        with open(path, "r", encoding="utf-8") as fh:
            return json.load(fh)
    except OSError as exc:
        raise SystemExit(f"error: cannot read {path!r}: {exc}")
    except json.JSONDecodeError as exc:
        raise SystemExit(f"error: invalid JSON in {path!r}: {exc}")


def _cmd_solve(args):
    try:
        rules = parse_instance(_read_json(args.input))
    except ValueError as exc:
        raise SystemExit(f"error: invalid instance: {exc}")
    result = solve(rules, budget=args.budget, time_limit=args.time_limit)
    out = json.dumps(result.to_dict(), indent=2, ensure_ascii=False)
    if args.output:
        with open(args.output, "w", encoding="utf-8") as fh:
            fh.write(out + "\n")
    else:
        print(out)
    return 0 if result.status == "optimal" else 2


def _cmd_verify(args):
    try:
        rules = parse_instance(_read_json(args.input))
    except ValueError as exc:
        raise SystemExit(f"error: invalid instance: {exc}")
    solution = _read_json(args.solution)
    verdict = verify_solution(rules, solution)
    print(json.dumps(verdict, indent=2, ensure_ascii=False))
    return 0 if verdict["ok"] else 1


def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="rule_prune",
        description=(
            "Delete a minimum-total-cost set of rules so the remaining "
            "precedence graph is acyclic. Exact branch-and-bound with a "
            "node budget; emits a verifiable lower-bound certificate."
        ),
    )
    sub = parser.add_subparsers(dest="command", required=True)

    p_solve = sub.add_parser("solve", help="solve an instance")
    p_solve.add_argument("input", help="instance JSON file, or - for stdin")
    p_solve.add_argument(
        "--budget", type=int, default=100_000,
        help="maximum branch-and-bound nodes to expand (default 100000)",
    )
    p_solve.add_argument(
        "--time-limit", type=float, default=None, metavar="SECONDS",
        help="optional wall-clock limit; search stops early if exceeded",
    )
    p_solve.add_argument(
        "-o", "--output", default=None,
        help="write solution JSON here instead of stdout",
    )
    p_solve.set_defaults(func=_cmd_solve)

    p_verify = sub.add_parser("verify", help="independently verify a solution")
    p_verify.add_argument("input", help="instance JSON file, or - for stdin")
    p_verify.add_argument("solution", help="solution JSON file, or - for stdin")
    p_verify.set_defaults(func=_cmd_verify)

    args = parser.parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
