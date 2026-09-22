"""MiniLang: a tiny language with structured control flow.

Hand-written lexer, recursive-descent parser and tree-walking interpreter.
No ``eval``, no ``compile``, no parser generators -- standard library only.
All source positions are UTF-8 byte ranges.
"""
from .errors import (
    BreakSignal,
    ContinueSignal,
    ControlTransfer,
    LangThrow,
    ReturnSignal,
    RuntimeFault,
)
from .interp import Interpreter, stringify
from .lexer import LexError, Lexer, Token
from .parser import ParseError, Parser

__all__ = [
    "BreakSignal",
    "ContinueSignal",
    "ControlTransfer",
    "Interpreter",
    "LangThrow",
    "LexError",
    "Lexer",
    "ParseError",
    "Parser",
    "ReturnSignal",
    "RuntimeFault",
    "Token",
    "run_source",
    "stringify",
]


def run_source(source: str, out=None) -> Interpreter:
    """Lex, parse and interpret ``source``. Returns the interpreter used.

    ``out`` (optional) is a callable receiving each printed line, handy for
    tests. Raises LexError / ParseError on syntax problems and LangThrow on
    uncaught throws (with ``.cause`` chains where applicable).
    """
    tokens = Lexer(source).tokenize()
    program = Parser(tokens).parse()
    interp = Interpreter(out=out)
    interp.interpret(program)
    return interp
