"""CLI: python -m depres INPUT.json"""

from __future__ import annotations

import json
import sys

from .constraints import Constraint
from .registry import Registry
from .resolver import ResolutionConflict, resolve


def main(argv: list[str] | None = None) -> int:
    argv = list(sys.argv[1:] if argv is None else argv)
    if len(argv) != 1:
        print("usage: python -m depres INPUT.json", file=sys.stderr)
        return 2
    with open(argv[0], encoding="utf-8") as handle:
        data = json.load(handle)
    registry = Registry.from_dict(data.get("packages", {}))
    requirements = {
        name: Constraint.parse(spec)
        for name, spec in data.get("requirements", {}).items()
    }
    try:
        solution = resolve(registry, requirements)
    except ResolutionConflict as error:
        print(error.report(), file=sys.stderr)
        return 1
    for name in sorted(solution):
        print(f"{name} {solution[name]}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
