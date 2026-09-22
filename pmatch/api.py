"""Public API: parse + compile + run a source program."""
from __future__ import annotations

from . import interpreter
from .compiler import compile_program
from .lexer import PError
from .nodes import Diag
from .parser import parse


def compile_source(src):
    """Returns (program_or_None, tree_or_None, [Diag])."""
    try:
        prog = parse(src)
    except PError as e:
        return None, None, [Diag("error", e.message, e.span)]
    tree, diags = compile_program(prog)
    return prog, tree, diags


def run_source(src):
    """Compile and interpret ``src``. Returns (value_or_None, [Diag]).

    Interpretation is skipped when there are errors. Warnings (unreachable
    cases, non-exhaustive match) do not prevent execution; a non-exhaustive
    match may raise ``interpreter.MatchError`` at run time.
    """
    prog, tree, diags = compile_source(src)
    if tree is None:
        return None, diags
    value = interpreter.run(tree, prog)
    return value, diags
