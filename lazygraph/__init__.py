"""call-by-need 表达式图解释器。

对外主要接口：

- :func:`run`：直接运行一段源码，返回整数结果。
- :class:`Interpreter`：编译并暴露 thunk 图，便于观察缓存状态。
- :class:`Tracer`：记录强制求值链、缓存命中与 tick 事件。
"""
from __future__ import annotations

from .interpreter import Env, Evaluator, Interpreter, Thunk, Tracer
from .errors import (
    BlackHoleError,
    DivByZeroError,
    EvalError,
    LazyError,
    LexError,
    ParseError,
    UnboundVariableError,
)
from .sourcemap import Span, SourceMap

__all__ = [
    "run",
    "Interpreter",
    "Evaluator",
    "Tracer",
    "Thunk",
    "Env",
    "Span",
    "SourceMap",
    "LazyError",
    "LexError",
    "ParseError",
    "EvalError",
    "DivByZeroError",
    "UnboundVariableError",
    "BlackHoleError",
]


def run(source: str | bytes, tracer: Tracer | None = None) -> int:
    """编译并强制求值根表达式，返回整数结果。"""
    return Interpreter(source, tracer=tracer).run()
