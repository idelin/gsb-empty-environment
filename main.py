#!/usr/bin/env python3
"""Command-line runner:  python3.11 main.py <source-file>

Errors are reported with their UTF-8 byte range plus a derived line/column
for convenience. Uncaught throws print their full ``cause`` chain.
"""
import sys

from minilang import Interpreter, LangThrow, LexError, Lexer, ParseError, Parser, stringify


def line_col(source: str, byte_offset: int):
    """Derive 1-based (line, column) from a UTF-8 byte offset."""
    prefix = source.encode("utf-8")[:byte_offset].decode("utf-8", errors="ignore")
    line = prefix.count("\n") + 1
    col = len(prefix.rsplit("\n", 1)[-1]) + 1
    return line, col


def main(argv) -> int:
    if len(argv) != 2:
        print("usage: python3.11 main.py <source-file>", file=sys.stderr)
        return 2

    with open(argv[1], "r", encoding="utf-8") as handle:
        source = handle.read()

    try:
        tokens = Lexer(source).tokenize()
        program = Parser(tokens).parse()
    except (LexError, ParseError) as err:
        line, col = line_col(source, err.start)
        print(
            f"syntax error at line {line}, col {col} "
            f"(bytes {err.start}..{err.end}): {err.message}",
            file=sys.stderr,
        )
        return 65

    interp = Interpreter()
    try:
        interp.interpret(program)
    except LangThrow as thrown:
        chain = []
        current = thrown
        while current is not None:
            chain.append(current)
            current = current.cause
        first = chain[0]
        line, col = line_col(source, first.start or 0)
        print(
            f"uncaught throw at line {line}, col {col} "
            f"(bytes {first.start}..{first.end}): {stringify(first.value)}",
            file=sys.stderr,
        )
        for cause in chain[1:]:
            print(
                f"  caused by (bytes {cause.start}..{cause.end}): "
                f"{stringify(cause.value)}",
                file=sys.stderr,
            )
        return 70
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
