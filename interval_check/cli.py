"""Command line interface.

Run a JSON specification from a file or stdin::

    echo '{"expression": "x - x", "variables": {"x": [0, 1]},
           "assertion": "x - x >= 0"}' | python3.11 -m interval_check

The result is printed as JSON. Exit codes::

    0 safe (interval proof or complete enumeration with no witness)
    1 conservative warning not refuted
    2 confirmed failure (exact witness exists)
    3 input or parse error
"""

from __future__ import annotations

import argparse
import json
import sys
from typing import Sequence

from .checker import CheckStatus, analyze_dict


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="interval_check",
        description=(
            "conservative interval analysis with exact enumeration "
            "cross-check (Python 3.11, standard library)"
        ),
    )
    parser.add_argument(
        "spec",
        nargs="?",
        help="path to a JSON specification file; defaults to stdin",
    )
    parser.add_argument(
        "--limit",
        type=int,
        default=None,
        help="maximum number of assignments enumerated (default 1000000)",
    )
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        if args.spec:
            with open(args.spec, "r", encoding="utf-8") as handle:
                spec = json.load(handle)
        else:
            spec = json.load(sys.stdin)
        if args.limit is not None:
            if not isinstance(spec, dict):
                raise ValueError("specification must be a JSON object")
            spec["enumerate_limit"] = args.limit
        result = analyze_dict(spec)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"input error: {exc}", file=sys.stderr)
        return 3

    json.dump(result.to_dict(), sys.stdout, ensure_ascii=False, indent=2)
    sys.stdout.write("\n")
    if result.status is CheckStatus.CONFIRMED_FAILURE:
        return 2
    if result.status is CheckStatus.CONSERVATIVE_WARNING:
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
