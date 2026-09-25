"""批式执行引擎。

- 输入为带列类型的批次（Batch），列值中 None 表示 null，NaN 是普通浮点值。
- 所有查询的表达式构成一张 DAG，按拓扑序逐批执行：每个节点每批只算一次，
  被多处引用的节点（如 sum(x)）共享同一份结果与同一个聚合状态。
- 聚合按批维护可合并的部分状态（partial state），跨批 merge 后 finalize。
- 记录每个节点的实际计算次数（批次数 evals 与行级运算数 row_ops）。
"""
from __future__ import annotations

import math
from fractions import Fraction

from ir import BOOL, FLOAT, INT, STR, Node

_TYPE_OK = {
    INT: lambda v: isinstance(v, int) and not isinstance(v, bool),
    FLOAT: lambda v: isinstance(v, float),
    STR: lambda v: isinstance(v, str),
    BOOL: lambda v: isinstance(v, bool),
}


class Batch:
    """一个输入批次：{列名: (列类型, 值列表)}，None 表示 null。"""

    def __init__(self, columns):
        self.columns = {}
        self.nrows = None
        for name, (type_, values) in columns.items():
            if type_ not in _TYPE_OK:
                raise TypeError(f"未知列类型: {type_!r}")
            vals = [
                float(v)
                if type_ == FLOAT and isinstance(v, int) and not isinstance(v, bool)
                else v
                for v in values
            ]
            for v in vals:
                if v is None:
                    continue
                if not _TYPE_OK[type_](v):
                    raise TypeError(
                        f"列 {name!r} 声明为 {type_}, 但收到值 {v!r} ({type(v).__name__})"
                    )
            if self.nrows is None:
                self.nrows = len(vals)
            elif self.nrows != len(vals):
                raise ValueError("同一批次内各列行数必须一致")
            self.columns[name] = (type_, vals)
        if self.nrows is None:
            self.nrows = 0


def topo_order(roots):
    order, seen = [], set()

    def visit(node):
        if node.uid in seen:
            return
        seen.add(node.uid)
        for child in node.children:
            visit(child)
        order.append(node)

    for root in roots:
        visit(root)
    return order


def _cast_value(v, to):
    if v is None:
        return None
    if to == FLOAT:
        if isinstance(v, bool):
            raise TypeError("bool 不能 cast 为 float")
        return float(v)
    if to == INT:
        if isinstance(v, bool):
            raise TypeError("bool 不能 cast 为 int")
        if isinstance(v, float):
            if math.isnan(v):
                raise ValueError("NaN 不能 cast 为 int")
            return int(v)
        return int(v)
    if to == STR:
        return str(v)
    if to == BOOL:
        return bool(v)
    raise TypeError(f"不支持的 cast 目标: {to!r}")


def _binop(op, a, b):
    if op == "and":
        if a is False or b is False:
            return False
        if a is None or b is None:
            return None
        return True
    if op == "or":
        if a is True or b is True:
            return True
        if a is None or b is None:
            return None
        return False
    if a is None or b is None:
        return None
    if op == "add":
        return a + b
    if op == "sub":
        return a - b
    if op == "mul":
        return a * b
    if op == "div":
        return a / b
    if op == "mod":
        return a % b
    if op == "lt":
        return a < b
    if op == "le":
        return a <= b
    if op == "gt":
        return a > b
    if op == "ge":
        return a >= b
    if op == "eq":
        return a == b
    if op == "ne":
        return a != b
    raise ValueError(f"未知运算符: {op!r}")


def _compute_scalar(node, arg_cols, nrows, funcs):
    kind = node.kind
    if kind == "cast":
        to = node.params[0]
        return [_cast_value(v, to) for v in arg_cols[0]]
    if kind == "binop":
        op = node.params[0]
        return [_binop(op, a, b) for a, b in zip(arg_cols[0], arg_cols[1])]
    if kind == "not":
        return [None if v is None else not v for v in arg_cols[0]]
    if kind == "is_null":
        return [v is None for v in arg_cols[0]]
    if kind == "is_nan":
        return [isinstance(v, float) and math.isnan(v) for v in arg_cols[0]]
    if kind == "func":
        fn = funcs[node.params[0]]
        if not arg_cols:
            return [fn() for _ in range(nrows)]
        return [fn(*xs) for xs in zip(*arg_cols)]
    raise ValueError(f"未知标量节点: {kind!r}")


class Counts:
    """每个节点的实际计算次数：evals=执行的批次数, row_ops=行级运算总数。"""

    def __init__(self):
        self.evals = {}
        self.row_ops = {}

    def note(self, node, nrows):
        self.evals[node.uid] = self.evals.get(node.uid, 0) + 1
        self.row_ops[node.uid] = self.row_ops.get(node.uid, 0) + nrows


class SumState:
    def __init__(self, type_):
        self.total = 0 if type_ == INT else 0.0
        self.n = 0

    def add(self, v):
        self.total += v
        self.n += 1

    def merge(self, other):
        self.total += other.total
        self.n += other.n

    def final(self):
        return None if self.n == 0 else self.total


class AvgState:
    def __init__(self, arg_type):
        self.exact = arg_type != FLOAT
        self.total = Fraction(0) if self.exact else 0.0
        self.n = 0

    def add(self, v):
        self.total += v
        self.n += 1

    def merge(self, other):
        self.total += other.total
        self.n += other.n

    def final(self):
        if self.n == 0:
            return None
        return self.total / self.n


class MinMaxState:
    def __init__(self, is_min):
        self.is_min = is_min
        self.best = None
        self.has = False

    def add(self, v):
        if not self.has:
            self.best = v
            self.has = True
        elif self.is_min and v < self.best:
            self.best = v
        elif not self.is_min and v > self.best:
            self.best = v

    def merge(self, other):
        if other.has:
            self.add(other.best)

    def final(self):
        return self.best if self.has else None


class CountState:
    def __init__(self, star):
        self.star = star
        self.n = 0

    def add(self, v):
        self.n += 1

    def merge(self, other):
        self.n += other.n

    def final(self):
        return self.n


class DistinctState:
    """distinct 包装：NaN 视为同一个键，null 在进入前已被过滤。"""

    def __init__(self, inner):
        self.inner = inner
        self.seen = set()
        self.accepted = []

    @staticmethod
    def _key(v):
        if isinstance(v, float) and math.isnan(v):
            return ("nan",)
        return ("v", v)

    def add(self, v):
        k = self._key(v)
        if k in self.seen:
            return
        self.seen.add(k)
        self.accepted.append(v)
        self.inner.add(v)

    def merge(self, other):
        for v in other.accepted:
            self.add(v)

    def final(self):
        return self.inner.final()


def _make_state(node):
    kind, distinct, has_arg = node.params
    if kind == "sum":
        state = SumState(node.type)
    elif kind == "avg":
        state = AvgState(node.children[0].type)
    elif kind == "min":
        state = MinMaxState(True)
    elif kind == "max":
        state = MinMaxState(False)
    elif kind == "count":
        state = CountState(star=not has_arg)
    else:
        raise ValueError(f"未知聚合: {kind!r}")
    return DistinctState(state) if distinct else state


class Engine:
    def __init__(self, funcs=None):
        self.funcs = dict(funcs or {})

    def run(self, queries, batches):
        """queries: [{别名: 聚合节点}, ...]; 返回 (results, counts, topo_order)。"""
        roots = []
        for query in queries:
            for alias, node in query.items():
                if node.kind != "agg":
                    raise ValueError(f"查询输出 {alias!r} 必须是聚合表达式")
                roots.append(node)
        order = topo_order(roots)
        counts = Counts()
        states = {n.uid: _make_state(n) for n in order if n.kind == "agg"}
        for batch in batches:
            values = self._eval_scalars(order, batch, counts)
            for node in order:
                if node.kind != "agg":
                    continue
                self._update_state(node, states[node.uid], values, batch.nrows)
                counts.note(node, batch.nrows)
        results = [
            {alias: states[node.uid].final() for alias, node in query.items()}
            for query in queries
        ]
        return results, counts, order

    def _eval_scalars(self, order, batch, counts):
        values = {}
        for node in order:
            kind = node.kind
            if kind == "agg":
                continue
            if kind == "col":
                name = node.params[0]
                if name not in batch.columns:
                    raise KeyError(f"批次中缺少列 {name!r}")
                type_, vals = batch.columns[name]
                if type_ != node.type:
                    raise TypeError(
                        f"列 {name!r} 类型不一致: 表达式 {node.type}, 批次 {type_}"
                    )
                values[node.uid] = vals
                counts.note(node, 0)
            elif kind == "lit":
                values[node.uid] = [node.params[0]] * batch.nrows
                counts.note(node, 0)
            else:
                arg_cols = [values[c.uid] for c in node.children]
                values[node.uid] = _compute_scalar(
                    node, arg_cols, batch.nrows, self.funcs
                )
                counts.note(node, batch.nrows)
        return values

    @staticmethod
    def _update_state(node, state, values, nrows):
        _, _, has_arg = node.params
        pos = 0
        arg_vals = None
        if has_arg:
            arg_vals = values[node.children[0].uid]
            pos = 1
        filt = None
        if len(node.children) > pos:
            filt = values[node.children[pos].uid]
        for i in range(nrows):
            if filt is not None and filt[i] is not True:
                continue
            if arg_vals is None:
                state.add(None)
            else:
                v = arg_vals[i]
                if v is None:
                    continue
                state.add(v)


def _label(node):
    kind = node.kind
    if kind == "col":
        return f"col({node.params[0]})"
    if kind == "lit":
        return f"lit({node.params[0]!r})"
    if kind == "cast":
        return f"cast->{node.params[0]}"
    if kind == "binop":
        return f"binop({node.params[0]})"
    if kind == "func":
        purity = "pure" if node.pure else "side-effect"
        return f"func({node.params[0]}, {purity})"
    if kind == "agg":
        agg_kind, distinct, has_arg = node.params
        parts = [agg_kind]
        if not has_arg:
            parts.append("*")
        if distinct:
            parts.append("distinct")
        if len(node.children) > (1 if has_arg else 0):
            parts.append("filtered")
        return f"agg({', '.join(parts)})"
    return kind


def render_plan(order, queries, counts):
    """渲染执行依赖图（拓扑序节点 + 依赖边）及每个节点的实际计算次数。"""
    ids = {n.uid: f"N{i}" for i, n in enumerate(order, 1)}
    lines = ["== 执行依赖图（拓扑序） =="]
    for n in order:
        ev = counts.evals.get(n.uid, 0)
        ro = counts.row_ops.get(n.uid, 0)
        lines.append(
            f"{ids[n.uid]:>4}  {_label(n):<34} : {n.type:<8} "
            f"evals={ev} row_ops={ro}"
        )
    lines.append("-- 依赖边（parent -> child） --")
    for n in order:
        for c in n.children:
            lines.append(f"{ids[n.uid]} -> {ids[c.uid]}")
    lines.append("-- 查询输出映射 --")
    for qi, query in enumerate(queries, 1):
        for alias, node in query.items():
            lines.append(f"Q{qi}.{alias} = {ids[node.uid]}")
    total_evals = sum(counts.evals.values())
    total_rows = sum(counts.row_ops.values())
    lines.append(f"-- 合计: 节点计算 {total_evals} 次, 行级运算 {total_rows} 次 --")
    return "\n".join(lines)
