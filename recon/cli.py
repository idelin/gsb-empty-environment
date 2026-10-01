"""Command-line interface.

Usage:
  python -m recon onetoone   INPUT.json [--pretty]
  python -m recon splitmerge INPUT.json [--budget N] [--no-cross-check]
                                      [--no-forced-analysis] [--pretty]

Input JSON:
  {
    "window": 300,
    "left":  [{"id": "a", "keys": ["x", "y"],
               "amount": "100.00", "time": 1000}],
    "right": [{"id": "r1", "keys": ["x"],
               "amount": "100.00", "time": 1010}]
  }

Output: JSON report on stdout (see README.md for the full schema).
"""

from __future__ import annotations

import argparse
import json
import sys

from . import model, onetoone, splitmerge


def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="recon", description="Reconciliation key-conflict interpreter")
    sub = parser.add_subparsers(dest="command", required=True)

    p1 = sub.add_parser("onetoone", help="strict 1-1 matching engine")
    p1.add_argument("input", help="path to input JSON ('-' for stdin)")
    p1.add_argument("--pretty", action="store_true", help="indent output")

    p2 = sub.add_parser("splitmerge",
                        help="split/merge group matching engine")
    p2.add_argument("input", help="path to input JSON ('-' for stdin)")
    p2.add_argument("--budget", type=int, default=None,
                    help="branch-and-bound node budget; on exhaustion the "
                         "report carries lower/upper bounds and "
                         "proven_optimal=false")
    p2.add_argument("--no-cross-check", action="store_true",
                    help="skip the independent exhaustive cross-check")
    p2.add_argument("--no-forced-analysis", action="store_true",
                    help="skip per-unmatched-row forced-inclusion analysis")
    p2.add_argument("--pretty", action="store_true", help="indent output")

    args = parser.parse_args(argv)

    if args.input == "-":
        instance = model.load_instance(sys.stdin)
    else:
        instance = model.load_instance(args.input)

    if args.command == "onetoone":
        report = onetoone.solve(instance)
    else:
        report = splitmerge.solve(
            instance,
            node_budget=args.budget,
            run_cross_check=not args.no_cross_check,
            run_forced_analysis=not args.no_forced_analysis,
        )

    indent = 2 if args.pretty else None
    json.dump(report, sys.stdout, indent=indent, ensure_ascii=False)
    sys.stdout.write("\n")
    return 0
