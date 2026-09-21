"""Command line interface: python -m depsolver resolve ..."""

from __future__ import annotations

import argparse
import json
import sys

from .model import InputError, Lock, Repository, Requirements
from .report import render
from .solver import DEFAULT_MAX_STATES, LockError, Solver, Status

EXIT_SOLVED = 0
EXIT_UNSATISFIABLE = 1
EXIT_LIMIT = 2
EXIT_INPUT_ERROR = 3


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="depsolver",
        description=(
            "Minimal-change lockfile-aware dependency resolver "
            "(local JSON inputs, no network)."
        ),
    )
    sub = parser.add_subparsers(dest="command", required=True)
    resolve = sub.add_parser(
        "resolve", help="resolve requirements against a lockfile"
    )
    resolve.add_argument("--repo", required=True, help="repository JSON file")
    resolve.add_argument("--lock", required=True, help="current lockfile JSON")
    resolve.add_argument(
        "--requirements", required=True, help="root requirements JSON (build inputs)"
    )
    resolve.add_argument(
        "--max-states",
        type=int,
        default=DEFAULT_MAX_STATES,
        help=f"search budget in explored states (default {DEFAULT_MAX_STATES})",
    )
    resolve.add_argument(
        "--keep-orphans",
        action="store_true",
        help="keep locked packages that become unreachable, pinned at their "
        "locked versions (default: prune them from the new lock)",
    )
    resolve.add_argument("--write-lock", help="write the new lockfile JSON here")
    resolve.add_argument(
        "--json", action="store_true", help="print the report as JSON instead of text"
    )
    return parser


def _result_json(result) -> dict:
    return {
        "status": result.status.value,
        "optimal": result.optimal,
        "states": result.states,
        "changes": [
            {
                "package": c.package,
                "kind": c.kind,
                "old": str(c.old) if c.old else None,
                "new": str(c.new) if c.new else None,
            }
            for c in result.changes
        ],
        "resolution": {n: str(v) for n, v in sorted(result.resolution.items())},
        "conflicts": result.conflicts,
    }


def main(argv=None) -> int:
    args = build_parser().parse_args(argv)
    try:
        repo = Repository.load(args.repo)
        lock = Lock.load(args.lock)
        requirements = Requirements.load(args.requirements)
    except InputError as exc:
        print(f"input error: {exc}", file=sys.stderr)
        return EXIT_INPUT_ERROR
    solver = Solver(
        repo,
        lock,
        requirements,
        max_states=args.max_states,
        keep_orphans=args.keep_orphans,
    )
    try:
        result = solver.solve()
    except LockError as exc:
        print(f"lock error: {exc}", file=sys.stderr)
        return EXIT_INPUT_ERROR
    if args.json:
        print(json.dumps(_result_json(result), indent=2))
    else:
        print(render(result, lock, args.keep_orphans))
    if args.write_lock and result.resolution:
        new_lock = {
            "locked": {n: str(v) for n, v in sorted(result.resolution.items())}
        }
        with open(args.write_lock, "w", encoding="utf-8") as handle:
            json.dump(new_lock, handle, indent=2)
            handle.write("\n")
    if result.status is Status.SOLVED:
        return EXIT_SOLVED
    if result.status is Status.UNSATISFIABLE:
        return EXIT_UNSATISFIABLE
    return EXIT_LIMIT


if __name__ == "__main__":
    raise SystemExit(main())
