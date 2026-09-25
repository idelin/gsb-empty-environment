"""Tree-walking interpreter.

Semantics contract:
  * Binary operands are evaluated strictly left-to-right; the first error
    raised wins, and its byte span is the operator's op_span.
  * 'and' / 'or' short-circuit: the right operand is only evaluated when
    the left operand does not decide the result.
  * Errors: division/modulo by zero, type errors, undefined variables.
    Every error carries the UTF-8 byte span of the offending operator/name.
"""
from __future__ import annotations

from nodes import (Binary, Block, BoolLit, ExprStmt, If, IntLit, Let, Print,
                   Unary, Var)


class LangError(Exception):
    def __init__(self, message, span):
        super().__init__(message)
        self.message = message
        self.span = span


def is_int(v):
    return isinstance(v, int) and not isinstance(v, bool)


def fmt(v):
    if v is True:
        return "true"
    if v is False:
        return "false"
    return str(v)


class Env:
    def __init__(self):
        self.scopes = [{}]

    def push(self):
        self.scopes.append({})

    def pop(self):
        self.scopes.pop()

    def get(self, name, span):
        for scope in reversed(self.scopes):
            if name in scope:
                return scope[name]
        raise LangError("undefined variable '%s'" % name, span)

    def set(self, name, value):
        self.scopes[-1][name] = value


def const_binop(op, lv, rv, span):
    """Pure operator semantics shared by the interpreter and the folder.
    Raises LangError on division by zero / type errors."""
    if op in ("+", "-", "*", "/", "%"):
        if not (is_int(lv) and is_int(rv)):
            raise LangError("type error: '%s' expects int operands" % op, span)
        if op == "+":
            return lv + rv
        if op == "-":
            return lv - rv
        if op == "*":
            return lv * rv
        if rv == 0:
            raise LangError("division by zero", span)
        if op == "/":
            return lv // rv  # 整除，向下取整
        return lv % rv
    if op in ("<", "<=", ">", ">="):
        if not (is_int(lv) and is_int(rv)):
            raise LangError("type error: '%s' expects int operands" % op, span)
        if op == "<":
            return lv < rv
        if op == "<=":
            return lv <= rv
        if op == ">":
            return lv > rv
        return lv >= rv
    if op in ("==", "!="):
        if isinstance(lv, bool) != isinstance(rv, bool):
            raise LangError("type error: cannot compare int with bool", span)
        return (lv == rv) if op == "==" else (lv != rv)
    raise LangError("unknown operator '%s'" % op, span)


def eval_expr(node, env):
    if isinstance(node, IntLit):
        return node.value
    if isinstance(node, BoolLit):
        return node.value
    if isinstance(node, Var):
        return env.get(node.name, node.span)
    if isinstance(node, Unary):
        v = eval_expr(node.operand, env)
        if node.op == "neg":
            if not is_int(v):
                raise LangError("type error: unary '-' expects int",
                                node.op_span)
            return -v
        # 'not'
        if not isinstance(v, bool):
            raise LangError("type error: 'not' expects bool", node.op_span)
        return not v
    if isinstance(node, Binary):
        if node.op == "and":
            lv = eval_expr(node.left, env)
            if not isinstance(lv, bool):
                raise LangError("type error: 'and' expects bool operands",
                                node.op_span)
            if not lv:
                return False  # 短路：右操作数不求值
            rv = eval_expr(node.right, env)
            if not isinstance(rv, bool):
                raise LangError("type error: 'and' expects bool operands",
                                node.op_span)
            return rv
        if node.op == "or":
            lv = eval_expr(node.left, env)
            if not isinstance(lv, bool):
                raise LangError("type error: 'or' expects bool operands",
                                node.op_span)
            if lv:
                return True  # 短路：右操作数不求值
            rv = eval_expr(node.right, env)
            if not isinstance(rv, bool):
                raise LangError("type error: 'or' expects bool operands",
                                node.op_span)
            return rv
        lv = eval_expr(node.left, env)   # 严格先左后右，保证错误发生顺序
        rv = eval_expr(node.right, env)
        return const_binop(node.op, lv, rv, node.op_span)
    raise AssertionError("unknown node %r" % (node,))


def exec_stmt(node, env, out):
    if isinstance(node, Let):
        env.set(node.name, eval_expr(node.value, env))
        return
    if isinstance(node, Print):
        out.append(fmt(eval_expr(node.value, env)))
        return
    if isinstance(node, ExprStmt):
        eval_expr(node.value, env)
        return
    if isinstance(node, If):
        cond = eval_expr(node.cond, env)
        if not isinstance(cond, bool):
            raise LangError("type error: 'if' condition must be bool",
                            node.cond.span)
        env.push()
        try:
            for stmt in (node.then_body if cond else node.else_body):
                exec_stmt(stmt, env, out)
        finally:
            env.pop()
        return
    if isinstance(node, Block):
        env.push()
        try:
            for stmt in node.body:
                exec_stmt(stmt, env, out)
        finally:
            env.pop()
        return
    raise AssertionError("unknown statement %r" % (node,))


def run(program):
    """Returns (outputs, error). error is None or a LangError."""
    out = []
    env = Env()
    try:
        for stmt in program.body:
            exec_stmt(stmt, env, out)
    except LangError as err:
        return out, err
    return out, None
