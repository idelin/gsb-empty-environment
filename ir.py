"""带类型的表达式 IR 与哈希 consing（结构共享）。

- 纯表达式按结构 intern：相同子表达式在多个查询间只存在一个节点，
  执行时自然只计算一次（跨查询中间项去重的核心机制）。
- 副作用函数（pure=False）不参与 intern，每次调用都是独立节点，禁止复用。
- 类型在构建期推导并固化在节点上：sum(x) 与 sum(cast(x)) 因子节点
  类型/结构不同而成为不同节点，不会被错误合并。
"""
from __future__ import annotations

import itertools
import math

INT = "int"
FLOAT = "float"
BOOL = "bool"
STR = "str"
NUMERIC = "numeric"  # avg(int) 的精确有理数结果（Fraction）

_NAN_KEY = ("<nan>",)


def _canon(value):
    """把 NaN 归一化为可哈希、可比较的 key（NaN != NaN 会破坏 intern）。"""
    if isinstance(value, float) and math.isnan(value):
        return _NAN_KEY
    if isinstance(value, (tuple, list)):
        return tuple(_canon(v) for v in value)
    return value


class Node:
    __slots__ = ("kind", "params", "children", "type", "pure", "uid")

    _counter = itertools.count(1)

    def __init__(self, kind, params, children, type_, pure):
        self.kind = kind
        self.params = params
        self.children = tuple(children)
        self.type = type_
        self.pure = pure
        self.uid = next(Node._counter)

    def __repr__(self):
        return (
            f"Node({self.kind!r}, {self.params!r}, "
            f"type={self.type!r}, uid={self.uid})"
        )


class Builder:
    """表达式构造器：纯节点 intern，副作用节点每次新建。"""

    _ARITH = {"add", "sub", "mul", "div", "mod"}
    _CMP = {"lt", "le", "gt", "ge", "eq", "ne"}
    _LOGIC = {"and", "or"}
    _AGG = {"sum", "avg", "min", "max", "count"}

    def __init__(self):
        self._intern = {}
        self.nodes = []

    def _mk(self, kind, params, children, type_, pure=True):
        children = tuple(children)
        key = None
        if pure:
            key = (kind, _canon(params), tuple(c.uid for c in children), type_)
            hit = self._intern.get(key)
            if hit is not None:
                return hit
        node = Node(kind, params, children, type_, pure)
        if pure:
            self._intern[key] = node
        self.nodes.append(node)
        return node

    def col(self, name, type_):
        return self._mk("col", (name,), (), type_)

    def lit(self, value, type_=None):
        if type_ is None:
            if value is None:
                raise ValueError("null 字面量必须显式给出类型")
            if isinstance(value, bool):
                type_ = BOOL
            elif isinstance(value, int):
                type_ = INT
            elif isinstance(value, float):
                type_ = FLOAT
            elif isinstance(value, str):
                type_ = STR
            else:
                raise TypeError(f"不支持的字面量类型: {type(value)!r}")
        return self._mk("lit", (value,), (), type_)

    def cast(self, expr, to):
        if to not in (INT, FLOAT, STR, BOOL):
            raise TypeError(f"不支持的目标类型: {to!r}")
        return self._mk("cast", (to,), (expr,), to)

    def binop(self, op, left, right):
        if op in self._ARITH:
            for e in (left, right):
                if e.type not in (INT, FLOAT):
                    raise TypeError(f"算术运算需要数值类型, 得到 {e.type!r}")
            if op == "div":
                type_ = FLOAT
            elif left.type == INT and right.type == INT:
                type_ = INT
            else:
                type_ = FLOAT
        elif op in self._CMP:
            type_ = BOOL
        elif op in self._LOGIC:
            if left.type != BOOL or right.type != BOOL:
                raise TypeError("and/or 需要 bool 操作数")
            type_ = BOOL
        else:
            raise ValueError(f"未知运算符: {op!r}")
        return self._mk("binop", (op,), (left, right), type_)

    def not_(self, expr):
        if expr.type != BOOL:
            raise TypeError("not 需要 bool 操作数")
        return self._mk("not", (), (expr,), BOOL)

    def is_null(self, expr):
        return self._mk("is_null", (), (expr,), BOOL)

    def is_nan(self, expr):
        return self._mk("is_nan", (), (expr,), BOOL)

    def func(self, name, args, ret_type, pure=True):
        return self._mk("func", (name,), tuple(args), ret_type, pure=pure)

    def agg(self, kind, arg=None, filter=None, distinct=False):
        if kind not in self._AGG:
            raise ValueError(f"未知聚合: {kind!r}")
        has_arg = arg is not None
        children = []
        if has_arg:
            if kind in ("sum", "avg") and arg.type not in (INT, FLOAT):
                raise TypeError(f"{kind} 需要数值参数, 得到 {arg.type!r}")
            children.append(arg)
        elif kind != "count":
            raise TypeError(f"{kind} 需要参数表达式")
        if filter is not None:
            if filter.type != BOOL:
                raise TypeError("filter 必须是 bool 表达式")
            children.append(filter)
        if kind == "count":
            type_ = INT
        elif kind == "sum":
            type_ = arg.type
        elif kind == "avg":
            type_ = FLOAT if arg.type == FLOAT else NUMERIC
        else:
            type_ = arg.type
        return self._mk("agg", (kind, bool(distinct), has_arg), children, type_)
