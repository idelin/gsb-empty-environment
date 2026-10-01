"""Command-line interface for the reconciliation key-conflict interpreter."""
from __future__ import annotations

import argparse
import json
import sys
from decimal import Decimal

from .common import load_table
from .onetoone import solve_onetoone
from .splitmerge import solve_splitmerge


def _emit(report, output):
    text = json.dumps(report, indent=2, ensure_ascii=False, default=str)
    if output:
        with open(output, "w", encoding="utf-8") as fh:
            fh.write(text + "\n")
    else:
        print(text)


def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="recon",
        description="Reconciliation key-conflict interpreter "
                    "(one-to-one and split/merge matching).")
    sub = parser.add_subparsers(dest="command", required=True)

    def add_common(p):
        p.add_argument("--left", required=True,
                       help="JSON file with the left transaction table")
        p.add_argument("--right", required=True,
                       help="JSON file with the right transaction table")
        p.add_argument("--window", required=True,
                       help="max allowed time difference, in seconds")
        p.add_argument("--output", help="write the JSON report to this file "
                                        "instead of stdout")

    p1 = sub.add_parser("onetoone",
                        help="one-to-one matching (max pairs, min total time "
                             "diff, lexicographic pair IDs)")
    add_common(p1)

    p2 = sub.add_parser("splitmerge",
                        help="split/merge matching with groups of up to 3 "
                             "rows (exact set packing)")
    add_common(p2)
    p2.add_argument("--budget", type=int, default=None,
                    help="search node budget; on exhaustion the best lower "
                         "bound and a loose upper bound are reported")
    p2.add_argument("--verify-exhaustive", action="store_true",
                    help="cross-check the branch-and-bound result against an "
                         "independent exhaustive enumeration")
    p2.add_argument("--no-explain", action="store_true",
                    help="skip forced-inclusion analysis of unmatched rows")

    args = parser.parse_args(argv)
    left = load_table(args.left)
    right = load_table(args.right)
    window = Decimal(str(args.window))

    if args.command == "onetoone":
        report = solve_onetoone(left, right, window)
    else:
        report = solve_splitmerge(
            left, right, window,
            budget=args.budget,
            verify_exhaustive=args.verify_exhaustive,
            explain=not args.no_explain,
        )
    _emit(report, args.output)
    return 0


if __name__ == "__main__":
    sys.exit(main())
