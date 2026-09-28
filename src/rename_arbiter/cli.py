"""Command line interface for the offline rename arbiter."""

from __future__ import annotations

import argparse
import json
import sys
from collections.abc import Sequence

from .engine import CausalCycleError, SyncEngine
from .model import EventParseError, parse_events


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="rename-arbiter",
        description=(
            "Offline synchronization rename arbitration. Reads a JSON array "
            "of create/move/rename/delete events and emits the converged tree, "
            "conflict directory and rejection report."
        ),
    )
    parser.add_argument(
        "input",
        nargs="?",
        help="path to the JSON event file (default: standard input)",
    )
    parser.add_argument(
        "-o",
        "--output",
        help="write the JSON report here (default: standard output)",
    )
    parser.add_argument(
        "--compact",
        action="store_true",
        help="emit compact single-line JSON instead of indented JSON",
    )
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        if args.input:
            with open(args.input, encoding="utf-8") as fh:
                text = fh.read()
        else:
            text = sys.stdin.read()
        raw = json.loads(text)
        events = parse_events(raw)
        report = SyncEngine().resolve(events).to_dict()
    except json.JSONDecodeError as exc:
        print(f"error: invalid JSON: {exc}", file=sys.stderr)
        return 2
    except (EventParseError, CausalCycleError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2

    text = json.dumps(
        report,
        ensure_ascii=False,
        indent=None if args.compact else 2,
        sort_keys=False,
    )
    if args.output:
        with open(args.output, "w", encoding="utf-8") as fh:
            fh.write(text + "\n")
    else:
        print(text)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
