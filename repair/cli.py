"""Command line interface for the plan-repair tool."""
from __future__ import annotations

import argparse
import dataclasses
import json
import sys

from .jsonio import load_scenario
from .model import parse_scenario
from .repair import repair
from .samples import SAMPLES


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="repair",
        description="Repair a feasible plan after task additions or "
                    "resource failures: minimise moved tasks first, then "
                    "cost.  Executed prefix stays frozen.")
    parser.add_argument("scenario", nargs="?",
                        help="path to a scenario JSON file")
    parser.add_argument("--sample", metavar="NAME",
                        help="run a built-in sample scenario")
    parser.add_argument("--list-samples", action="store_true",
                        help="list built-in sample names and exit")
    parser.add_argument("--subset-budget", type=int, default=None,
                        help="override the moved-subset search budget")
    parser.add_argument("--placement-budget", type=int, default=None,
                        help="override the placement search budget")
    return parser


def main(argv=None) -> int:
    args = build_parser().parse_args(argv)
    if args.list_samples:
        for name in sorted(SAMPLES):
            print(name)
        return 0
    if args.sample:
        if args.sample not in SAMPLES:
            print(f"unknown sample {args.sample!r}; "
                  f"choose from: {', '.join(sorted(SAMPLES))}",
                  file=sys.stderr)
            return 2
        scenario = parse_scenario(SAMPLES[args.sample])
    elif args.scenario:
        scenario = load_scenario(args.scenario)
    else:
        print("error: give a scenario JSON file or --sample NAME",
              file=sys.stderr)
        return 2

    if args.subset_budget is not None or args.placement_budget is not None:
        scenario = dataclasses.replace(
            scenario,
            subset_budget=(args.subset_budget
                           if args.subset_budget is not None
                           else scenario.subset_budget),
            placement_budget=(args.placement_budget
                              if args.placement_budget is not None
                              else scenario.placement_budget))

    result = repair(scenario)
    print(json.dumps(result.to_dict(), indent=2, ensure_ascii=False))
    return 0
