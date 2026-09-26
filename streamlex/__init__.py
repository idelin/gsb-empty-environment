"""streamlex: a streaming maximal-munch lexer over explicit DFAs.

Pure Python 3.11 standard library; no regex or automaton packages.
"""
from .builders import char_class_plus, literal, star_then_literal
from .dfa import DFA, DFAError
from .lexer import (
    AmbiguousRuleError,
    Checkpoint,
    FeedResult,
    Lexer,
    LexError,
    StateStackError,
    Status,
    Token,
)
from .reference import lex_all
from .rules import NO_ACTION, Action, Pop, Push, Rule, Switch

__all__ = [
    "Action",
    "AmbiguousRuleError",
    "Checkpoint",
    "DFA",
    "DFAError",
    "FeedResult",
    "LexError",
    "Lexer",
    "NO_ACTION",
    "Pop",
    "Push",
    "Rule",
    "StateStackError",
    "Status",
    "Switch",
    "Token",
    "char_class_plus",
    "lex_all",
    "literal",
    "star_then_literal",
]

__version__ = "0.1.0"
