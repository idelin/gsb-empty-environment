from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

from .brute import exact_optimal_check
from .model import load_machine
from .planner import plan
from .verifier import verify


def _emit(data: object, output: str | None) -> None:
    text = json.dumps(data, indent=2, ensure_ascii=False, sort_keys=False)
    if output:
        Path(output).write_text(text + "\n", encoding="utf-8")
    else:
        print(text)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Deterministic Mealy transition-coverage contingent planner")
    sub = parser.add_subparsers(dest="command", required=True)

    plan_parser = sub.add_parser("plan", help="build an adaptive AND/OR strategy")
    plan_parser.add_argument("machine")
    plan_parser.add_argument("--budget", type=int, default=100_000)
    plan_parser.add_argument("--output", "-o")

    verify_parser = sub.add_parser("verify", help="independently verify a strategy envelope")
    verify_parser.add_argument("machine")
    verify_parser.add_argument("policy")

    brute_parser = sub.add_parser("brute-check", help="exhaustively check a claimed optimum")
    brute_parser.add_argument("machine")
    brute_parser.add_argument("--cost", type=int, required=True)

    args = parser.parse_args(argv)
    machine = load_machine(args.machine)

    if args.command == "plan":
        result = plan(machine, budget=args.budget)
        _emit(result, args.output)
        return 0
    if args.command == "verify":
        result = verify(args.machine, args.policy)
        _emit(result, None)
        return 0
    if args.command == "brute-check":
        _emit(exact_optimal_check(machine, args.cost), None)
        return 0
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
