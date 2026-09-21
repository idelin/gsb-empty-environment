from __future__ import annotations

import argparse
import sys

from .common import LangError
from .interpreter import Interpreter, format_value
from .parser import parse
from .report import render_syntax_error, render_type_error
from .typer import TypeChecker


def run(source: bytes, show_type: bool = False, execute: bool = True) -> int:
    try:
        ast = parse(source)
        result_type = TypeChecker(source).result_type(ast)
        if show_type:
            print(f"type: {result_type}")
        if execute:
            value = Interpreter().eval(ast, {})
            print(format_value(value))
        return 0
    except LangError as err:
        from .common import SyntaxErr, TypeInferenceError
        if isinstance(err, SyntaxErr):
            print(render_syntax_error(source, err), file=sys.stderr)
        elif isinstance(err, TypeInferenceError):
            print(render_type_error(source, err), file=sys.stderr)
        else:
            print(f"runtime error: {err}", file=sys.stderr)
        return 1


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description="minilang: HM type inference + interpreter")
    ap.add_argument("file", help="source file to type-check and run")
    ap.add_argument("--type-only", action="store_true", help="infer the type without running")
    args = ap.parse_args(argv)
    with open(args.file, "rb") as fh:
        source = fh.read()
    return run(source, show_type=True, execute=not args.type_only)


if __name__ == "__main__":
    raise SystemExit(main())
