"""Command line entry point: python -m stageplan ..."""

from __future__ import annotations

import argparse
import json
import sys

from .model import CatalogError, load_catalog
from .planner import plan
from .render import render_text


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="stageplan",
        description="Plan staged builds from a local package JSON file (no network access).",
    )
    parser.add_argument("catalog", help="path to the local package catalog JSON")
    parser.add_argument(
        "--target",
        action="append",
        default=None,
        help="package to build (repeatable); defaults to all packages",
    )
    parser.add_argument("--json", action="store_true", help="emit machine-readable JSON")
    args = parser.parse_args(argv)

    try:
        catalog = load_catalog(args.catalog)
    except CatalogError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2

    report = plan(catalog, args.target)
    if args.json:
        print(json.dumps(report.to_dict(), indent=2, ensure_ascii=False))
    else:
        print(render_text(report), end="")
    return 0 if report.ok else 1


if __name__ == "__main__":
    raise SystemExit(main())
