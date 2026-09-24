"""跨查询公共子表达式消除(CSE)的聚合执行器。

- 表按 (schema, rows) 批次输入, schema 为 [(列名, 类型), ...]
- null 用 None 表示, NaN 用 float('nan') 表示, 二者语义不同
- 整数精确计算(Python int 任意精度); 浮点 sum/avg 用 math.fsum 保精度
- 相同中间项(结构 + 类型相同)跨查询去重, 只计算一次
- volatile(有副作用)函数永不复用, 每次出现都独立计算
- 输出执行依赖图与每个节点的实际计算次数
"""
from __future__ import annotations

import math
from fractions import Fraction

INT, FLOAT, STR, BOOL = "int", "float", "str", "bool"
TYPES = {INT, FLOAT, STR, BOOL}


# ---------------------------------------------------------------- 表达式 AST

class Expr:
    volatile = False

    def key(self):
        raise NotImplementedError


def _lit_token(value):
    if isinstance(value, float) and math.isnan(value):
        return ("nan",)  # NaN != NaN, 但作为字面量 key 必须稳定
    return ("val", value)


class Col(Expr):
    def __init__(self, name, typ):
        assert typ in TYPES, typ
        self.name, self.typ = name, typ

    def key(self):
        return ("col", self.name, self.typ)

    def __repr__(self):
        return f"col({self.name}:{self.typ})"


class Lit(Expr):
    def __init__(self, value, typ):
        assert typ in TYPES, typ
        self.value, self.typ = value, typ

    def key(self):
        return ("lit", self.typ, _lit_token(self.value))

    def __repr__(self):
        return f"lit({self.value!r}:{self.typ})"


class Cast(Expr):
    def __init__(self, arg, to):
        assert to in TYPES, to
        self.arg, self.to = arg, to

    def key(self):
        return ("cast", self.to, self.arg.key())

    def __repr__(self):
        return f"cast({self.arg!r}->{self.to})"


class Func(Expr):
    """标量行函数。volatile=True 表示有副作用, 禁止去重。"""

    def __init__(self, name, impl, args, out_type, volatile=False,
                 null_propagating=True):
        assert out_type in TYPES, out_type
        self.name, self.impl = name, impl
        self.args = list(args)
        self.out_type = out_type
        self.volatile = volatile
        self.null_propagating = null_propagating

    def key(self):
        if self.volatile:
            return None  # 副作用函数永不进入 memo
        return ("func", self.name, self.out_type,
                tuple(a.key() for a in self.args))

    def __repr__(self):
        inner = ", ".join(map(repr, self.args))
        tag = "!" if self.volatile else ""
        return f"{self.name}{tag}({inner})"


class Agg(Expr):
    NAMES = {"sum", "count", "avg", "min", "max"}

    def __init__(self, name, arg=None, out_type=None, filt=None):
        assert name in self.NAMES, name
        self.name, self.arg = name, arg
        self.out_type, self.filt = out_type, filt

    def key(self):
        return ("agg", self.name, self.out_type,
                self.arg.key() if self.arg is not None else None,
                self.filt.key() if self.filt is not None else None)

    def __repr__(self):
        inner = repr(self.arg) if self.arg is not None else "*"
        f = f" filter({self.filt!r})" if self.filt is not None else ""
        return f"{self.name}({inner}){f}:{self.out_type}"


def _children(expr):
    if isinstance(expr, (Col, Lit)):
        return []
    if isinstance(expr, Cast):
        return [expr.arg]
    if isinstance(expr, Func):
        return expr.args
    if isinstance(expr, Agg):
        out = []
        if expr.arg is not None:
            out.append(expr.arg)
        if expr.filt is not None:
            out.append(expr.filt)
        return out
    raise TypeError(expr)


# ---------------------------------------------------------------- 计划 / DAG

class Node:
    __slots__ = ("id", "expr", "deps", "label")


def _label(expr, deps):
    if isinstance(expr, (Col, Lit)):
        return repr(expr)
    if isinstance(expr, Cast):
        return f"cast(n{deps[0]}->{expr.to})"
    if isinstance(expr, Func):
        inner = ",".join(f"n{d}" for d in deps)
        tag = " [volatile]" if expr.volatile else ""
        return f"{expr.name}({inner}):{expr.out_type}{tag}"
    if isinstance(expr, Agg):
        inner = f"n{deps[0]}" if expr.arg is not None else "*"
        s = f"{expr.name}({inner})"
        if expr.filt is not None:
            s += f" FILTER n{deps[-1]}"
        return s + f":{expr.out_type}"
    raise TypeError(expr)


class Plan:
    """把多个查询的表达式折叠成一个 DAG: 结构+类型相同的子表达式共享节点。"""

    def __init__(self):
        self.nodes = []
        self.memo = {}

    def intern(self, expr):
        k = expr.key()  # volatile 函数返回 None, 永不复用
        if k is not None and k in self.memo:
            return self.memo[k]
        deps = [self.intern(c) for c in _children(expr)]
        node = Node()
        node.id, node.expr, node.deps = len(self.nodes), expr, deps
        node.label = _label(expr, deps)
        self.nodes.append(node)
        if k is not None:
            self.memo[k] = node.id
        return node.id


# ---------------------------------------------------------------- 执行

def _cast(value, to):
    if value is None:
        return None
    if to == FLOAT:
        return float(value)
    if to == INT:
        return int(value)
    if to == STR:
        return str(value)
    if to == BOOL:
        return bool(value)
    raise ValueError(to)


def _agg_state(expr):
    if expr.name == "sum":
        return {"int": 0, "floats": [], "seen": 0}
    if expr.name == "count":
        return {"n": 0}
    if expr.name == "avg":
        return {"int": 0, "floats": [], "n": 0}
    return {"best": None, "seen": False}  # min / max


def _agg_update(state, expr, avec, fvec, nrows):
    for i in range(nrows):
        if fvec is not None and not fvec[i]:
            continue  # filter 为 False 或 null 都排除
        v = avec[i] if avec is not None else None
        name = expr.name
        if name == "count":
            if avec is None or v is not None:  # count(*) / count(x): null 不计, NaN 计
                state["n"] += 1
        elif name == "sum":
            if v is None:
                continue
            state["seen"] += 1
            if expr.out_type == FLOAT:
                state["floats"].append(float(v))
            else:
                state["int"] += v  # 整数精确累加
        elif name == "avg":
            if v is None:
                continue
            state["n"] += 1
            if expr.out_type == FLOAT:
                state["floats"].append(float(v))
            else:
                state["int"] += v
        else:  # min / max
            if v is None:
                continue
            if not state["seen"]:
                state["best"], state["seen"] = v, True
            elif name == "min" and v < state["best"]:
                state["best"] = v
            elif name == "max" and v > state["best"]:
                state["best"] = v


def _agg_final(state, expr):
    name = expr.name
    if name == "count":
        return state["n"]
    if name == "sum":
        if not state["seen"]:
            return None
        if expr.out_type == FLOAT:
            return math.fsum(state["floats"])  # NaN 会传播为 NaN
        return state["int"]
    if name == "avg":
        if not state["n"]:
            return None
        if expr.out_type == FLOAT:
            return math.fsum(state["floats"]) / state["n"]
        return Fraction(state["int"], state["n"])  # 整数 avg 精确为分数
    return state["best"] if state["seen"] else None


class Engine:
    """queries: List[List[Expr]] —— 每个查询是一组 select 表达式。"""

    def __init__(self, queries):
        self.plan = Plan()
        self.roots = [[self.plan.intern(e) for e in q] for q in queries]

    def graph(self):
        refs = {n.id: 0 for n in self.plan.nodes}
        for n in self.plan.nodes:
            for d in n.deps:
                refs[d] += 1
        for q in self.roots:
            for rid in q:
                refs[rid] += 1
        lines = ["execution dependency graph (DAG):"]
        for n in self.plan.nodes:
            deps = ",".join(f"n{d}" for d in n.deps) or "-"
            lines.append(f"  n{n.id}: {n.label:<40} deps=[{deps}] refs={refs[n.id]}")
        return "\n".join(lines)

    def run(self, batches):
        """batches: iterable of (schema, rows)。返回 (results, counts)。

        results[q][k] 为第 q 个查询第 k 个 select 项的结果:
        聚合 -> 标量; 标量表达式 -> 每批次一个值向量的列表。
        counts[node_id] = 该节点被实际计算的次数(每批次一次)。
        """
        counts = {n.id: 0 for n in self.plan.nodes}
        states = {n.id: _agg_state(n.expr) for n in self.plan.nodes
                  if isinstance(n.expr, Agg)}
        scalar_vals = {}
        for schema, rows in batches:
            idx = {}
            for i, (name, typ) in enumerate(schema):
                if name in idx:
                    raise ValueError(f"duplicate column {name!r}")
                idx[name] = i
            schema_types = dict(schema)
            nrows = len(rows)
            values = {}
            for node in self.plan.nodes:  # 节点 id 即拓扑序(子节点先建)
                e = node.expr
                if isinstance(e, Col):
                    if e.name not in idx:
                        raise KeyError(f"missing column {e.name!r}")
                    if schema_types[e.name] != e.typ:
                        raise TypeError(
                            f"column {e.name!r}: batch type "
                            f"{schema_types[e.name]!r} != expected {e.typ!r}")
                    values[node.id] = [r[idx[e.name]] for r in rows]
                elif isinstance(e, Lit):
                    values[node.id] = [e.value] * nrows
                elif isinstance(e, Cast):
                    src = values[node.deps[0]]
                    values[node.id] = [_cast(v, e.to) for v in src]
                elif isinstance(e, Func):
                    cols = [values[d] for d in node.deps]
                    if not cols:  # 零参函数(如 rand())
                        values[node.id] = [e.impl() for _ in range(nrows)]
                    else:
                        out = []
                        for tup in zip(*cols):
                            if e.null_propagating and any(v is None for v in tup):
                                out.append(None)
                            else:
                                out.append(e.impl(*tup))
                        values[node.id] = out
                elif isinstance(e, Agg):
                    avec = values[node.deps[0]] if e.arg is not None else None
                    fvec = values[node.deps[-1]] if e.filt is not None else None
                    _agg_update(states[node.id], e, avec, fvec, nrows)
                counts[node.id] += 1
            for q in self.roots:
                for rid in q:
                    if not isinstance(self.plan.nodes[rid].expr, Agg):
                        scalar_vals.setdefault(rid, []).append(values[rid])
        results = []
        for q in self.roots:
            row = []
            for rid in q:
                e = self.plan.nodes[rid].expr
                if isinstance(e, Agg):
                    row.append(_agg_final(states[rid], e))
                else:
                    row.append(scalar_vals.get(rid, []))
            results.append(row)
        return results, counts
