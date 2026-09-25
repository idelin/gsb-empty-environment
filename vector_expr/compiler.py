"""Compiler: parse -> type-check -> vectorized plan.

Type checking happens entirely at compile time against the declared
schema (column name -> Type).  Any type error raises
:class:`~vector_expr.errors.CompileError` before a single row is read.

Rules:

* arithmetic ``+ - * / %`` requires both operands INT64 or both
  FLOAT64 (no implicit mixing; use CAST);
* comparisons require equal types; ordering (``<`` etc.) is numeric
  only, ``=``/``!=`` also accept BOOL;
* ``AND``/``OR``/``NOT`` and the IF condition require BOOL;
* IF branches must have the same type;
* ``CAST(x AS INT64|FLOAT64)`` converts numerics explicitly.
"""

from __future__ import annotations

from . import ast_nodes as ast
from .errors import CompileError
from .parser import parse
from .plan import (
    AndNode,
    ArithNode,
    CastNode,
    ColumnNode,
    CompareNode,
    IfNode,
    LiteralNode,
    NegateNode,
    Node,
    NotNode,
    NullNode,
    OrNode,
    Plan,
)
from .types import Type

_NUMERIC = (Type.INT64, Type.FLOAT64)


class _Compiler:
    def __init__(self, source: str, schema: dict[str, Type]):
        self.source = source
        self.schema = schema
        self.used: dict[str, Type] = {}

    def _fail(self, pos: int, message: str) -> None:
        caret = " " * pos + "^"
        raise CompileError(f"{message}\n  {self.source}\n  {caret}")

    def compile(self, expr: ast.Expr) -> Node:
        method = getattr(self, f"_compile_{type(expr).__name__}")
        return method(expr)

    def _compile_Literal(self, expr: ast.Literal) -> Node:
        v = expr.value
        if v is None:
            self._fail(
                expr.pos,
                "untyped NULL literal: use CAST(NULL AS INT64|FLOAT64|BOOL)",
            )
        if isinstance(v, bool):
            return LiteralNode(v, Type.BOOL)
        if isinstance(v, int):
            return LiteralNode(v, Type.INT64)
        return LiteralNode(v, Type.FLOAT64)

    def _compile_ColumnRef(self, expr: ast.ColumnRef) -> Node:
        try:
            type_ = self.schema[expr.name]
        except KeyError:
            known = ", ".join(sorted(self.schema)) or "<none>"
            self._fail(
                expr.pos,
                f"unknown column {expr.name!r} (schema has: {known})",
            )
        self.used[expr.name] = type_
        return ColumnNode(expr.name, type_)

    def _compile_UnaryOp(self, expr: ast.UnaryOp) -> Node:
        operand = self.compile(expr.operand)
        if expr.op == "-":
            if operand.type not in _NUMERIC:
                self._fail(expr.pos, f"unary '-' needs a numeric operand, "
                                     f"got {operand.type}")
            return NegateNode(operand)
        if operand.type is not Type.BOOL:
            self._fail(expr.pos, f"NOT needs a BOOL operand, got {operand.type}")
        return NotNode(operand)

    def _compile_BinaryOp(self, expr: ast.BinaryOp) -> Node:
        left = self.compile(expr.left)
        right = self.compile(expr.right)
        op = expr.op
        if op in ("AND", "OR"):
            for side, node in (("left", left), ("right", right)):
                if node.type is not Type.BOOL:
                    self._fail(expr.pos, f"{op} needs BOOL operands, "
                                         f"{side} side is {node.type}")
            return AndNode(left, right) if op == "AND" else OrNode(left, right)
        if op in ("+", "-", "*", "/", "%"):
            if left.type not in _NUMERIC or right.type not in _NUMERIC:
                self._fail(expr.pos, f"arithmetic '{op}' needs numeric "
                                     f"operands, got {left.type} and "
                                     f"{right.type}")
            if left.type is not right.type:
                self._fail(
                    expr.pos,
                    f"cannot mix {left.type} and {right.type} in '{op}'; "
                    f"add an explicit CAST",
                )
            return ArithNode(op, left, right, left.type)
        if left.type is not right.type:
            self._fail(
                expr.pos,
                f"cannot compare {left.type} with {right.type}; "
                f"add an explicit CAST",
            )
        if op not in ("=", "!=") and left.type not in _NUMERIC:
            self._fail(expr.pos, f"ordering comparison '{op}' needs numeric "
                                 f"operands, got {left.type}")
        return CompareNode(op, left, right)

    def _compile_If(self, expr: ast.If) -> Node:
        cond = self.compile(expr.cond)
        if cond.type is not Type.BOOL:
            self._fail(expr.pos, f"IF condition must be BOOL, got {cond.type}")
        then = self.compile(expr.then)
        otherwise = self.compile(expr.otherwise)
        if then.type is not otherwise.type:
            self._fail(
                expr.pos,
                f"IF branches have different types: {then.type} vs "
                f"{otherwise.type}; add an explicit CAST",
            )
        return IfNode(cond, then, otherwise, then.type)

    def _compile_Cast(self, expr: ast.Cast) -> Node:
        try:
            target = Type(expr.target)
        except ValueError:
            valid = ", ".join(t.value for t in Type)
            self._fail(expr.pos, f"unknown cast target {expr.target!r} "
                                 f"(valid: {valid})")
        if isinstance(expr.operand, ast.Literal) and expr.operand.value is None:
            return NullNode(target)  # typed null literal
        operand = self.compile(expr.operand)
        if target is Type.BOOL:
            self._fail(expr.pos, "CAST to BOOL is not supported")
        if operand.type is Type.BOOL:
            self._fail(expr.pos, "cannot CAST a BOOL operand")
        if operand.type is target:
            return operand  # no-op cast
        return CastNode(operand, target)


def compile_expression(source: str, schema: dict[str, Type]) -> Plan:
    """Compile ``source`` against ``schema`` into an executable Plan.

    Raises :class:`CompileError` for syntax and type errors; no data is
    touched at this stage.
    """
    compiler = _Compiler(source, schema)
    root = compiler.compile(parse(source))
    return Plan(root, compiler.used, source)
