"""Command-line interface: run a script file or standard input."""

from __future__ import annotations

import sys

from .errors import CalcError
from .interp import Analyzer, Evaluator, format_value
from .lexer import tokenize
from .parser import parse_program


def run_source(source: str) -> list[str]:
    """Analyze the whole program first, then evaluate it.

    Returns one output line per expression statement.
    """
    stmts = parse_program(tokenize(source))
    analyzer = Analyzer()
    for stmt in stmts:
        analyzer.analyze_stmt(stmt)
    evaluator = Evaluator()
    out = []
    for stmt in stmts:
        value = evaluator.eval_stmt(stmt)
        if value is not None:
            out.append(format_value(value))
    return out


def render_error(source: str, err: CalcError) -> str:
    data = source.encode("utf-8")
    start, end = err.span
    line_start = data.rfind(b"\n", 0, start) + 1
    line_end = data.find(b"\n", start)
    if line_end == -1:
        line_end = len(data)
    line = data[line_start:line_end].decode("utf-8", errors="replace")
    col = len(data[line_start:start].decode("utf-8", errors="replace"))
    width = max(1, len(data[start:min(end, line_end)].decode("utf-8", errors="replace")))
    lineno = data.count(b"\n", 0, start) + 1
    return (
        f"error: {err.message}\n"
        f"  line {lineno}: {line}\n"
        f"  {' ' * col}{'^' * width}"
    )


def main(argv=None):
    argv = list(sys.argv[1:] if argv is None else argv)
    if argv and argv[0] in ("-h", "--help"):
        print("usage: python3 -m unitlang [script]   (reads stdin without args)")
        return
    if argv:
        with open(argv[0], "r", encoding="utf-8") as fh:
            source = fh.read()
    else:
        source = sys.stdin.read()
    try:
        for line in run_source(source):
            print(line)
    except CalcError as err:
        print(render_error(source, err), file=sys.stderr)
        sys.exit(1)
