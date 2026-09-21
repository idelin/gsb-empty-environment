"""Algorithm W: inference and let-generalization with constraint provenance."""
from __future__ import annotations

from .astnodes import App, BinOp, BoolLit, If, IntLit, Lam, Let, Node, Var
from .errors import UnboundName
from .types import (Origin, Reason, Scheme, TCon, TVar, Type, free_type_vars,
                    prune, t_bool, t_fun, t_int)
from .unify import unify

_INT_OPS = {"+", "-", "*"}
_INT_CMP = {"<"}


def generalize(env: dict, t: Type) -> Scheme:
    """Quantify variables free in `t` but not free in the environment."""
    env_free: dict[int, TVar] = {}
    for scheme in env.values():
        bound = {v.id for v in scheme.vars}
        for vid, var in free_type_vars(scheme.typ).items():
            if vid not in bound:
                env_free[vid] = var
    gen = [v for vid, v in free_type_vars(t).items() if vid not in env_free]
    return Scheme(gen, t)


def instantiate(scheme: Scheme) -> Type:
    """Replace quantified variables with fresh ones (origins are kept)."""
    mapping = {v.id: TVar(origin=v.origin) for v in scheme.vars}

    def go(t: Type) -> Type:
        t = prune(t)
        if isinstance(t, TVar):
            return mapping.get(t.id, t)
        new_args = [go(a) for a in t.args]
        if all(n is a for n, a in zip(new_args, t.args)):
            return t
        return TCon(t.name, new_args, origin=t.origin)

    return go(scheme.typ)


def infer(node: Node, env: dict) -> Type:
    if isinstance(node, IntLit):
        return t_int(Origin(node.span, f"integer literal `{node.value}`"))
    if isinstance(node, BoolLit):
        word = "true" if node.value else "false"
        return t_bool(Origin(node.span, f"boolean literal `{word}`"))
    if isinstance(node, Var):
        scheme = env.get(node.name)
        if scheme is None:
            raise UnboundName(node.name, node.span)
        return instantiate(scheme)
    if isinstance(node, Lam):
        param_t = TVar(Origin(node.param_span, f"type of parameter `{node.param}`"))
        body_t = infer(node.body, {**env, node.param: Scheme([], param_t)})
        return t_fun(param_t, body_t,
                     Origin(node.span, f"function `fun {node.param} -> ...`"))
    if isinstance(node, App):
        fn_t = infer(node.fn, env)
        arg_t = infer(node.arg, env)
        result_t = TVar(Origin(node.span, "result of the application"))
        expected = t_fun(arg_t, result_t,
                         Origin(node.fn.span, "expression applied as a function"))
        unify(fn_t, expected, Reason(node.span, "function application"))
        return result_t
    if isinstance(node, Let):
        value_t = infer(node.value, env)
        scheme = generalize(env, value_t)
        return infer(node.body, {**env, node.name: scheme})
    if isinstance(node, If):
        cond_t = infer(node.cond, env)
        unify(cond_t,
              t_bool(Origin(node.cond.span, "`if` condition must be `Bool`")),
              Reason(node.cond.span, "condition of `if`"))
        then_t = infer(node.then, env)
        else_t = infer(node.otherwise, env)
        unify(then_t, else_t,
              Reason(node.span, "both branches of `if` must have the same type"))
        return then_t
    if isinstance(node, BinOp):
        return _infer_binop(node, env)
    raise AssertionError(f"unknown node {node!r}")


def _infer_binop(node: BinOp, env: dict) -> Type:
    left_t = infer(node.left, env)
    right_t = infer(node.right, env)
    op = node.op
    if op in _INT_OPS or op in _INT_CMP:
        need = Origin(node.op_span, f"`{op}` requires `Int` operands")
        unify(left_t, t_int(need),
              Reason(node.left.span, f"left operand of `{op}`"))
        unify(right_t, t_int(need),
              Reason(node.right.span, f"right operand of `{op}`"))
        if op in _INT_OPS:
            return t_int(Origin(node.op_span, f"result of `{op}`"))
        return t_bool(Origin(node.op_span, f"result of `{op}`"))
    if op == "==":
        unify(left_t, right_t,
              Reason(node.op_span, "both sides of `==` must have the same type"))
        return t_bool(Origin(node.op_span, "result of `==`"))
    raise AssertionError(f"unknown operator {op}")


def infer_program(node: Node) -> Type:
    return infer(node, {})
