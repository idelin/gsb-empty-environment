"""CLI: python -m compatcheck INPUT.json [--json]

Exit code 0: no root affected by breaking changes.
Exit code 1: at least one root is affected (or the input is invalid).
"""
from __future__ import annotations

import argparse
import json
import sys

from .core import analyze
from .report import render_text
from .semver import VersionError


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        prog="compatcheck",
        description="Offline API compatibility impact analysis from local JSON.",
    )
    parser.add_argument("input", help="path to the analysis input JSON file")
    parser.add_argument("--json", action="store_true", help="emit the report as JSON")
    args = parser.parse_args(argv)

    try:
        with open(args.input, "r", encoding="utf-8") as handle:
            payload = json.load(handle)
        report = analyze(payload)
    except (OSError, json.JSONDecodeError, KeyError, VersionError, ValueError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2

    if args.json:
        print(json.dumps(report.to_dict(), indent=2, ensure_ascii=False))
    else:
        print(render_text(report))
    return 1 if report.breaking_paths else 0


if __name__ == "__main__":
    sys.exit(main())
