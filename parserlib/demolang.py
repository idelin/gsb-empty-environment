"""Demo mini-language: explicit grammar plus a hand-rolled lexer.

The lexer alphabet and every automaton transition are given explicitly; no
regular-expression or automata library is involved.
"""
from __future__ import annotations

import string

from .automata import Lexer, LexerSpec, literal, plus, symbol
from .grammar import Grammar
from .tokens import Token

TERMINALS = frozenset({"id", "num", "=", ";", "+", "(", ")", "print"})

RULES: dict[str, list[tuple[str, ...]]] = {
    "program": [("statement_list",)],
    "statement_list": [("statement", "statement_list"), ()],
    "statement": [("assign", ";"), ("print_stmt", ";")],
    "assign": [("id", "=", "expr")],
    "print_stmt": [("print", "expr")],
    "expr": [("term", "expr_tail")],
    "expr_tail": [("+", "term", "expr_tail"), ()],
    "term": [("id",), ("num",), ("(", "expr", ")")],
}

ALPHABET = frozenset(string.ascii_lowercase + string.digits + "=;+() \t\n")


def program_grammar() -> Grammar:
    return Grammar("program", RULES, TERMINALS)


def statement_grammar() -> Grammar:
    """Same language with ``statement`` as start symbol (used per block)."""
    return Grammar("statement", RULES, TERMINALS)


def build_lexer() -> Lexer:
    letter = symbol(set(string.ascii_lowercase), ALPHABET)
    digit = symbol(set(string.digits), ALPHABET)
    whitespace = symbol({" ", "\t", "\n"}, ALPHABET)
    specs = [
        LexerSpec("print", literal("print", ALPHABET)),
        LexerSpec("id", plus(letter)),
        LexerSpec("num", plus(digit)),
        LexerSpec("=", literal("=", ALPHABET)),
        LexerSpec(";", literal(";", ALPHABET)),
        LexerSpec("+", literal("+", ALPHABET)),
        LexerSpec("(", literal("(", ALPHABET)),
        LexerSpec(")", literal(")", ALPHABET)),
        LexerSpec("<ws>", plus(whitespace), skip=True),
    ]
    return Lexer(specs)


def lex(source: str) -> list[Token]:
    return build_lexer().tokenize(source)


def tokens_from_kinds(kinds: str) -> list[Token]:
    """Build tokens from a whitespace-separated kind string (test helper)."""
    return [
        Token(kind=kind, text=kind, start=i, end=i + 1)
        for i, kind in enumerate(kinds.split())
    ]
