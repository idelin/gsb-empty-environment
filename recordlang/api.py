"""Program-level driver: parse, type check (compile time), then run."""

from __future__ import annotations

from .ast_nodes import LetStmt
from .interpreter import eval_node, format_value
from .lexer import Lexer
from .parser import Parser
from .typecheck import type_of


def parse_program(src: str):
    return Parser(Lexer(src).tokenize()).parse_program()


def typecheck_program(src: str) -> list:
    """Compile-time pass only. Returns the types of the expression statements."""
    stmts = parse_program(src)
    tenv = {}
    types = []
    for stmt in stmts:
        t = type_of(stmt.expr, tenv)
        if isinstance(stmt, LetStmt):
            tenv[stmt.name] = t
        else:
            types.append(t)
    return types


def run_program(src: str) -> list[str]:
    """Type check the whole program first; evaluate only if it is well typed."""
    stmts = parse_program(src)
    tenv = {}
    for stmt in stmts:  # compile time
        t = type_of(stmt.expr, tenv)
        if isinstance(stmt, LetStmt):
            tenv[stmt.name] = t
    env = {}
    outputs = []
    for stmt in stmts:  # run time
        value = eval_node(stmt.expr, env)
        if isinstance(stmt, LetStmt):
            env[stmt.name] = value
        else:
            outputs.append(format_value(value))
    return outputs
