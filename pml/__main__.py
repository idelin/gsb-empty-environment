from __future__ import annotations

import sys

from .api import run_source
from .errors import CompileError
from .interpreter import render_value


def main(argv: list[str] | None = None) -> int:
    argv = list(sys.argv[1:] if argv is None else argv)
    if len(argv) != 1:
        print("usage: python -m pml FILE.pml", file=sys.stderr)
        return 2
    path = argv[0]
    with open(path, "rb") as source_file:
        source = source_file.read()
    try:
        result = run_source(source, path)
    except CompileError as error:
        print(str(error), file=sys.stderr)
        return 1
    if result is not None:
        print(render_value(result))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
