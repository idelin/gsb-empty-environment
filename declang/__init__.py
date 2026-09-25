"""Declang: a tiny declaration-list language with sync-point error recovery."""

from .lexer import Token, Diagnostic, lex, ALL_KINDS, CANONICAL_TEXT
from .parser import (
    parse_program,
    parse_decl,
    ParseError,
    Decl,
    IntLit,
    StrLit,
    NameRef,
    Paren,
    Bin,
)
from .repair import find_minimal_repairs, Candidate, RepairSearch
from .interp import execute
from .document import Document

__all__ = [
    "Token",
    "Diagnostic",
    "lex",
    "ALL_KINDS",
    "CANONICAL_TEXT",
    "parse_program",
    "parse_decl",
    "ParseError",
    "Decl",
    "IntLit",
    "StrLit",
    "NameRef",
    "Paren",
    "Bin",
    "find_minimal_repairs",
    "Candidate",
    "RepairSearch",
    "execute",
    "Document",
]
