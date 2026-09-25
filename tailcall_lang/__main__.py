from __future__ import annotations

import argparse
from pathlib import Path

from .interpreter import LangError, execute_source, show_value


def main() -> int:
    parser = argparse.ArgumentParser(description="Run a tail-call optimized language")
    parser.add_argument("source", type=Path)
    parser.add_argument("--stats", action="store_true")
    args = parser.parse_args()
    source = args.source.read_text(encoding="utf-8")
    try:
        result, vm = execute_source(source)
    except LangError as error:
        print(error.format(source), file=__import__("sys").stderr)
        return 1
    if result is not None:
        print(show_value(result))
    if args.stats:
        print(f"logical calls: {vm.next_call_number - 1}")
        print(f"max physical frames: {vm.max_physical_frames}")
        print(f"max logical depth: {vm.max_logical_depth}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
