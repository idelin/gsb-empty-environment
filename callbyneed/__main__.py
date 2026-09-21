"""CLI: python -m callbyneed [--trace] [file]

Reads a program from FILE (or stdin when omitted / '-'), evaluates it
call-by-need, prints the result on stdout and the trace on stderr.
"""

from __future__ import annotations

import argparse
import sys

from .errors import LangError
from .interpreter import Interpreter


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(
        prog="callbyneed", description="call-by-need expression-graph interpreter"
    )
    ap.add_argument("file", nargs="?", help="source file (default: stdin)")
    ap.add_argument("--trace", action="store_true", help="show the forcing chain")
    args = ap.parse_args(argv)

    if args.file and args.file != "-":
        with open(args.file, encoding="utf-8") as fh:
            src = fh.read()
    else:
        src = sys.stdin.read()

    interp = Interpreter(trace=args.trace)
    try:
        value = interp.run(src)
    except LangError as exc:
        for line in interp.trace_lines:
            print(line, file=sys.stderr)
        print(f"error: {exc}", file=sys.stderr)
        return 1
    for line in interp.trace_lines:
        print(line, file=sys.stderr)
    print(value)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
