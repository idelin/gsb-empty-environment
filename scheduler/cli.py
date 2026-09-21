"""Command-line interface: python -m scheduler <input.json>"""
from __future__ import annotations

import json
import sys

from .model import load_problem
from .scheduler import InfeasibleError, schedule

NOTE = (
    "deterministic list-scheduling heuristic; "
    "the makespan is NOT guaranteed to be globally optimal"
)


def main(argv: list[str] | None = None) -> int:
    args = sys.argv[1:] if argv is None else argv
    if len(args) != 1:
        print("usage: python -m scheduler <input.json>", file=sys.stderr)
        return 2
    path = args[0]
    try:
        with open(path, "r", encoding="utf-8") as handle:
            data = json.load(handle)
    except (OSError, json.JSONDecodeError) as exc:
        print(json.dumps({"status": "error", "reason": str(exc)}, indent=2))
        return 1

    try:
        problem = load_problem(data)
    except ValueError as exc:
        print(json.dumps({"status": "invalid_input", "reason": str(exc)}, indent=2))
        return 1

    try:
        result = schedule(problem)
    except InfeasibleError as exc:
        print(json.dumps({"status": "infeasible", "reason": str(exc)}, indent=2))
        return 1

    output = {
        "status": "ok",
        "input_version": str(problem.version),
        "packages": [
            {"name": pkg.name, "version": str(pkg.version)}
            for pkg in problem.packages
        ],
        "makespan": result.makespan,
        "lower_bound": result.lower_bound,
        "gap": result.makespan - result.lower_bound,
        "note": NOTE,
        "schedule": [
            {"task": entry.id, "start": entry.start, "end": entry.end}
            for entry in sorted(result.entries, key=lambda e: (e.start, e.id))
        ],
    }
    print(json.dumps(output, indent=2))
    return 0
