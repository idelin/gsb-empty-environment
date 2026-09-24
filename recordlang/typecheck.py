"""Static type checker: record width subtyping with explicit ro/rw variance.

Variance discipline
-------------------
* ``@ro`` (read-only) positions are covariant: a record with more fields may
  be used where fewer fields are required (width subtyping), and field types
  may themselves be subtypes (depth subtyping, read-only all the way down).
* ``@rw`` (writable) positions are invariant: the argument type must be
  exactly the parameter type.  Allowing width subtyping here would be
  unsound, because the callee may write through the parameter.
* Function types are contravariant in their parameters (each compared at its
  own declared mode) and covariant in their results.
"""

from __future__ import annotations

from .ast_nodes import (
    App, BinOp, BoolLit, Extend, FieldAccess, Fn, If, IntLit, Let, RecordLit,
    TBool, TFn, TInt, TRecord, UnaryOp, Update, Var, record_type, show_type,
)
from .errors import LangError


def is_subtype(t, u, pos: str = "ro") -> bool:
    """t <: u at a position with variance `pos` ("ro" covariant / "rw" invariant)."""
    if t == u:
        return True
    if isinstance(t, TRecord) and isinstance(u, TRecord):
        if pos == "rw":
            return False  # writable positions are invariant: exact match only
        have = dict(t.fields)
        return all(
            name in have and is_subtype(have[name], ft, "ro")
            for name, ft in u.fields
        )
    if isinstance(t, TFn) and isinstance(u, TFn):
        if len(t.params) != len(u.params):
            return False
        for (tp, tm), (up, um) in zip(t.params, u.params):
            if tm != um or not is_subtype(up, tp, tm):  # contravariant parameters
                return False
        return is_subtype(t.ret, u.ret, "ro")  # covariant result
    return False


def type_of(node, env) -> object:
    if isinstance(node, IntLit):
        return TInt()
    if isinstance(node, BoolLit):
        return TBool()
    if isinstance(node, Var):
        try:
            return env[node.name]
        except KeyError:
            raise LangError(f"unbound variable '{node.name}'", node.span) from None
    if isinstance(node, RecordLit):
        return record_type(
            {name: type_of(value, env) for name, value, _ in node.fields}
        )
    if isinstance(node, FieldAccess):
        rt = type_of(node.record, env)
        if not isinstance(rt, TRecord):
            raise LangError(
                f"field access '.{node.name}' on non-record of type {show_type(rt)}",
                node.name_span,
            )
        have = dict(rt.fields)
        if node.name not in have:
            raise LangError(
                f"record of type {show_type(rt)} has no field '{node.name}': "
                "access to potentially missing fields is rejected at compile time",
                node.name_span,
            )
        return have[node.name]
    if isinstance(node, Update):
        rt = type_of(node.record, env)
        if not isinstance(rt, TRecord):
            raise LangError(
                f"cannot update a field of non-record of type {show_type(rt)}",
                node.name_span,
            )
        have = dict(rt.fields)
        if node.name not in have:
            raise LangError(
                f"cannot update field '{node.name}': type {show_type(rt)} has no "
                f"such field; use 'extend ... with {node.name} = ...' to add a "
                "new field explicitly",
                node.name_span,
            )
        vt = type_of(node.value, env)
        if not is_subtype(vt, have[node.name], "ro"):
            raise LangError(
                f"update of field '{node.name}' expects "
                f"{show_type(have[node.name])}, got {show_type(vt)}",
                node.value.span,
            )
        return rt
    if isinstance(node, Extend):
        rt = type_of(node.record, env)
        if not isinstance(rt, TRecord):
            raise LangError(
                f"cannot extend non-record of type {show_type(rt)}", node.name_span
            )
        have = dict(rt.fields)
        if node.name in have:
            raise LangError(
                f"field '{node.name}' already exists in {show_type(rt)}; "
                f"use '... with {node.name} = ...' to update it",
                node.name_span,
            )
        have[node.name] = type_of(node.value, env)
        return record_type(have)
    if isinstance(node, Let):
        vt = type_of(node.value, env)
        return type_of(node.body, {**env, node.name: vt})
    if isinstance(node, If):
        ct = type_of(node.cond, env)
        if ct != TBool():
            raise LangError(
                f"if condition must be Bool, got {show_type(ct)}", node.cond.span
            )
        tt = type_of(node.then, env)
        ot = type_of(node.other, env)
        if is_subtype(tt, ot, "ro"):
            return ot
        if is_subtype(ot, tt, "ro"):
            return tt
        raise LangError(
            f"if branches have incompatible types {show_type(tt)} and "
            f"{show_type(ot)}",
            node.span,
        )
    if isinstance(node, UnaryOp):
        ot = type_of(node.operand, env)
        if ot != TInt():
            raise LangError(
                f"unary '-' expects Int, got {show_type(ot)}", node.span
            )
        return TInt()
    if isinstance(node, BinOp):
        lt = type_of(node.left, env)
        rt = type_of(node.right, env)
        if node.op in ("+", "-", "*", "/", "<", "<=", ">", ">="):
            if lt != TInt() or rt != TInt():
                raise LangError(
                    f"operator '{node.op}' expects Int operands, got "
                    f"{show_type(lt)} and {show_type(rt)}",
                    node.span,
                )
            return TInt() if node.op in ("+", "-", "*", "/") else TBool()
        if node.op in ("==", "!="):
            if lt != rt:
                raise LangError(
                    f"operator '{node.op}' expects equal types, got "
                    f"{show_type(lt)} and {show_type(rt)}",
                    node.span,
                )
            return TBool()
        raise AssertionError(f"unknown operator {node.op}")
    if isinstance(node, Fn):
        inner = dict(env)
        for param in node.params:
            if param.name in inner:
                raise LangError(f"duplicate parameter '{param.name}'", param.span)
            inner[param.name] = param.type
        body_type = type_of(node.body, inner)
        if not is_subtype(body_type, node.ret, "ro"):
            raise LangError(
                f"function body has type {show_type(body_type)}, which is not a "
                f"subtype of the declared return type {show_type(node.ret)}",
                node.body.span,
            )
        return TFn(tuple((p.type, p.mode) for p in node.params), node.ret)
    if isinstance(node, App):
        ft = type_of(node.func, env)
        if not isinstance(ft, TFn):
            raise LangError(
                f"cannot call non-function of type {show_type(ft)}", node.func.span
            )
        if len(node.args) != len(ft.params):
            raise LangError(
                f"arity mismatch: expected {len(ft.params)} argument(s), "
                f"got {len(node.args)}",
                node.span,
            )
        for arg, (ptype, mode) in zip(node.args, ft.params):
            at = type_of(arg, env)
            if not is_subtype(at, ptype, mode):
                if mode == "rw":
                    detail = (
                        "writable (@rw) parameters are invariant and do not allow "
                        "width subtyping; the argument type must match exactly"
                    )
                else:
                    detail = (
                        "the argument type must be a subtype of the parameter type"
                    )
                raise LangError(
                    f"argument of type {show_type(at)} does not match parameter of "
                    f"type {show_type(ptype)} @{mode}: {detail}",
                    arg.span,
                )
        return ft.ret
    raise AssertionError(f"unknown node {node!r}")
