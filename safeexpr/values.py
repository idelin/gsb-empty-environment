"""Helpers for the plain-value domain the evaluator works in.

The only values that may flow through an expression are: ``None``,
``bool``, ``int``, ``float``, ``str`` and (recursively) ``list`` of
those.  Nothing else ever crosses the boundary in either direction.
"""

from __future__ import annotations

import math

from .errors import EvalError, IntegerLimitError


def is_num(value) -> bool:
    return isinstance(value, (int, float)) and not isinstance(value, bool)


def truthy(value) -> bool:
    return bool(value)


def int_digits(value: int) -> int:
    return len(str(abs(value)))


def check_number(value, limits, span) -> None:
    """Reject oversized integers and non-finite floats."""
    if isinstance(value, bool):
        return
    if isinstance(value, int):
        if int_digits(value) > limits.max_int_digits:
            raise IntegerLimitError(
                f"integer exceeds limit of {limits.max_int_digits} digits", span
            )
    elif isinstance(value, float):
        if math.isinf(value) or math.isnan(value):
            raise EvalError("numeric overflow", span)


def render(value) -> str:
    """Deterministic rendering used by the ``str`` builtin."""
    if value is None:
        return "null"
    if value is True:
        return "true"
    if value is False:
        return "false"
    if isinstance(value, str):
        return value
    if isinstance(value, list):
        return "[" + ", ".join(render(item) for item in value) + "]"
    return str(value)
