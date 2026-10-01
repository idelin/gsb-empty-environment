"""Command line interface for the constrained lottery assignment tool."""

from __future__ import annotations

import argparse
import json
import sys

from .solver import Problem, brute_force_lexmin, solve


def _load_json(path: str) -> dict:
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def _dump_json(data: dict, path: str | None) -> None:
    text = json.dumps(data, indent=2, sort_keys=True, ensure_ascii=False) + "\n"
    if path:
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(text)
    else:
        sys.stdout.write(text)


def cmd_solve(args: argparse.Namespace) -> int:
    problem = Problem.from_dict(_load_json(args.input))
    if args.seed is not None:
        problem.seed = int(args.seed)
    result = solve(problem)
    if args.exhaustive_check:
        edges = problem.feasible_edges()
        if len(problem.candidates) > 12 or len(edges) > 60:
            print("exhaustive check skipped: instance too large", file=sys.stderr)
            return 2
        expected = brute_force_lexmin(problem)
        from .solver import edge_order

        ordered, _ = edge_order(edges, problem.seed)
        rank_of = {edge: pos for pos, edge in enumerate(ordered)}
        actual = tuple(sorted(rank_of[(a["candidate"], a["award"])] for a in result["assignments"]))
        if actual != expected:
            print("exhaustive check FAILED", file=sys.stderr)
            return 1
        result["exhaustive_check"] = "passed"
    _dump_json(result, args.output)
    return 0


def cmd_verify(args: argparse.Namespace) -> int:
    problem = Problem.from_dict(_load_json(args.input))
    result = _load_json(args.result)
    seed = int(result.get("seed", problem.seed))
    problem.seed = seed
    recomputed = solve(problem)
    expected_pairs = sorted(
        (a["candidate"], a["award"]) for a in recomputed["assignments"]
    )
    actual_pairs = sorted(
        (str(a["candidate"]), str(a["award"])) for a in result.get("assignments", [])
    )
    if actual_pairs != expected_pairs:
        print("verify FAILED: assignments do not match the deterministic recomputation",
              file=sys.stderr)
        print(f"  expected: {expected_pairs}", file=sys.stderr)
        print(f"  actual:   {actual_pairs}", file=sys.stderr)
        return 1
    if int(result.get("optimum", -1)) != recomputed["optimum"]:
        print("verify FAILED: reported optimum is wrong", file=sys.stderr)
        return 1
    print(f"verify OK: {len(actual_pairs)} assignment(s), optimum={recomputed['optimum']}, "
          f"seed={seed}, prng=splitmix64")
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="lottery_assign",
        description="Constrained lottery assignment: lexicographically smallest "
                    "maximum matching under a seeded, reproducible edge order.",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    p_solve = sub.add_parser("solve", help="solve an instance and emit the result as JSON")
    p_solve.add_argument("input", help="input JSON file (see README for the format)")
    p_solve.add_argument("-o", "--output", help="output JSON file (default: stdout)")
    p_solve.add_argument("--seed", type=int, default=None,
                         help="override the seed given in the input file")
    p_solve.add_argument("--exhaustive-check", action="store_true",
                         help="brute-force verify lex-min optimality (small instances only)")
    p_solve.set_defaults(func=cmd_solve)

    p_verify = sub.add_parser("verify", help="recompute and check a previously emitted result")
    p_verify.add_argument("input", help="input JSON file used for the solve")
    p_verify.add_argument("result", help="result JSON file to verify")
    p_verify.set_defaults(func=cmd_verify)

    return parser


def main(argv: list | None = None) -> int:
    args = build_parser().parse_args(argv)
    return args.func(args)
