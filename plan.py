"""关系算子、反向列裁剪与本地多重集合执行（不调用 SQL 引擎）。

语义约定：
- 行是 dict[限定列名, 值]，表是行的 list —— 多重集合，重复行保留；
- NULL 用 None 表示；过滤/连接仅保留谓词为 True 的行（UNKNOWN 不保留）；
- 排序：升序 NULLS LAST，降序 NULLS FIRST，排序稳定；
- 分组：NULL 键归入同一组（SQL 语义）；
- 裁剪到零列的扫描仍逐行产生空字典，行重数不变（COUNT(*) 不变空表）。
"""
from __future__ import annotations

from dataclasses import dataclass, replace
from typing import Any

from exprs import Expr


@dataclass
class Evidence:
    """一条裁剪证据：某算子保留/裁掉了哪些列，以及原因。"""

    operator: str
    kept: tuple[str, ...]
    pruned: tuple[str, ...]
    reason: str


class Node:
    def run(self) -> tuple[list[str], list[dict[str, Any]]]:
        raise NotImplementedError


@dataclass
class Scan(Node):
    table: str
    schema: tuple[str, ...]            # 全部列（限定名 'table.col'）
    rows: list[dict[str, Any]]
    kept: tuple[str, ...] | None = None  # None 表示尚未裁剪

    def run(self) -> tuple[list[str], list[dict[str, Any]]]:
        cols = list(self.kept) if self.kept is not None else list(self.schema)
        # 多重集合：逐行投影不去重；零列时仍保留行数
        return cols, [{c: r[c] for c in cols} for r in self.rows]


@dataclass
class Project(Node):
    items: tuple[tuple[str, Expr], ...]  # (输出列名, 表达式)
    child: Node

    def run(self) -> tuple[list[str], list[dict[str, Any]]]:
        _, rows = self.child.run()
        cols = [name for name, _ in self.items]
        return cols, [{name: expr.eval(r) for name, expr in self.items} for r in rows]


@dataclass
class Filter(Node):
    pred: Expr
    child: Node

    def run(self) -> tuple[list[str], list[dict[str, Any]]]:
        cols, rows = self.child.run()
        return cols, [r for r in rows if self.pred.eval(r) is True]


@dataclass
class Join(Node):
    cond: Expr
    left: Node
    right: Node

    def run(self) -> tuple[list[str], list[dict[str, Any]]]:
        lcols, lrows = self.left.run()
        rcols, rrows = self.right.run()
        out: list[dict[str, Any]] = []
        for lrow in lrows:
            for rrow in rrows:
                merged = {**lrow, **rrow}  # 限定名保证同名列不互相覆盖
                if self.cond.eval(merged) is True:
                    out.append(merged)
        return lcols + rcols, out


def _sort_key(value: Any) -> tuple[bool, Any]:
    # None 视作最大：升序时 NULLS LAST；配合 reverse 降序时 NULLS FIRST
    return (value is None, value)


@dataclass
class Sort(Node):
    keys: tuple[tuple[Expr, bool], ...]  # (排序表达式, 是否降序)
    child: Node
    out_cols: tuple[str, ...] | None = None  # 排序完成后保留的列（裁掉纯排序键）

    def run(self) -> tuple[list[str], list[dict[str, Any]]]:
        cols, rows = self.child.run()
        rows = list(rows)
        for expr, desc in reversed(self.keys):  # 稳定排序：从末键到首键
            rows.sort(key=lambda r, e=expr: _sort_key(e.eval(r)), reverse=desc)
        if self.out_cols is not None:
            cols = [c for c in self.out_cols]
            rows = [{c: r[c] for c in cols} for r in rows]
        return cols, rows


@dataclass(frozen=True)
class Agg:
    func: str          # 'count_star' | 'count' | 'sum' | 'avg' | 'min' | 'max'
    arg: Expr | None   # count_star 为 None —— 不依赖任何列
    out: str


def _eval_agg(agg: Agg, rows: list[dict[str, Any]]) -> Any:
    if agg.func == "count_star":
        return len(rows)  # 行即使没有任何列也仍被计数
    assert agg.arg is not None
    values = [agg.arg.eval(r) for r in rows]
    non_null = [v for v in values if v is not None]
    if agg.func == "count":
        return len(non_null)
    if not non_null:
        return None
    if agg.func == "sum":
        return sum(non_null)
    if agg.func == "avg":
        return sum(non_null) / len(non_null)
    if agg.func == "min":
        return min(non_null)
    if agg.func == "max":
        return max(non_null)
    raise ValueError(f"未知聚合: {agg.func}")


@dataclass
class Aggregate(Node):
    group_keys: tuple[tuple[str, Expr], ...]  # (输出名, 分组表达式)
    aggs: tuple[Agg, ...]
    child: Node
    out_cols: tuple[str, ...] | None = None  # 裁剪后实际输出的列

    def run(self) -> tuple[list[str], list[dict[str, Any]]]:
        _, rows = self.child.run()
        groups: dict[tuple[Any, ...], list[dict[str, Any]]] = {}
        order: list[tuple[Any, ...]] = []
        for r in rows:
            key = tuple(expr.eval(r) for _, expr in self.group_keys)
            if key not in groups:
                groups[key] = []
                order.append(key)
            groups[key].append(r)
        all_cols = [n for n, _ in self.group_keys] + [a.out for a in self.aggs]
        cols = [c for c in all_cols if self.out_cols is None or c in self.out_cols]
        out = []
        for key in order:
            members = groups[key]
            row: dict[str, Any] = {}
            for (name, _), value in zip(self.group_keys, key):
                row[name] = value
            for agg in self.aggs:
                row[agg.out] = _eval_agg(agg, members)
            out.append({c: row[c] for c in cols})
        return cols, out


# ---------------------------------------------------------------- 反向列裁剪

def prune(node: Node, needed: frozenset[str]) -> tuple[Node, list[Evidence]]:
    """自顶向下传入所需列集合，自底向上回报裁剪证据。

    needed 是父算子要求本算子输出的限定列名（或输出别名）集合。
    """
    if isinstance(node, Scan):
        kept = tuple(c for c in node.schema if c in needed)
        pruned = tuple(c for c in node.schema if c not in needed)
        ev = Evidence(
            operator=f"Scan({node.table})",
            kept=kept,
            pruned=pruned,
            reason="未被输出或任何表达式引用，扫描层裁剪；行数不变",
        )
        return replace(node, kept=kept), [ev]

    if isinstance(node, Project):
        kept_items = tuple(it for it in node.items if it[0] in needed)
        pruned_names = tuple(n for n, _ in node.items if n not in needed)
        child_needed = frozenset().union(*(e.refs() for _, e in kept_items)) if kept_items else frozenset()
        new_child, evs = prune(node.child, child_needed)
        ev = Evidence(
            operator="Project",
            kept=tuple(n for n, _ in kept_items),
            pruned=pruned_names,
            reason="输出列不在上层需求中，投影项裁剪；保留项的表达式引用继续下推",
        )
        return Project(kept_items, new_child), evs + [ev]

    if isinstance(node, Filter):
        pred_refs = node.pred.refs()
        new_child, evs = prune(node.child, needed | pred_refs)
        ev = Evidence(
            operator="Filter",
            kept=tuple(sorted(pred_refs)),
            pruned=(),
            reason=f"谓词隐式依赖保留下推: {sorted(pred_refs)}",
        )
        return Filter(node.pred, new_child), evs + [ev]

    if isinstance(node, Join):
        lschema = _schema_of(node.left)
        rschema = _schema_of(node.right)
        cond_refs = node.cond.refs()
        left_needed = (needed & lschema) | (cond_refs & lschema)
        right_needed = (needed & rschema) | (cond_refs & rschema)
        new_left, evs_l = prune(node.left, left_needed)
        new_right, evs_r = prune(node.right, right_needed)
        ev = Evidence(
            operator="Join",
            kept=tuple(sorted(cond_refs)),
            pruned=(),
            reason=f"连接条件隐式依赖按来源拆分: 左={sorted(cond_refs & lschema)}, 右={sorted(cond_refs & rschema)}",
        )
        return Join(node.cond, new_left, new_right), evs_l + evs_r + [ev]

    if isinstance(node, Sort):
        key_refs = frozenset().union(*(e.refs() for e, _ in node.keys)) if node.keys else frozenset()
        new_child, evs = prune(node.child, needed | key_refs)
        child_cols, _ = _peek_schema(new_child)
        out_cols = tuple(c for c in child_cols if c in needed)
        dropped = tuple(c for c in child_cols if c not in needed)
        ev = Evidence(
            operator="Sort",
            kept=tuple(sorted(key_refs)),
            pruned=dropped,
            reason=f"排序键 {sorted(key_refs)} 保留至排序完成，排序后裁掉非输出列: {list(dropped)}",
        )
        return Sort(node.keys, new_child, out_cols), evs + [ev]

    if isinstance(node, Aggregate):
        group_refs = frozenset().union(*(e.refs() for _, e in node.group_keys)) if node.group_keys else frozenset()
        arg_refs = frozenset().union(*(a.arg.refs() for a in node.aggs if a.arg is not None)) if node.aggs else frozenset()
        new_child, evs = prune(node.child, group_refs | arg_refs)
        all_cols = tuple(n for n, _ in node.group_keys) + tuple(a.out for a in node.aggs)
        out_cols = tuple(c for c in all_cols if c in needed)
        ev = Evidence(
            operator="Aggregate",
            kept=tuple(sorted(group_refs | arg_refs)),
            pruned=tuple(c for c in all_cols if c not in needed),
            reason="分组键与聚合参数引用下推；COUNT(*) 无列依赖，子算子可裁至零列但行数保留",
        )
        return Aggregate(node.group_keys, node.aggs, new_child, out_cols), evs + [ev]

    raise TypeError(f"未知算子: {type(node)}")


def _schema_of(node: Node) -> frozenset[str]:
    if isinstance(node, Scan):
        return frozenset(node.schema)
    if isinstance(node, Project):
        return frozenset(n for n, _ in node.items)
    if isinstance(node, Filter):
        return _schema_of(node.child)
    if isinstance(node, Join):
        return _schema_of(node.left) | _schema_of(node.right)
    if isinstance(node, Sort):
        return _schema_of(node.child)
    if isinstance(node, Aggregate):
        return frozenset([n for n, _ in node.group_keys] + [a.out for a in node.aggs])
    raise TypeError(type(node))


def _peek_schema(node: Node) -> tuple[list[str], None]:
    """裁剪后算子的输出列顺序（不执行）。"""
    if isinstance(node, Scan):
        return list(node.kept) if node.kept is not None else list(node.schema), None
    if isinstance(node, Project):
        return [n for n, _ in node.items], None
    if isinstance(node, Filter):
        return _peek_schema(node.child)
    if isinstance(node, Join):
        l, _ = _peek_schema(node.left)
        r, _ = _peek_schema(node.right)
        return l + r, None
    if isinstance(node, Sort):
        cols, _ = _peek_schema(node.child)
        if node.out_cols is not None:
            cols = [c for c in cols if c in node.out_cols]
        return cols, None
    if isinstance(node, Aggregate):
        cols = [n for n, _ in node.group_keys] + [a.out for a in node.aggs]
        if node.out_cols is not None:
            cols = [c for c in cols if c in node.out_cols]
        return cols, None
    raise TypeError(type(node))
