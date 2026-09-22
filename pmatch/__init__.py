"""A tiny pattern-matching compiler and interpreter.

Supports tagged unions, tuples, literals, wildcards, variable bindings and
guards. Matches are compiled to decision trees with unreachable-case and
exhaustiveness checking (guards never prove exhaustiveness). All source
positions are UTF-8 byte ranges. No eval, no external parser: the lexer,
parser, compiler and interpreter are all implemented here with the standard
library only.
"""

from .api import compile_source, run_source
from .interpreter import MatchError
from .nodes import Diag, Variant

__all__ = ["compile_source", "run_source", "MatchError", "Diag", "Variant"]
