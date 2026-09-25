"""constkit: multi-module constant language with conditional compilation.

Pure standard library, hand-written lexer/parser/interpreter, no eval().
"""
from .diag import Diagnostic, Span
from .eval import Cancelled, CancelToken, EnumVal, EvalError, OverflowPolicy
from .engine import ApplyResult, Engine, Snapshot, Entry

__all__ = [
    "Diagnostic",
    "Span",
    "Cancelled",
    "CancelToken",
    "EnumVal",
    "EvalError",
    "OverflowPolicy",
    "ApplyResult",
    "Engine",
    "Snapshot",
    "Entry",
]
