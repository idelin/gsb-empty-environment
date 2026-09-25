"""Public entry point for compiling and running Rill programs."""

from __future__ import annotations

from pathlib import Path

from .checker import Checker
from .errors import CompileError, EvalError, RillError
from .interpreter import Interpreter
from .lexer import Lexer
from .parser import Parser


def compile_source(source: str) -> tuple:
    tokens = Lexer(source).tokenize()
    program = Parser(tokens).parse_program()
    Checker(program).check()
    return program


def run_source(source: str) -> str:
    program = compile_source(source)
    return Interpreter(program).run()


def run_file(path: str | Path) -> str:
    source = Path(path).read_text(encoding="utf-8")
    try:
        output = run_source(source)
    except RillError as err:
        print(err.render(source, str(path)))
        raise SystemExit(1)
    print(output, end="")
    return output
