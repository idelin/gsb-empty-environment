"""CLI: resolve capabilities from local JSON files.

Usage: python3 main.py PACKAGES_JSON BUILD_JSON
"""

import json
import sys

from resolver import Constraint, Package, Repository, ResolutionError, Resolver


def main(argv):
    if len(argv) != 3:
        print(__doc__.strip(), file=sys.stderr)
        return 2
    with open(argv[1], encoding="utf-8") as fh:
        packages = [Package.from_dict(d) for d in json.load(fh)]
    with open(argv[2], encoding="utf-8") as fh:
        build = json.load(fh)

    requirements = {
        cap: Constraint.parse(c) for cap, c in build.get("requires", {}).items()
    }
    resolver = Resolver(Repository(packages), target=build.get("target", ""))
    try:
        mapping = resolver.resolve(requirements)
    except ResolutionError as exc:
        print(f"resolution failed: {exc}", file=sys.stderr)
        return 1
    for cap, pkg in mapping.items():
        print(f"{cap} -> {pkg}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
