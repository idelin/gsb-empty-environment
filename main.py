"""CLI entry point: run a module from a directory of .mm source files.

Usage: python3.11 main.py <examples-dir> <entry-module>
"""

import pathlib
import sys

from modlang import Interpreter, LangError


def main(argv):
    if len(argv) != 3:
        print("usage: python3.11 main.py <dir> <entry-module>", file=sys.stderr)
        return 2
    directory = pathlib.Path(argv[1])
    sources = {
        path.stem: path.read_text(encoding="utf-8")
        for path in sorted(directory.glob("*.mm"))
    }
    interp = Interpreter(sources)
    try:
        interp.load(argv[2])
    except LangError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
