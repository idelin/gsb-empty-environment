#!/usr/bin/env python3.11
"""CLI for the license-selection solver. Local JSON in, JSON out.

Commands:
  solve      input.json [--budget N] [--pin name=version ...]
  enumerate  input.json            brute-force optimum (small graphs)
  repair     input.json --prev solution.json [--pin name=version ...]
"""

from __future__ import annotations

import argparse
import json
import sys

from licsel.core import conflict_core
from licsel.incremental import repair
from licsel.model import ModelError, load_problem
from licsel.solver import (
    STATUS_INFEASIBLE,
    STATUS_OPTIMAL,
    Solver,
    enumerate_optimal,
)
from licsel.version import Version


def _apply_pins(raw: dict, pins: list[str]) -> dict:
    """Restrict the local index so ``name`` resolves to exactly ``version``."""
    for pin in pins:
        name, _, version = pin.partition("=")
        if not name or not version:
            raise ModelError(f"bad --pin {pin!r}, want name=major.minor.patch")
        wanted = Version.parse(version)
        kept = [
            p for p in raw.get("packages", [])
            if not (p.get("name") == name and p.get("version") != str(wanted))
        ]
        if not any(p.get("name") == name and p.get("version") == str(wanted) for p in kept):
            raise ModelError(f"pin {pin!r}: version not present in local index")
        raw = dict(raw, packages=kept)
    return raw


def _load(args) -> "object":
    with open(args.input, "r", encoding="utf-8") as fh:
        raw = json.load(fh)
    raw = _apply_pins(raw, getattr(args, "pin", []) or [])
    return load_problem(raw)


def _solve_payload(problem, result) -> dict:
    payload = {
        "status": result.status,
        "complete": result.complete,
        "pending": result.pending,
        "explored": result.explored,
        "objective": (
            {"exceptions": result.objective[0], "disclosures": result.objective[1]}
            if result.objective
            else None
        ),
        "choices": result.choices_as_json(),
        "disclosure_packages": list(result.disclosures),
    }
    if result.status == STATUS_OPTIMAL:
        payload["note"] = (
            "search complete: all propagation-rule instances hold and the "
            "objective is minimal (synthetic rules only, not legal advice)"
        )
    elif result.status == "feasible_upper_bound":
        payload["note"] = (
            "search budget EXHAUSTED: best feasible assignment so far is an "
            "upper bound only; optimality not proven and full constraint "
            "verification still pending - do not treat as fully verified"
        )
    elif result.status == "unknown":
        payload["note"] = (
            "search budget EXHAUSTED before any feasible assignment was "
            "found; feasibility is undecided"
        )
    return payload


def cmd_solve(args) -> int:
    problem = _load(args)
    result = Solver(problem, budget=args.budget).solve()
    payload = _solve_payload(problem, result)
    if result.status == STATUS_INFEASIBLE:
        core = conflict_core(problem)
        payload["conflict_core"] = core.as_json()
        payload["note"] = (
            "no choice of license branches satisfies all propagation rules; "
            "see conflict_core (synthetic rules, not legal advice)"
        )
    print(json.dumps(payload, indent=2, sort_keys=True))
    return 0


def cmd_enumerate(args) -> int:
    problem = _load(args)
    best, checked = enumerate_optimal(problem)
    payload = {
        "status": "optimal" if best else "infeasible",
        "combinations_checked": checked,
        "objective": (
            {"exceptions": best[0], "disclosures": best[1]} if best else None
        ),
        "note": "exhaustive enumeration over all branch combinations",
    }
    print(json.dumps(payload, indent=2, sort_keys=True))
    return 0


def cmd_repair(args) -> int:
    problem = _load(args)
    with open(args.prev, "r", encoding="utf-8") as fh:
        previous = json.load(fh)
    result = repair(problem, previous.get("choices", {}), budget=args.budget)
    payload = result.as_json()
    if result.core is not None:
        payload["note"] = (
            "repair impossible: conflict core attached (synthetic rules, "
            "not legal advice)"
        )
    print(json.dumps(payload, indent=2, sort_keys=True))
    return 0


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)

    def add_common(p):
        p.add_argument("input", help="problem JSON (local file)")
        p.add_argument("--budget", type=int, default=100_000,
                       help="max search nodes before giving an upper bound")
        p.add_argument("--pin", action="append", default=[],
                       help="force name=version during resolution")

    p_solve = sub.add_parser("solve", help="branch-and-bound solve")
    add_common(p_solve)
    p_solve.set_defaults(func=cmd_solve)

    p_enum = sub.add_parser("enumerate", help="exhaustive small-graph check")
    add_common(p_enum)
    p_enum.set_defaults(func=cmd_enumerate)

    p_repair = sub.add_parser("repair", help="incremental repair after version change")
    add_common(p_repair)
    p_repair.add_argument("--prev", required=True,
                          help="previous solve output JSON (uses its choices)")
    p_repair.set_defaults(func=cmd_repair)

    args = parser.parse_args(argv)
    try:
        return args.func(args)
    except ModelError as exc:
        print(json.dumps({"status": "error", "error": str(exc)}), file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
