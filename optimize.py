"""Optimizations with a rationale map, preserving the semantics contract:

  1. constant-fold      纯字面量子表达式在编译期求值。
  2. short-circuit-fold 'false and X' -> false, 'true or X' -> true 等：
                        右操作数本来就不会被求值，删除它不改变错误顺序，
                        即使 X 会抛错（例如 false and (1/0) 可化为 false）。
  3. fold-skip          求值会抛错的字面量表达式（如 1/0）故意不折叠，
                        让错误仍在运行期、按原顺序、原字节区间抛出。
  4. branch-simplify    条件为常量的 if 只保留必走分支；空分支 if 退化为
                        条件表达式语句（条件仍需求值，可能抛错）。
  5. cse                复用先前 let 绑定过的相同表达式。仅当该 let 在本
                        语句之前无条件执行、且其间没有任何相关变量被重新
                        绑定时才复用，因此不会引入/重排错误。

交换律仅用于“纯”操作数（编译期可证不抛错）的规范化；可能抛错的两个
操作数绝不交换，以维持从左到右的错误发生顺序。
"""
from __future__ import annotations

from interp import LangError, const_binop, fmt, is_int
from nodes import (Binary, Block, BoolLit, ExprStmt, If, IntLit, Let, Print,
                   Program, Unary, Var)

COMMUTATIVE = {"+", "*", "==", "!=", "and", "or"}


# ---------------------------------------------------------------- utilities

def unparse(node):
    if isinstance(node, IntLit):
        return str(node.value)
    if isinstance(node, BoolLit):
        return "true" if node.value else "false"
    if isinstance(node, Var):
        return node.name
    if isinstance(node, Unary):
        sym = "-" if node.op == "neg" else "not "
        return "(%s%s)" % (sym, unparse(node.operand))
    if isinstance(node, Binary):
        return "(%s %s %s)" % (unparse(node.left), node.op,
                               unparse(node.right))
    raise AssertionError("cannot unparse %r" % (node,))


def try_const(node):
    """若 node 仅由字面量构成且求值不抛错，返回 (True, value)。
    用于判定“纯”（可证不抛错），任何错误都被吞掉并返回 (False, None)。"""
    if isinstance(node, IntLit):
        return True, node.value
    if isinstance(node, BoolLit):
        return True, node.value
    if isinstance(node, Unary):
        ok, v = try_const(node.operand)
        if not ok:
            return False, None
        if node.op == "neg" and is_int(v):
            return True, -v
        if node.op == "not" and isinstance(v, bool):
            return True, not v
        return False, None
    if isinstance(node, Binary):
        if node.op in ("and", "or"):
            ok, lv = try_const(node.left)
            if not ok or not isinstance(lv, bool):
                return False, None
            if node.op == "and" and not lv:
                return True, False
            if node.op == "or" and lv:
                return True, True
            ok, rv = try_const(node.right)
            if ok and isinstance(rv, bool):
                return True, rv
            return False, None
        ok, lv = try_const(node.left)
        if not ok:
            return False, None
        ok, rv = try_const(node.right)
        if not ok:
            return False, None
        try:
            return True, const_binop(node.op, lv, rv, node.op_span)
        except LangError:
            return False, None
    return False, None


def make_lit(span, value):
    if isinstance(value, bool):
        return BoolLit(span, value)
    return IntLit(span, value)


# ------------------------------------------------------- constant folding

def fold_expr(node, rationale):
    if isinstance(node, Unary):
        node.operand = fold_expr(node.operand, rationale)
        ok, val = try_const(node)
        if ok:
            rationale.append({
                "rule": "constant-fold",
                "span": list(node.span),
                "before": unparse(node),
                "after": fmt(val),
                "reason": "纯字面量表达式，编译期求值结果与运行期相同，"
                          "且不产生错误，折叠不改变可观察行为",
            })
            return make_lit(node.span, val)
        return node
    if isinstance(node, Binary):
        node.left = fold_expr(node.left, rationale)
        node.right = fold_expr(node.right, rationale)
        if node.op == "and" and isinstance(node.left, BoolLit):
            if node.left.value is False:
                rationale.append({
                    "rule": "short-circuit-fold",
                    "span": list(node.span),
                    "before": unparse(node),
                    "after": "false",
                    "reason": "'false and X' 短路：X 本就不会被求值，删除 X "
                              "不改变结果与错误顺序（即使 X 会抛错，如除零）",
                })
                return BoolLit(node.span, False)
            rationale.append({
                "rule": "short-circuit-fold",
                "span": list(node.span),
                "before": unparse(node),
                "after": unparse(node.right),
                "reason": "'true and X' 恒等于 X：左操作数恒真、无副作用，"
                          "求值顺序不变",
            })
            return node.right
        if node.op == "or" and isinstance(node.left, BoolLit):
            if node.left.value is True:
                rationale.append({
                    "rule": "short-circuit-fold",
                    "span": list(node.span),
                    "before": unparse(node),
                    "after": "true",
                    "reason": "'true or X' 短路：X 本就不会被求值，删除 X "
                              "不改变结果与错误顺序（即使 X 会抛错）",
                })
                return BoolLit(node.span, True)
            rationale.append({
                "rule": "short-circuit-fold",
                "span": list(node.span),
                "before": unparse(node),
                "after": unparse(node.right),
                "reason": "'false or X' 恒等于 X：左操作数恒假、无副作用，"
                          "求值顺序不变",
            })
            return node.right
        both_lit = (isinstance(node.left, (IntLit, BoolLit))
                    and isinstance(node.right, (IntLit, BoolLit)))
        if both_lit:
            ok, val = try_const(node)
            if ok:
                rationale.append({
                    "rule": "constant-fold",
                    "span": list(node.span),
                    "before": unparse(node),
                    "after": fmt(val),
                    "reason": "纯字面量表达式，编译期求值结果与运行期相同，"
                              "且不产生错误，折叠不改变可观察行为",
                })
                return make_lit(node.span, val)
            rationale.append({
                "rule": "fold-skip",
                "span": list(node.span),
                "before": unparse(node),
                "after": unparse(node),
                "reason": "该常量表达式求值会抛错（如除零/类型错误）；"
                          "故意不折叠，让错误仍在运行期按原顺序、"
                          "原字节区间抛出",
            })
        return node
    return node


# ------------------------------------------------------------------- CSE

def expr_key(node):
    """结构键。交换律操作符仅在两个操作数都可证不抛错时才规范化顺序，
    否则保持书写顺序——可能抛错的两项不能任意交换。"""
    if isinstance(node, IntLit):
        return ("lit", "int", node.value)
    if isinstance(node, BoolLit):
        return ("lit", "bool", node.value)
    if isinstance(node, Var):
        return ("var", node.name)
    if isinstance(node, Unary):
        return ("un", node.op, expr_key(node.operand))
    if isinstance(node, Binary):
        lk, rk = expr_key(node.left), expr_key(node.right)
        if node.op in COMMUTATIVE:
            lok, _ = try_const(node.left)
            rok, _ = try_const(node.right)
            if lok and rok and lk > rk:
                lk, rk = rk, lk
        return ("bin", node.op, lk, rk)
    raise AssertionError("no key for %r" % (node,))


def key_mentions(key, name):
    if key[0] == "var":
        return key[1] == name
    return any(isinstance(part, tuple) and key_mentions(part, name)
               for part in key[1:])


def invalidate(scopes, name):
    """新的 let 绑定 name 后，所有提到 name 或复用 name 的缓存项失效。"""
    for scope in scopes:
        for key in [k for k, v in scope.items()
                    if v == name or key_mentions(k, name)]:
            del scope[key]


def reuse(node, scopes, rationale):
    if isinstance(node, (IntLit, BoolLit, Var)):
        return node
    key = expr_key(node)
    for scope in reversed(scopes):
        if key in scope:
            name = scope[key]
            rationale.append({
                "rule": "cse",
                "span": list(node.span),
                "before": unparse(node),
                "after": name,
                "reason": "相同表达式已在 let 绑定 '%s' 中求值；该绑定先于本"
                          "语句无条件执行，且其间没有相关变量被重新绑定，"
                          "复用其结果不引入、不消除、不重排任何错误" % name,
            })
            return Var(node.span, name)
    if isinstance(node, Unary):
        node.operand = reuse(node.operand, scopes, rationale)
    elif isinstance(node, Binary):
        node.left = reuse(node.left, scopes, rationale)
        node.right = reuse(node.right, scopes, rationale)
    return node


# ---------------------------------------------------- block / branch pass

def optimize_block(stmts, scopes, rationale):
    out = []
    for stmt in stmts:
        if isinstance(stmt, Let):
            invalidate(scopes, stmt.name)
            stmt.value = fold_expr(stmt.value, rationale)
            stmt.value = reuse(stmt.value, scopes, rationale)
            out.append(stmt)
            if not isinstance(stmt.value, (IntLit, BoolLit)):
                scopes[-1][expr_key(stmt.value)] = stmt.name
        elif isinstance(stmt, (Print, ExprStmt)):
            stmt.value = fold_expr(stmt.value, rationale)
            stmt.value = reuse(stmt.value, scopes, rationale)
            out.append(stmt)
        elif isinstance(stmt, If):
            stmt.cond = fold_expr(stmt.cond, rationale)
            stmt.cond = reuse(stmt.cond, scopes, rationale)
            scopes.append({})
            stmt.then_body = optimize_block(stmt.then_body, scopes, rationale)
            scopes.pop()
            scopes.append({})
            stmt.else_body = optimize_block(stmt.else_body, scopes, rationale)
            scopes.pop()
            if isinstance(stmt.cond, BoolLit):
                taken = stmt.then_body if stmt.cond.value else stmt.else_body
                rationale.append({
                    "rule": "branch-simplify",
                    "span": list(stmt.span),
                    "before": "if %s { ... } else { ... }" % unparse(stmt.cond),
                    "after": "<%s 分支的 %d 条语句>" % (
                        "then" if stmt.cond.value else "else", len(taken)),
                    "reason": "条件为常量 %s，另一分支永不执行；死分支中的"
                              "错误本来就不会发生，删除它不违反错误契约；"
                              "保留块以维持 let 作用域"
                              % fmt(stmt.cond.value),
                })
                out.append(Block(stmt.span, taken))
            elif not stmt.then_body and not stmt.else_body:
                rationale.append({
                    "rule": "branch-simplify",
                    "span": list(stmt.span),
                    "before": "if %s { } else { }" % unparse(stmt.cond),
                    "after": unparse(stmt.cond) + ";",
                    "reason": "两个分支均为空，但条件仍可能被求值并抛错，"
                              "退化为表达式语句以保留求值与错误行为",
                })
                out.append(ExprStmt(stmt.span, stmt.cond))
            else:
                out.append(stmt)
        elif isinstance(stmt, Block):
            scopes.append({})
            stmt.body = optimize_block(stmt.body, scopes, rationale)
            scopes.pop()
            out.append(stmt)
        else:
            raise AssertionError("unknown statement %r" % (stmt,))
    return out


def optimize_program(program):
    """返回 (优化后的 Program, 理由映射列表)。理由映射中 span 为原始源码的
    UTF-8 字节区间 [start, end)。"""
    rationale = []
    body = optimize_block(program.body, [{}], rationale)
    return Program(program.span, body), rationale
