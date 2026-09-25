"""Type-checks expression ASTs and lowers them to vector execution plans."""
from __future__ import annotations

from . import expr as ast
from .errors import CompileError
from .plan import (BinaryPlan, CastPlan, ColumnPlan, IfPlan, IsNullPlan,
                   LiteralPlan, NotPlan)
from .types import (ALL_TYPES, BOOL, FLOAT64, INT64, STRING, Type,
                    common_type, promote_numeric)


def compile(expression, schema):
    """Type-check ``expression`` against ``schema`` and return an executable plan.

    All type errors are reported here, before any data is evaluated.
    """
    return _compile(ast.ensure(expression), dict(schema))


def _require(ok, message):
    if not ok:
        raise CompileError(message)


def _compile(node, schema):
    if isinstance(node, ast.Literal):
        return _compile_literal(node)
    if isinstance(node, ast.Column):
        _require(node.name in schema, f"unknown column {node.name!r}")
        return ColumnPlan(node.name, schema[node.name])
    if isinstance(node, ast.Binary):
        return _compile_binary(node, schema)
    if isinstance(node, ast.Not):
        operand = _compile(node.operand, schema)
        _require(operand.type == BOOL, f"NOT requires BOOL, got {operand.type}")
        return NotPlan(operand)
    if isinstance(node, ast.IsNull):
        return IsNullPlan(_compile(node.expr, schema))
    if isinstance(node, ast.If):
        return _compile_if(node, schema)
    if isinstance(node, ast.Cast):
        return _compile_cast(node, schema)
    raise CompileError(f"unsupported expression: {node!r}")


def _compile_literal(node):
    value, typ = node.value, node.type
    if isinstance(typ, str):
        _require(typ in ALL_TYPES, f"unknown type {typ!r}")
        typ = ALL_TYPES[typ]
    if typ is None:
        _require(value is not None,
                 "null literal needs an explicit type, e.g. lit(None, INT32)")
        typ = _infer_literal_type(value)
    _validate_literal(value, typ)
    if typ == FLOAT64 and isinstance(value, int) and not isinstance(value, bool):
        value = float(value)
    return LiteralPlan(value, typ)


def _infer_literal_type(value):
    if isinstance(value, bool):
        return BOOL
    if isinstance(value, int):
        return INT64
    if isinstance(value, float):
        return FLOAT64
    if isinstance(value, str):
        return STRING
    raise CompileError(f"unsupported literal {value!r}")


def _validate_literal(value, typ):
    if value is None:
        return
    if typ == BOOL:
        _require(isinstance(value, bool), f"literal {value!r} is not BOOL")
    elif typ.is_integer:
        _require(isinstance(value, int) and not isinstance(value, bool),
                 f"literal {value!r} is not an integer")
        _require(typ.min_value <= value <= typ.max_value,
                 f"literal {value} does not fit {typ}")
    elif typ == FLOAT64:
        _require(isinstance(value, (int, float)) and not isinstance(value, bool),
                 f"literal {value!r} is not numeric")
    else:
        _require(isinstance(value, str), f"literal {value!r} is not a string")


def _compile_binary(node, schema):
    left = _compile(node.left, schema)
    right = _compile(node.right, schema)
    op = node.op
    if op in ast.ARITH_OPS:
        _require(left.type.is_numeric and right.type.is_numeric,
                 f"{op} requires numeric operands, got {left.type} and {right.type}")
        if op == "mod":
            _require(left.type.is_integer and right.type.is_integer,
                     f"mod requires integer operands, got {left.type} and {right.type}")
        return BinaryPlan(op, left, right, promote_numeric(left.type, right.type))
    if op in ast.COMPARE_OPS:
        _require(_comparable(op, left.type, right.type),
                 f"cannot compare {left.type} with {right.type}")
        return BinaryPlan(op, left, right, BOOL)
    _require(left.type == BOOL and right.type == BOOL,
             f"{op} requires BOOL operands, got {left.type} and {right.type}")
    return BinaryPlan(op, left, right, BOOL)


def _comparable(op, left, right):
    if left.is_numeric and right.is_numeric:
        return True
    if left != right:
        return False
    if op in ("eq", "ne"):
        return True
    return left == STRING


def _compile_if(node, schema):
    cond = _compile(node.cond, schema)
    _require(cond.type == BOOL, f"IF condition must be BOOL, got {cond.type}")
    then = _compile(node.then, schema)
    otherwise = _compile(node.otherwise, schema)
    result = common_type(then.type, otherwise.type)
    _require(result is not None,
             f"IF branches have incompatible types {then.type} and {otherwise.type}")
    return IfPlan(cond, then, otherwise, result)


def _compile_cast(node, schema):
    operand = _compile(node.expr, schema)
    target = node.target
    if isinstance(target, str):
        _require(target in ALL_TYPES, f"unknown type {target!r}")
        target = ALL_TYPES[target]
    _require(isinstance(target, Type), f"invalid cast target {target!r}")
    _require(_cast_allowed(operand.type, target),
             f"cannot cast {operand.type} to {target}")
    return CastPlan(operand, target)


def _cast_allowed(source, target):
    if source == target:
        return True
    if target == STRING:
        return True
    if source == STRING:
        return target.is_numeric or target == BOOL
    return (source.is_numeric or source == BOOL) and \
           (target.is_numeric or target == BOOL)
