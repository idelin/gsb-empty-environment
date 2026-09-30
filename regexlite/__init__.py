"""regexlite -- a small, self-contained regular expression engine.

Features: concatenation, ordered alternation, greedy star, character
classes, parentheses, and labelled captures; compiled to a tagged
epsilon-NFA over an explicitly supplied alphabet, determinized on demand
with bounded caches, and matched leftmost-longest with deterministic
priority rules.  No regex or automata libraries are used.
"""
from .ast_nodes import (
    Alt, CharClass, Concat, Dot, Empty, Group, Literal, Node, Star,
)
from .dfa import BudgetExceeded, Determinizer, DFAState
from .matcher import Match, Regex, StreamMatcher
from .nfa import NFA, CompileError, build_nfa, class_symbols
from .parser import ParseError, Parsed, parse
from .pike import pike_match_at, pike_search
from .reference import Inconclusive, ref_search

__all__ = [
    "Alt", "BudgetExceeded", "CharClass", "CompileError", "Concat",
    "Determinizer", "DFAState", "Dot", "Empty", "Group", "Inconclusive",
    "Literal", "Match", "NFA", "Node", "ParseError", "Parsed", "Regex",
    "Star", "StreamMatcher", "build_nfa", "class_symbols", "parse",
    "pike_match_at", "pike_search", "ref_search",
]
