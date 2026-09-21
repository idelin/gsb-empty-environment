"""minilang: a tiny interpreted language with structured control transfer.

Pipeline: source text -> lexer.tokenize -> parser.Parser -> interpreter.
All source positions are UTF-8 byte offsets.
"""

from .errors import LangError
from .interpreter import Interpreter, ThrowSignal
from .lexer import tokenize
from .parser import Parser

__all__ = ["LangError", "Interpreter", "ThrowSignal", "tokenize", "Parser",
           "run_source"]


def run_source(source: str, out=None):
    """Tokenize, parse and execute ``source``; returns the Interpreter."""
    interp = Interpreter(out=out)
    program = Parser(tokenize(source)).parse_program()
    interp.run(program)
    return interp
