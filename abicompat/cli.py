"""Command line interface: python -m abicompat --packages P.json --build B.json."""

from __future__ import annotations

import argparse
import sys
from typing import List, Optional

from .engine import run
from .model import BuildInput, Catalog
from .report import to_json, to_text


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="abicompat",
        description=(
            "Compute API compatibility impact from local package catalog and "
            "build-input JSON files, then propagate breaks along call edges "
            "to root packages."
        ),
    )
    parser.add_argument(
        "--packages",
        required=True,
        help="path to the package/symbol catalog JSON (local file only)",
    )
    parser.add_argument(
        "--build",
        required=True,
        help="path to the build graph JSON with resolved versions and call edges",
    )
    parser.add_argument(
        "--format",
        choices=("text", "json"),
        default="text",
        help="report format (default: text)",
    )
    parser.add_argument(
        "--hide-unreached",
        action="store_true",
        help="do not list changes that no root package can reach",
    )
    parser.add_argument(
        "--check",
        action="store_true",
        help="exit with status 1 when breaking changes reach a root",
    )
    return parser


def main(argv: Optional[List[str]] = None) -> int:
    args = build_parser().parse_args(argv)
    catalog = Catalog.load(args.packages)
    build = BuildInput.load(args.build)
    output = run(catalog, build)
    if args.format == "json":
        print(to_json(output.result, output.nodes_report))
    else:
        print(to_text(output.result, show_unreached=not args.hide_unreached))
    if args.check and output.result.findings:
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
