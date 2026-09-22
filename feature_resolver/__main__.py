"""CLI: python -m feature_resolver REGISTRY_JSON REQUEST_JSON"""

from __future__ import annotations

import argparse
import json
import sys

from .errors import FeatureResolverError
from .model import Registry, RootRequest
from .resolver import Resolver


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="feature_resolver",
        description=(
            "Compute the union closure of features and packages from local"
            " JSON files (no network access)."
        ),
    )
    parser.add_argument("registry", help="path to the package registry JSON")
    parser.add_argument("request", help="path to the build request JSON")
    parser.add_argument(
        "--compact", action="store_true", help="print single-line JSON output"
    )
    args = parser.parse_args(argv)
    try:
        registry = Registry.load(args.registry)
        request = RootRequest.load(args.request)
        resolution = Resolver(registry).resolve(request)
    except FeatureResolverError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1
    indent = None if args.compact else 2
    print(json.dumps(resolution.to_dict(), indent=indent))
    return 0


if __name__ == "__main__":
    sys.exit(main())
