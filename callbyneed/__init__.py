"""callbyneed: a call-by-need expression-graph interpreter.

- thunks are forced at most once; results are memoized
- raised exceptions are cached and re-raised on later forces
- self-reference (direct or indirect) is reported as a black hole
  with the full forcing chain
- an optional tracer shows the force/result/cache chain
- source spans are UTF-8 byte intervals
"""

from .errors import (
    BlackHoleError,
    DivByZeroError,
    EvalError,
    LangError,
    LexError,
    ParseError,
    UnboundNameError,
)
from .interpreter import Interpreter, Thunk
from .parser import parse

__all__ = [
    "Interpreter",
    "Thunk",
    "parse",
    "LangError",
    "LexError",
    "ParseError",
    "EvalError",
    "DivByZeroError",
    "UnboundNameError",
    "BlackHoleError",
]
