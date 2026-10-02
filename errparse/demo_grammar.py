"""Demo language: explicit DFA lexer spec + LL(1) statement grammar."""
from __future__ import annotations

from .grammar import Grammar
from .lexer import DFA, tokenize
from .parser import PredictiveParser

LETTERS = "abcdefghijklmnopqrstuvwxyz"
DIGITS = "0123456789"
SYMBOL_CHARS = {"=": "ASSIGN", ";": "SEMI", "+": "PLUS",
                "(": "LPAREN", ")": "RPAREN"}
ALPHABET = frozenset(LETTERS + DIGITS + "".join(SYMBOL_CHARS))
KEYWORDS = {"if": "IF", "print": "PRINT"}


def build_dfa():
    transitions = {}
    for c in LETTERS:
        transitions[("start", c)] = "id"
        transitions[("id", c)] = "id"
    for c in DIGITS:
        transitions[("start", c)] = "num"
        transitions[("num", c)] = "num"
        transitions[("id", c)] = "id"
    for c, name in SYMBOL_CHARS.items():
        transitions[("start", c)] = "sym_" + name
    accepts = {"id": "ID", "num": "NUM"}
    for c, name in SYMBOL_CHARS.items():
        accepts["sym_" + name] = name
    return DFA(alphabet=ALPHABET, transitions=transitions,
               start="start", accepts=accepts)


PRODUCTIONS = {
    "program": [("stmt_list",)],
    "stmt_list": [("stmt", "stmt_list"), ()],
    "stmt": [
        ("ID", "ASSIGN", "expr", "SEMI"),
        ("PRINT", "expr", "SEMI"),
        ("IF", "expr", "stmt"),
    ],
    "expr": [("term", "expr_tail")],
    "expr_tail": [("PLUS", "term", "expr_tail"), ()],
    "term": [("ID",), ("NUM",), ("LPAREN", "expr", "RPAREN")],
}


def make_grammar():
    return Grammar("program", PRODUCTIONS)


def make_parser():
    return PredictiveParser(make_grammar())


def lex(text):
    return tokenize(build_dfa(), text, keywords=KEYWORDS)
