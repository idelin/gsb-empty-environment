"""Command-line runner: ``python -m minilang <file>``."""

from __future__ import annotations

import sys

from .errors import LangError
from .interpreter import Interpreter, ThrowSignal, show
from .lexer import tokenize
from .parser import Parser


def line_col(source: str, byte_offset: int) -> tuple[int, int]:
    """Convert a UTF-8 byte offset to a 1-based (line, column) pair."""
    prefix = source.encode("utf-8")[:byte_offset].decode("utf-8",
                                                          errors="ignore")
    line = prefix.count("\n") + 1
    col = len(prefix) - prefix.rfind("\n")
    return line, col


def report_span(source: str, path: str, message: str, span) -> str:
    if span is None:
        return f"{path}: error: {message}"
    line, col = line_col(source, span[0])
    return (f"{path}:{line}:{col}: error: {message} "
            f"[bytes {span[0]}..{span[1]}]")


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        print("usage: python -m minilang <file>", file=sys.stderr)
        return 2
    path = argv[1]
    try:
        with open(path, "r", encoding="utf-8") as fh:
            source = fh.read()
    except OSError as exc:
        print(f"error: cannot read {path}: {exc}", file=sys.stderr)
        return 2

    try:
        program = Parser(tokenize(source)).parse_program()
        Interpreter().run(program)
    except LangError as err:
        print(report_span(source, path, err.message, err.span),
              file=sys.stderr)
        return 1
    except ThrowSignal as sig:
        print(report_span(source, path,
                          f"uncaught throw: {show(sig.value)}", sig.span),
              file=sys.stderr)
        cause = sig.cause
        while cause is not None:
            print(f"  caused by: {show(cause.value)}", file=sys.stderr)
            cause = cause.cause
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
