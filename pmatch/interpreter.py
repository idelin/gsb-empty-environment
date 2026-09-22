"""Tree-walking interpreter for expressions and compiled decision trees."""
from __future__ import annotations

from .compiler import Fail, Guard, Leaf, Switch
from .nodes import (EBin, ECall, ECtor, ELit, ETuple, EUn, EVar, Variant,
                    lit_key)


class MatchError(Exception):
    """Raised when a non-exhaustive match meets an uncovered value."""


def _value_key(v):
    if isinstance(v, Variant):
        return ("ctor", v.tag)
    if isinstance(v, tuple):
        return ("tuple", len(v))
    return lit_key(v)


def _get_path(value, path):
    for i in path:
        if isinstance(value, Variant):
            value = value.args[i]
        elif isinstance(value, tuple):
            value = value[i]
        else:
            raise MatchError(f"cannot project field {i} of {value!r}")
    return value


def _strict_eq(a, b):
    if isinstance(a, bool) or isinstance(b, bool):
        return type(a) is type(b) and a == b
    return a == b


def _arith(op, l, r):
    if op == "+":
        if isinstance(l, int) and isinstance(r, int) \
                and not isinstance(l, bool) and not isinstance(r, bool):
            return l + r
        if isinstance(l, str) and isinstance(r, str):
            return l + r
        raise MatchError(f"bad operands for '+': {l!r}, {r!r}")
    if not (isinstance(l, int) and isinstance(r, int)) \
            or isinstance(l, bool) or isinstance(r, bool):
        raise MatchError(f"bad operands for {op!r}: {l!r}, {r!r}")
    if op == "-":
        return l - r
    if op == "*":
        return l * r
    if op == "/":
        return l // r
    if op == "%":
        return l % r
    raise AssertionError(op)


def eval_expr(e, env):
    if isinstance(e, ELit):
        return e.value
    if isinstance(e, EVar):
        return env[e.name]
    if isinstance(e, ECtor):
        return Variant(e.tag, tuple(eval_expr(a, env) for a in e.args))
    if isinstance(e, ETuple):
        return tuple(eval_expr(a, env) for a in e.items)
    if isinstance(e, EUn):
        v = eval_expr(e.operand, env)
        if e.op == "-":
            if not isinstance(v, int) or isinstance(v, bool):
                raise MatchError(f"bad operand for unary '-': {v!r}")
            return -v
        if e.op == "not":
            return not v
        raise AssertionError(e.op)
    if isinstance(e, EBin):
        if e.op == "and":
            return bool(eval_expr(e.left, env)) and bool(eval_expr(e.right, env))
        if e.op == "or":
            return bool(eval_expr(e.left, env)) or bool(eval_expr(e.right, env))
        l = eval_expr(e.left, env)
        r = eval_expr(e.right, env)
        if e.op in ("+", "-", "*", "/", "%"):
            return _arith(e.op, l, r)
        if e.op == "==":
            return _strict_eq(l, r)
        if e.op == "!=":
            return not _strict_eq(l, r)
        if e.op == "<":
            return l < r
        if e.op == "<=":
            return l <= r
        if e.op == ">":
            return l > r
        if e.op == ">=":
            return l >= r
        raise AssertionError(e.op)
    if isinstance(e, ECall):
        args = [eval_expr(a, env) for a in e.args]
        if e.name == "len":
            if isinstance(args[0], (str, tuple)):
                return len(args[0])
            raise MatchError(f"len() not defined for {args[0]!r}")
        raise MatchError(f"unknown function {e.name!r}")
    raise AssertionError(f"unknown expr node {e!r}")


def run_tree(node, subject, cases):
    if isinstance(node, Leaf):
        env = {name: _get_path(subject, path) for name, path in node.bindings}
        return eval_expr(cases[node.case_idx].body, env)
    if isinstance(node, Guard):
        env = {name: _get_path(subject, path) for name, path in node.bindings}
        if eval_expr(node.guard, env):
            return eval_expr(cases[node.case_idx].body, env)
        return run_tree(node.otherwise, subject, cases)
    if isinstance(node, Switch):
        key = _value_key(_get_path(subject, node.occ))
        for arm_key, sub in node.arms:
            if arm_key == key:
                return run_tree(sub, subject, cases)
        if node.default is not None:
            return run_tree(node.default, subject, cases)
        raise MatchError(f"no case matches value at {node.occ}: key {key!r}")
    if isinstance(node, Fail):
        raise MatchError("non-exhaustive match: no case matched the subject")
    raise AssertionError(f"unknown node {node!r}")


def run(tree, prog):
    subject = eval_expr(prog.subject, {})
    return run_tree(tree, subject, prog.cases)
