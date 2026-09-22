"""分区裁剪分析器。

对每个分区判定「谓词是否可能对该分区中某一行求值为 TRUE」：
- 可能 -> 保留（kept），附保留理由；
- 不可能 -> 跳过（skipped），附跳过证明（为什么每行都必为 FALSE/UNKNOWN）。

保守性原则：无法证明不可能时一律保留（如参数未绑定、约束缺失的列、
catch-all 分区），保证结果与全表扫描完全一致。
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Mapping

from .partition import (KeyEnum, KeyEnumComplement, KeyNull, KeyRange,
                        Interval, Partition, PartitionedTable)
from .predicate import And, Compare, Expr, IsNull, Or, Param, _cmp, normalize


@dataclass(frozen=True)
class Decision:
    partition: str
    kept: bool
    proof: str


def _pred_interval(op: str, v: int) -> Interval | None:
    """key <op> v 的键域；!= 不是单一区间，返回 None 单独处理。"""
    return {
        "=": Interval(v, v),
        "<": Interval(None, v - 1),
        "<=": Interval(None, v),
        ">": Interval(v + 1, None),
        ">=": Interval(v, None),
    }.get(op)


def _unbound(param: Param, params: Mapping[str, Any] | None) -> bool:
    return params is None or param.name not in params


def _may_match(expr: Expr, part: Partition, key_column: str,
               params: Mapping[str, Any] | None) -> tuple[bool, str]:
    c = part.constraint

    if isinstance(expr, Compare):
        if expr.column != key_column or c is None:
            return True, f"列 {expr.column} 无分区键约束，保守保留"
        if isinstance(expr.value, Param) and _unbound(expr.value, params):
            if isinstance(c, KeyNull):
                return False, ("键恒为 NULL，NULL 与任何值比较结果均为 UNKNOWN，"
                               "WHERE 不保留 UNKNOWN 行 ⇒ 该分区每行都被过滤")
            return True, f"参数 {expr.value} 未绑定，无法排除任何区间 ⇒ 保守保留"
        v = params[expr.value.name] if isinstance(expr.value, Param) else expr.value
        if v is None:
            return False, "与 NULL 比较结果恒为 UNKNOWN，WHERE 不保留 ⇒ 可跳过"
        if isinstance(c, KeyNull):
            return False, ("键恒为 NULL，NULL 比较结果为 UNKNOWN，"
                           "WHERE 不保留 UNKNOWN 行 ⇒ 该分区每行都被过滤")
        if isinstance(c, KeyRange):
            if expr.op == "!=":
                if c.interval == Interval(v, v):
                    return False, f"键域 {c.interval} 只含 {v}，!= {v} 对每行均为 FALSE ⇒ 可跳过"
                return True, f"键域 {c.interval} 含不等于 {v} 的值，可能命中"
            pi = _pred_interval(expr.op, v)
            if c.interval.intersects(pi):
                return True, f"键域 {c.interval} 与谓词域 {pi} 存在交集，可能命中"
            return False, (f"∀行: 键 ∈ {c.interval}，而谓词要求 键 ∈ {pi}，"
                           f"两区间不相交 ⇒ 谓词对该分区每行均为 FALSE")
        if isinstance(c, KeyEnum):
            hits = sorted(x for x in c.values if _cmp(expr.op, x, v))
            if hits:
                return True, f"枚举值 {hits} 满足 键 {expr.op} {v!r}，可能命中"
            return False, (f"枚举集合 {sorted(c.values)} 中没有任何值满足 "
                           f"键 {expr.op} {v!r} ⇒ 该分区每行均为 FALSE")
        if isinstance(c, KeyEnumComplement):
            if expr.op == "=":
                if v in c.excluded:
                    return False, (f"值 {v!r} 已显式枚举于其他分区，catch-all 只含未列出的值 "
                                   f"⇒ 该分区每行均为 FALSE")
                return True, f"值 {v!r} 未在枚举清单中，可能落入 catch-all"
            return True, "catch-all 键域为补集，无法静态排除 ⇒ 保守保留"
        raise TypeError(c)

    if isinstance(expr, IsNull):
        if expr.column != key_column or c is None:
            return True, f"列 {expr.column} 无分区键约束，保守保留"
        if isinstance(c, KeyNull):
            if expr.negated:
                return False, "键恒为 NULL，IS NOT NULL 对每行均为 FALSE ⇒ 可跳过"
            return True, "NULL 分区，IS NULL 对每行均为 TRUE"
        if expr.negated:
            return True, "该分区键非 NULL，IS NOT NULL 恒为 TRUE"
        return False, "该分区键非 NULL，IS NULL 对每行均为 FALSE ⇒ 可跳过"

    if isinstance(expr, And):
        subs = [_may_match(t, part, key_column, params) for t in expr.terms]
        for term, (ok, why) in zip(expr.terms, subs):
            if not ok:
                return False, f"AND 支路 [{term}] 不可能为真：{why}"
        return True, "AND 各支路均可能为真"

    if isinstance(expr, Or):
        subs = [_may_match(t, part, key_column, params) for t in expr.terms]
        kept = [why for ok, why in subs if ok]
        if kept:
            return True, "OR 存在可能为真的支路：" + "；".join(kept)
        reasons = "；".join(f"[{t}]: {w}" for t, (ok, w) in zip(expr.terms, subs))
        return False, f"OR 各支路均不可能为真 ⇒ 可跳过。{reasons}"

    raise TypeError(f"分析前请先 normalize: {expr!r}")


def analyze(where: Expr | None, table: PartitionedTable,
            params: Mapping[str, Any] | None = None) -> list[Decision]:
    """返回与 table.partitions 一一对应的保留/跳过决定及证明。"""
    if where is None:
        return [Decision(p.name, True, "无过滤条件，保留全部分区") for p in table.partitions]
    pred = normalize(where)
    decisions = []
    for part in table.partitions:
        kept, proof = _may_match(pred, part, table.key_column, params)
        decisions.append(Decision(part.name, kept, proof))
    return decisions
