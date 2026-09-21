"""Command line entry point: resolve a build request against a catalog.

Usage:
    python -m capability_resolver --catalog catalog.json --build build.json
"""
from __future__ import annotations

import argparse
import json
import sys
from typing import Sequence

from .errors import CapabilityResolverError
from .loader import load_build_request, load_catalog
from .resolver import Resolver


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="python -m capability_resolver",
        description="Resolve required capabilities to a single implementation each.",
    )
    parser.add_argument("--catalog", required=True, help="path to the package catalog JSON")
    parser.add_argument("--build", required=True, help="path to the build request JSON")
    parser.add_argument(
        "--indent", type=int, default=2, help="JSON indent for the output (default: 2)"
    )
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        catalog = load_catalog(args.catalog)
        request = load_build_request(args.build)
        resolution = Resolver(catalog, request.target).resolve(request.requirements)
    except CapabilityResolverError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1
    print(json.dumps(resolution.to_dict(), indent=args.indent, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
