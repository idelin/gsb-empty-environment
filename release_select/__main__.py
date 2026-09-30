"""Command-line interface: python -m release_select INPUT.json

Reads a problem instance (JSON), solves it exactly, and prints a JSON
report containing the optimal selection, constraint evidence, and an
independent exhaustive verification.
"""

from __future__ import annotations

import argparse
import json
import sys
from dataclasses import asdict

from .core import InstanceError, exhaustive_verify, parse_instance, solve


def build_report(instance, with_verify: bool) -> dict:
    solution = solve(instance)
    report = {
        "status": "optimal",
        "selected_ids": list(solution.selected_ids),
        "change_count": len(solution.selected_ids),
        "total_benefit": solution.total_benefit,
        "total_cost": solution.total_cost,
        "evidence": list(solution.evidence),
    }
    if with_verify:
        verification = exhaustive_verify(instance, solution)
        report["verification"] = asdict(verification)
        report["verification"]["best_ids"] = list(verification.best_ids)
    else:
        report["verification"] = None
        report["status"] = "optimal-unverified"
    return report


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="release_select",
        description="Exact minimal-diff release selection: maximize total "
                    "benefit under dependency (cycles taken as a whole), "
                    "mutex and integer capacity constraints. Ties: fewest "
                    "changes, then lexicographic ids.")
    parser.add_argument("input",
                        help="path to the instance JSON file, or '-' for stdin")
    parser.add_argument("--no-verify", action="store_true",
                        help="skip the independent exhaustive verification")
    args = parser.parse_args(argv)

    try:
        if args.input == "-":
            raw = sys.stdin.read()
        else:
            with open(args.input, "r", encoding="utf-8") as fh:
                raw = fh.read()
    except OSError as exc:
        print(f"error: cannot read input: {exc}", file=sys.stderr)
        return 2

    try:
        data = json.loads(raw)
    except json.JSONDecodeError as exc:
        print(f"error: invalid JSON: {exc}", file=sys.stderr)
        return 2

    try:
        instance = parse_instance(data)
    except InstanceError as exc:
        print(f"error: invalid instance: {exc}", file=sys.stderr)
        return 2

    report = build_report(instance, with_verify=not args.no_verify)
    json.dump(report, sys.stdout, indent=2, ensure_ascii=False)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
