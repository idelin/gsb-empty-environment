"""CLI: python -m feature_resolver <build.json>"""

from __future__ import annotations

import json
import sys

from .model import ModelError, load_document
from .resolver import ResolutionError, Resolver
from .version import VersionError


def main(argv=None):
    argv = argv if argv is not None else sys.argv[1:]
    if len(argv) != 1:
        print("usage: python -m feature_resolver <build.json>", file=sys.stderr)
        return 2
    try:
        with open(argv[0], "r", encoding="utf-8") as fh:
            data = json.load(fh)
    except (OSError, json.JSONDecodeError) as exc:
        print(f"error: cannot read {argv[0]}: {exc}", file=sys.stderr)
        return 2
    try:
        packages, roots = load_document(data)
        resolution = Resolver(packages).resolve(roots)
    except (ModelError, ResolutionError, VersionError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2
    print(json.dumps(resolution.to_dict(), indent=2, ensure_ascii=False))
    return 0 if resolution.ok else 1


if __name__ == "__main__":
    raise SystemExit(main())
