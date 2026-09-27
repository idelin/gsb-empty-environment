"""Command-line interface for the empirical DP auditor.

Example:
    python3.11 -m dp_audit.cli --mechanism leaky --trials 20000 --seed 42

Exit code is 1 when a violation is detected, 0 otherwise.
"""

from __future__ import annotations

import argparse
import json
import sys

from . import mechanisms
from .auditor import AuditConfig, format_summary, run_audit


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="dp_audit",
        description=(
            "Empirical audit of a differential-privacy claim on synthetic "
            "adjacent data. Statistical experiments can falsify a claim; "
            "they can never prove formal privacy."
        ),
    )
    parser.add_argument(
        "--mechanism",
        choices=["laplace", "leaky", "deterministic"],
        default="laplace",
        help="mechanism to audit (default: laplace)",
    )
    parser.add_argument(
        "--epsilon",
        type=float,
        default=1.0,
        help="claimed epsilon of the mechanism (default: 1.0)",
    )
    parser.add_argument(
        "--leak-probability",
        type=float,
        default=0.2,
        help="leak probability of the 'leaky' mechanism (default: 0.2)",
    )
    parser.add_argument(
        "--base-size",
        type=int,
        default=10,
        help="number of synthetic 0-records in the base dataset (default: 10)",
    )
    parser.add_argument(
        "--injected-record",
        type=int,
        default=1,
        choices=[0, 1],
        help="record injected to form the adjacent dataset (default: 1)",
    )
    parser.add_argument(
        "--threshold",
        type=float,
        default=None,
        help="event threshold t for 'output >= t' "
        "(default: sum(base_dataset) + injected_record)",
    )
    parser.add_argument(
        "--trials",
        type=int,
        default=20000,
        help="Monte-Carlo trials per dataset (default: 20000)",
    )
    parser.add_argument(
        "--alpha",
        type=float,
        default=0.05,
        help="significance level for confidence intervals (default: 0.05)",
    )
    parser.add_argument(
        "--seed",
        type=int,
        default=20260928,
        help="master seed; recorded in the report (default: 20260928)",
    )
    parser.add_argument(
        "--json",
        action="store_true",
        help="also print the full JSON report",
    )
    return parser


def main(argv=None) -> int:
    args = build_parser().parse_args(argv)

    if args.mechanism == "laplace":
        mechanism = mechanisms.laplace_count(args.epsilon)
    elif args.mechanism == "leaky":
        mechanism = mechanisms.leaky_laplace_count(args.epsilon, args.leak_probability)
    else:
        mechanism = mechanisms.deterministic_count()

    base_dataset = (0,) * args.base_size
    threshold = (
        args.threshold
        if args.threshold is not None
        else float(sum(base_dataset) + args.injected_record)
    )
    event_description = f"output >= {threshold:g}"

    config = AuditConfig(
        mechanism=mechanism,
        base_dataset=base_dataset,
        injected_record=args.injected_record,
        event=lambda out: out >= threshold,
        event_description=event_description,
        n_trials=args.trials,
        alpha=args.alpha,
        claimed_epsilon=args.epsilon,
        seed=args.seed,
    )
    report = run_audit(config)

    print(format_summary(report))
    if args.json:
        print("\n=== JSON report ===")
        print(json.dumps(report, indent=2, ensure_ascii=False))

    return 1 if report["verdict"]["violation_detected"] else 0


if __name__ == "__main__":
    sys.exit(main())
