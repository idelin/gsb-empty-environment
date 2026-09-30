"""Command line interface for the external merge sorter.

Usage:
    python -m extsort INPUT OUTPUT [--workdir DIR] [--memory N]
                    [--handle-budget N] [--keep-work]

Input format:  one JSON object per line, ``{"key": K, "value": V}``.
               Keys must be mutually comparable (all numbers or all strings).
Output format: one JSON object per line, ``{"key": K, "seq": I, "value": V}``,
               sorted by ``(key, seq)`` where ``seq`` is the 0-based input
               ordinal, so equal keys keep their original order (stable).
"""

from __future__ import annotations

import argparse
import sys

from .sorter import CorruptRunError, ExternalSorter, ManifestCorruptError


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="extsort",
        description=(
            "Bounded-memory external merge sort for JSONL records. "
            "Resumes from a checksummed manifest after interruption; "
            "the output file is replaced atomically."
        ),
    )
    parser.add_argument("input", help="input JSONL file: {\"key\": K, \"value\": V} per line")
    parser.add_argument("output", help="output JSONL file (atomically replaced)")
    parser.add_argument(
        "--workdir",
        default=None,
        help="directory for run files and the resume manifest "
        "(default: <output>.extsort-work)",
    )
    parser.add_argument(
        "--memory",
        type=int,
        default=10000,
        help="memory budget: max records held in memory per run (default: 10000)",
    )
    parser.add_argument(
        "--handle-budget",
        type=int,
        default=32,
        help="max open file handles; merge fan-in is budget - 2 (default: 32)",
    )
    parser.add_argument(
        "--keep-work",
        action="store_true",
        help="keep run files and manifest after a successful sort",
    )
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    workdir = args.workdir or (args.output + ".extsort-work")
    sorter = ExternalSorter(
        input_path=args.input,
        output_path=args.output,
        workdir=workdir,
        memory_budget=args.memory,
        handle_budget=args.handle_budget,
        keep_work=args.keep_work,
    )
    try:
        count = sorter.sort()
    except (CorruptRunError, ManifestCorruptError) as exc:
        print(f"extsort: {exc}", file=sys.stderr)
        return 2
    except (OSError, ValueError, KeyError) as exc:
        print(f"extsort: {exc}", file=sys.stderr)
        return 1
    print(f"extsort: wrote {count} records to {args.output}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
