"""CLI: python -m buildstate <packages.json> <events.json>"""
from __future__ import annotations

import json
import sys

from .loader import LoadError, load_events, load_packages
from .reducer import BuildState


def main(argv: list[str] | None = None) -> int:
    args = sys.argv[1:] if argv is None else argv
    if len(args) != 2:
        print("usage: python -m buildstate <packages.json> <events.json>", file=sys.stderr)
        return 2
    try:
        specs = load_packages(args[0])
        events = load_events(args[1])
    except LoadError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2

    state = BuildState(specs)
    state.apply_all(events)
    print(json.dumps(state.report(), indent=2, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
