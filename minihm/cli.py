"""Command line interface: parse, type-check, then evaluate a source file."""
from __future__ import annotations

import sys
from pathlib import Path

from .errors import MiniHMError
from .infer import infer_program
from .interp import evaluate, show_value
from .parser import Parser
from .report import render_error
from .source import Source
from .types import show


def run_file(path: str, out=None, err=None) -> int:
    out = sys.stdout if out is None else out
    err = sys.stderr if err is None else err
    src = Source(Path(path).read_text(encoding="utf-8"), name=path)
    try:
        ast = Parser(src).parse_program()
        typ = infer_program(ast)
    except MiniHMError as exc:
        print(render_error(exc, src), file=err)
        return 1
    print(f"type: {show(typ)}", file=out)
    try:
        value = evaluate(ast, {})
    except MiniHMError as exc:
        print(render_error(exc, src), file=err)
        return 1
    print(f"value: {show_value(value)}", file=out)
    return 0


def main(argv=None) -> int:
    argv = list(sys.argv[1:] if argv is None else argv)
    if len(argv) != 1:
        print("usage: python -m minihm FILE", file=sys.stderr)
        return 2
    return run_file(argv[0])
