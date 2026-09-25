"""Tree-walking interpreter for constant expressions.

Values are: int (arbitrary precision), EnumVal, or nested lists of those.
Division '/' and modulo '%' follow floor semantics. No eval()/compile().
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Protocol

from .diag import Span
from .parser import ArrayLit, Bin, Index, IntLit, Neg, Ref


class Cancelled(Exception):
    """Raised when a CancelToken is tripped mid-recomputation."""


class CancelToken:
    def __init__(self) -> None:
        self._cancelled = False

    def cancel(self) -> None:
        self._cancelled = True

    @property
    def cancelled(self) -> bool:
        return self._cancelled

    def throw_if_cancelled(self) -> None:
        if self._cancelled:
            raise Cancelled()


class EvalError(Exception):
    def __init__(self, code: str, message: str, span: Span, chain: tuple = ()):
        super().__init__(message)
        self.code = code
        self.message = message
        self.span = span
        self.chain = tuple(chain)


@dataclass(frozen=True)
class EnumVal:
    enum: str
    member: str
    value: int


class OverflowPolicy:
    """Integer policy: 'exact' (arbitrary precision), 'wrap' (signed N-bit),
    or 'error' (reject values outside the signed N-bit range)."""

    def __init__(self, mode: str = "exact", bits: int = 64):
        if mode not in ("exact", "wrap", "error"):
            raise ValueError(f"unknown overflow mode {mode!r}")
        self.mode = mode
        self.bits = bits
        self.low = -(1 << (bits - 1))
        self.high = (1 << (bits - 1)) - 1
        self.span_mod = 1 << bits

    def apply(self, value: int, span: Span) -> int:
        if self.mode == "exact":
            return value
        if self.mode == "wrap":
            return (value - self.low) % self.span_mod + self.low
        if not (self.low <= value <= self.high):
            raise EvalError(
                "overflow",
                f"integer {value} out of range for signed {self.bits}-bit "
                f"[{self.low}, {self.high}]",
                span,
            )
        return value


class Resolver(Protocol):
    def resolve_ref(self, parts: tuple[str, ...], span: Span) -> Any: ...

    def def_expr_of(self, parts: tuple[str, ...]):
        """Return (label, name_span, defining_expr) for a const ref, or None."""


def _as_int(value: Any, span: Span) -> int:
    if isinstance(value, EnumVal):
        return value.value
    if isinstance(value, bool) or not isinstance(value, int):
        raise EvalError("type-error", f"value {value!r} is not an integer", span)
    return value


def _zero_chain(divisor: object, resolver: Resolver) -> tuple[tuple[str, Span | None], ...]:
    """Trace where a zero divisor came from by following plain references."""
    chain: list[tuple[str, Span | None]] = []
    node = divisor
    seen: set[tuple[str, ...]] = set()
    while isinstance(node, Ref):
        if node.parts in seen:
            break
        seen.add(node.parts)
        target = resolver.def_expr_of(node.parts)
        if target is None:
            break
        label, name_span, sub_expr = target
        chain.append((f"zero divisor originated at {label}", name_span))
        node = sub_expr
    return tuple(chain)


def eval_expr(node: object, resolver: Resolver, policy: OverflowPolicy,
              token: CancelToken | None) -> Any:
    if token is not None:
        token.throw_if_cancelled()

    if isinstance(node, IntLit):
        return policy.apply(node.value, node.span)

    if isinstance(node, Ref):
        return resolver.resolve_ref(node.parts, node.span)

    if isinstance(node, Neg):
        v = _as_int(eval_expr(node.operand, resolver, policy, token), node.span)
        return policy.apply(-v, node.span)

    if isinstance(node, Bin):
        left = _as_int(eval_expr(node.left, resolver, policy, token), node.left.span)
        right = _as_int(eval_expr(node.right, resolver, policy, token), node.right.span)
        if node.op == "+":
            result = left + right
        elif node.op == "-":
            result = left - right
        elif node.op == "*":
            result = left * right
        elif node.op == "/":
            if right == 0:
                raise EvalError(
                    "div-by-zero",
                    "division by zero",
                    node.span,
                    _zero_chain(node.right, resolver),
                )
            result = left // right
        elif node.op == "%":
            if right == 0:
                raise EvalError(
                    "div-by-zero",
                    "modulo by zero",
                    node.span,
                    _zero_chain(node.right, resolver),
                )
            result = left % right
        else:  # pragma: no cover - parser guarantees this
            raise EvalError("type-error", f"unknown operator {node.op!r}", node.span)
        return policy.apply(result, node.span)

    if isinstance(node, Index):
        target = eval_expr(node.target, resolver, policy, token)
        index = _as_int(eval_expr(node.index, resolver, policy, token), node.index.span)
        if not isinstance(target, list):
            raise EvalError("type-error", "index applied to non-array value", node.span)
        if not (0 <= index < len(target)):
            raise EvalError(
                "index-out-of-range",
                f"array index {index} out of range (length {len(target)})",
                node.span,
            )
        return target[index]

    if isinstance(node, ArrayLit):
        return [eval_expr(item, resolver, policy, token) for item in node.items]

    raise EvalError("type-error", f"cannot evaluate {type(node).__name__}", getattr(node, "span", None))
