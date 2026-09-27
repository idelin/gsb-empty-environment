"""Command line interface.  See README.md for the input/output format."""
from __future__ import annotations

import argparse
import json
import sys

from .model import ProblemFormatError, compute_reclaim, load_problem
from .solver import solve_max_reclaim, solve_min_loss


def _add_limit_args(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--node-limit", type=int, default=None,
                        help="stop after this many search nodes and report "
                             "the best solution found (proven_optimal=false)")
    parser.add_argument("--time-limit", type=float, default=None,
                        help="stop after this many seconds and report the "
                             "best solution found (proven_optimal=false)")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="snapreclaim",
        description="Multi-snapshot dedup space reclamation planner.")
    sub = parser.add_subparsers(dest="command", required=True)

    solve = sub.add_parser(
        "solve", help="select snapshots to delete (exact branch and bound)")
    solve.add_argument("input", help="problem JSON file ('-' for stdin)")
    objective = solve.add_mutually_exclusive_group(required=True)
    objective.add_argument("--required-bytes", type=int,
                           help="minimise loss while reclaiming at least "
                                "this many bytes")
    objective.add_argument("--loss-budget", type=int,
                           help="maximise reclaimed bytes within this loss "
                                "budget")
    _add_limit_args(solve)

    reclaim = sub.add_parser(
        "reclaim", help="compute bytes reclaimed by deleting given snapshots")
    reclaim.add_argument("input", help="problem JSON file ('-' for stdin)")
    reclaim.add_argument("--delete", nargs="+", default=[], metavar="SNAP_ID",
                         help="snapshot ids to delete")
    return parser


def _load(path: str):
    if path == "-":
        data = json.load(sys.stdin)
        from .model import problem_from_dict
        return problem_from_dict(data)
    return load_problem(path)


def main(argv=None) -> int:
    args = build_parser().parse_args(argv)
    try:
        problem = _load(args.input)
    except (ProblemFormatError, json.JSONDecodeError, OSError) as exc:
        print(json.dumps({"error": str(exc)}), file=sys.stderr)
        return 2

    if args.command == "reclaim":
        try:
            reclaimed = compute_reclaim(problem, args.delete)
        except ValueError as exc:
            print(json.dumps({"error": str(exc)}), file=sys.stderr)
            return 2
        print(json.dumps({
            "deleted": sorted(set(args.delete)),
            "reclaimed_bytes": reclaimed,
        }, indent=2))
        return 0

    if args.required_bytes is not None:
        result = solve_min_loss(problem, args.required_bytes,
                                node_limit=args.node_limit,
                                time_limit=args.time_limit)
        result_dict = result.to_dict()
        result_dict["objective"] = "min_loss"
        result_dict["required_bytes"] = args.required_bytes
    else:
        result = solve_max_reclaim(problem, args.loss_budget,
                                   node_limit=args.node_limit,
                                   time_limit=args.time_limit)
        result_dict = result.to_dict()
        result_dict["objective"] = "max_reclaim"
        result_dict["loss_budget"] = args.loss_budget
    print(json.dumps(result_dict, indent=2))
    return 0 if result_dict["feasible"] else 1


if __name__ == "__main__":
    sys.exit(main())
