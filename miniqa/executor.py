from __future__ import annotations

from collections import Counter
from typing import Any

from .logic import and3, compare, not3, or3
from .model import And, Column, Comparison, Literal, Not, Or, Param, StaticBool, Tri
from .planner import Catalog, EmptyPlan, FullScanPlan, IndexSeekPlan, PlanCache, ProjectPlan, UnionAllPlan
from .table import Table


Relation = Counter


class ExecutionEngine:
    def __init__(self, catalog: Catalog, cache: PlanCache | None = None) -> None:
        self.catalog = catalog
        self.cache = cache or PlanCache(catalog)

    def execute(self, query: Any, bindings: dict[str, Any] | None = None) -> Relation:
        params = dict(bindings or {})
        prepared = self.cache.get_or_build(query, params)
        return self._execute(prepared.physical, params)

    def _execute(self, plan: Any, bindings: dict[str, Any]) -> Relation:
        if isinstance(plan, EmptyPlan):
            return Counter()
        if isinstance(plan, FullScanPlan):
            table = self.catalog.get(plan.table)
            return self._scan(table, plan.predicate, bindings)
        if isinstance(plan, IndexSeekPlan):
            table = self.catalog.get(plan.table)
            return self._index_scan(table, plan, bindings)
        if isinstance(plan, ProjectPlan):
            source = self._execute(plan.source, bindings)
            table = self.catalog.get(plan.table)
            positions = [table.position(column) for column in plan.columns]
            result: Relation = Counter()
            for row, copies in source.items():
                result[tuple(row[position] for position in positions)] += copies
            return result
        if isinstance(plan, UnionAllPlan):
            result = self._execute(plan.left, bindings)
            result.update(self._execute(plan.right, bindings))
            return result
        raise TypeError(f"unsupported plan: {type(plan)!r}")

    def _scan(self, table: Table, predicate: Any, bindings: dict[str, Any]) -> Relation:
        result: Relation = Counter()
        for row_id, copies in enumerate(table.multiplicity):
            if evaluate(predicate, table, table.rows[row_id], bindings) is Tri.TRUE:
                result[table.rows[row_id]] += copies
        return result

    def _index_scan(self, table: Table, plan: IndexSeekPlan, bindings: dict[str, Any]) -> Relation:
        if plan.parameter not in bindings:
            raise KeyError(f"missing parameter: {plan.parameter}")
        value = bindings[plan.parameter]
        index = table.indexes[plan.column]
        result: Relation = Counter()
        for row_id, copies in index.seek(plan.operator, value):
            row = table.rows[row_id]
            if evaluate(plan.predicate, table, row, bindings) is Tri.TRUE:
                result[row] += copies
        return result


def evaluate(node: Any, table: Table, row: tuple[Any, ...], bindings: dict[str, Any]) -> Tri:
    if isinstance(node, StaticBool):
        return node.value
    if isinstance(node, Comparison):
        left = evaluate_operand(node.left, table, row, bindings)
        right = evaluate_operand(node.right, table, row, bindings)
        return compare(left, node.operator, right)
    if isinstance(node, And):
        return and3(evaluate(node.left, table, row, bindings), evaluate(node.right, table, row, bindings))
    if isinstance(node, Or):
        return or3(evaluate(node.left, table, row, bindings), evaluate(node.right, table, row, bindings))
    if isinstance(node, Not):
        return not3(evaluate(node.operand, table, row, bindings))
    raise TypeError(f"unsupported predicate: {type(node)!r}")


def evaluate_operand(node: Any, table: Table, row: tuple[Any, ...], bindings: dict[str, Any]) -> Any:
    if isinstance(node, Column):
        return row[table.position(node.name)]
    if isinstance(node, Param):
        if node.name not in bindings:
            raise KeyError(f"missing parameter: {node.name}")
        return bindings[node.name]
    if isinstance(node, Literal):
        return node.value
    raise TypeError(f"unsupported operand: {type(node)!r}")
