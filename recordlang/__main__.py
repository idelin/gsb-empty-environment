"""Command line entry: python -m recordlang <file.rl>"""

from __future__ import annotations

import sys

from .api import run_program
from .errors import LangError


def main(argv=None) -> int:
    argv = list(sys.argv[1:] if argv is None else argv)
    if len(argv) != 1:
        print("usage: python -m recordlang <file.rl>", file=sys.stderr)
        return 2
    with open(argv[0], encoding="utf-8") as f:
        src = f.read()
    try:
        for line in run_program(src):
            print(line)
    except LangError as err:
        if err.span is not None:
            start, end = err.span
            excerpt = src.encode("utf-8")[start:end].decode("utf-8", "replace")
            print(
                f"error: {err.message} [bytes {start}..{end}: {excerpt!r}]",
                file=sys.stderr,
            )
        else:
            print(f"error: {err.message}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
