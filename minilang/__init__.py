"""minilang: a tiny declaration language with minimal-edit-cost recovery."""
from dataclasses import dataclass

from .diagnostics import Diagnostic
from .document import Document
from .interpreter import execute
from .lexer import lex
from .parser import parse_program, recover_decl

__all__ = ["Diagnostic", "Document", "Result", "execute", "lex",
           "parse_program", "recover_decl", "run_source"]


@dataclass
class Result:
    tokens: list
    program: object
    diagnostics: list
    env: dict


def run_source(source, **recovery_kw):
    tokens, lex_diagnostics = lex(source)
    program, diagnostics = parse_program(tokens, lex_diagnostics,
                                         **recovery_kw)
    env, runtime_diagnostics = execute(program)
    return Result(tokens, program, diagnostics + runtime_diagnostics, env)
