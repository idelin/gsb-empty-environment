"""A tiny language with incremental parsing.

- Hand-written lexer/parser/interpreter (no eval, no external compilers).
- All source positions are UTF-8 byte ranges.
- Error recovery produces explicit ErrorNodes and never swallows the
  following declaration.
- ``Session.edit`` re-parses incrementally, reusing unchanged subtrees.
"""
from .ast_nodes import (
    Node, Program, FnDecl, LetDecl, Binary, Unary, Call, Group, If,
    Num, Name, ErrorNode, dump,
)
from .incremental import Session, parse, to_bytes, is_char_boundary
from .interpreter import (
    Env, Function, LanguageError, collect_errors, evaluate,
    evaluate_expression, interpret, run,
)
from .lexer import lex, Token

__all__ = [
    "Node", "Program", "FnDecl", "LetDecl", "Binary", "Unary", "Call",
    "Group", "If", "Num", "Name", "ErrorNode", "dump",
    "Session", "parse", "to_bytes", "is_char_boundary",
    "Env", "Function", "LanguageError", "collect_errors", "evaluate",
    "evaluate_expression", "interpret", "run",
    "lex", "Token",
]
