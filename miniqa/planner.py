from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from typing import Any

from .logic import and3, not3, or3
from .model import And, Column, Comparison, Literal, Not, Or, Param, Select, StaticBool, Tri, UnionAll, node_key
from .table import Catalog, ColumnStats, Table


class RangeCategory(str, Enum):
    NULL = "null"
    TYPE_MISMATCH = "type_mismatch"
    BELOW_MIN = "below_min"
    ABOVE_MAX = "above_max"
    IN_RANGE = "in_range"
    EQUALITY_PRESENT = "equality_present"
    EQUALITY_ABSENT = "equality_absent"
    UNKNOWN_STATS = "unknown_stats"


@dataclass(frozen=True)
class ParamProfile:
    parameter: str
    table: str
    column: str
    type_name: str
    operator: str
    range_category: RangeCategory


@dataclass(frozen=True)
class EmptyPlan:
    reason: str


@dataclass(frozen=True)
class FullScanPlan:
    table: str
    predicate: Any


@dataclass(frozen=True)
class IndexSeekPlan:
    table: str
    column: str
    operator: str
    parameter: str
    predicate: Any


@dataclass(frozen=True)
class UnionAllPlan:
    left: Any
    right: Any


@dataclass(frozen=True)
class ProjectPlan:
    source: Any
    table: str
    columns: tuple[str, ...]


@dataclass(frozen=True)
class PreparedPlan:
    query_key: tuple
    physical: Any
    parameter_profiles: tuple[ParamProfile, ...]


@dataclass
class CacheStats:
    lookups: int = 0
    hits: int = 0
    misses: int = 0
    invalidations: int = 0
    builds: int = 0


class PlanCache:
    def __init__(self, catalog: Catalog) -> None:
        self.catalog = catalog
        self._entries: dict[tuple, PreparedPlan] = {}
        self._seen_catalog_version = catalog.stats_version
        self.stats = CacheStats()

    def get_or_build(self, query: Any, bindings: dict[str, Any]) -> PreparedPlan:
        self.stats.lookups += 1
        self._invalidate_if_needed()
        profiles = tuple(sorted(profile_query(query, bindings, self.catalog), key=lambda p: (p.parameter, p.table, p.column)))
        key = (
            node_key(query),
            self.catalog.stats_version,
            tuple(
                (
                    item.table,
                    item.column,
                    item.parameter,
                    item.type_name,
                    item.operator,
                    item.range_category.value,
                )
                for item in profiles
            ),
        )
        prepared = self._entries.get(key)
        if prepared is not None:
            self.stats.hits += 1
            return prepared
        self.stats.misses += 1
        physical = plan_query(query, bindings, self.catalog)
        prepared = PreparedPlan(query_key=node_key(query), physical=physical, parameter_profiles=tuple(profile.to_frozen() for profile in profiles))
        self._entries[key] = prepared
        self.stats.builds += 1
        return prepared

    def invalidate(self) -> int:
        removed = len(self._entries)
        self._entries.clear()
        if removed:
            self.stats.invalidations += 1
        return removed

    def _invalidate_if_needed(self) -> None:
        if self.catalog.stats_version != self._seen_catalog_version:
            self.invalidate()
            self._seen_catalog_version = self.catalog.stats_version


@dataclass(frozen=True)
class WorkingProfile:
    table: str
    column: str
    parameter: str
    type_name: str
    operator: str
    range_category: RangeCategory

    def to_frozen(self) -> ParamProfile:
        return ParamProfile(
            type_name=self.type_name,
            operator=self.operator,
            parameter=self.parameter,
            table=self.table,
            column=self.column,
            range_category=self.range_category,
        )


def type_name(value: Any) -> str:
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "bool"
    if isinstance(value, int):
        return "int"
    if isinstance(value, float):
        return "float"
    return type(value).__name__


def same_value_type(value: Any, stats: ColumnStats) -> bool:
    if value is None:
        return True
    example = next(iter(stats.values), None)
    if example is None:
        return True
    return type(value) is type(example)


def categorize(value: Any, operator: str, stats: ColumnStats) -> RangeCategory:
    if value is None:
        return RangeCategory.NULL
    if not same_value_type(value, stats):
        return RangeCategory.TYPE_MISMATCH
    if operator == "=":
        return RangeCategory.EQUALITY_PRESENT if value in stats.values else RangeCategory.EQUALITY_ABSENT
    if operator not in {"<", "<=", ">", ">=", "<>"}:
        return RangeCategory.UNKNOWN_STATS
    if stats.minimum is None:
        return RangeCategory.UNKNOWN_STATS
    if value < stats.minimum:
        return RangeCategory.BELOW_MIN
    if value > stats.maximum:
        return RangeCategory.ABOVE_MAX
    return RangeCategory.IN_RANGE


def profile_comparison(node: Comparison, table: Table, bindings: dict[str, Any]) -> tuple[WorkingProfile, ...]:
    column_side = node.left if isinstance(node.left, Column) else node.right if isinstance(node.right, Column) else None
    param_side = node.left if isinstance(node.left, Param) else node.right if isinstance(node.right, Param) else None
    if column_side is None or param_side is None:
        return ()
    if param_side.name not in bindings:
        raise KeyError(f"missing parameter: {param_side.name}")
    value = bindings[param_side.name]
    stats = table.stats[column_side.name]
    return (
        WorkingProfile(
            table=table.name,
            column=column_side.name,
            parameter=param_side.name,
            type_name=type_name(value),
            operator=node.operator,
            range_category=categorize(value, node.operator, stats),
        ),
    )


def profile_predicate(node: Any, table: Table, bindings: dict[str, Any]) -> tuple[WorkingProfile, ...]:
    if isinstance(node, Comparison):
        return profile_comparison(node, table, bindings)
    if isinstance(node, (And, Or)):
        return profile_predicate(node.left, table, bindings) + profile_predicate(node.right, table, bindings)
    if isinstance(node, Not):
        return profile_predicate(node.operand, table, bindings)
    return ()


def profile_query(node: Any, bindings: dict[str, Any], catalog: Catalog) -> tuple[WorkingProfile, ...]:
    if isinstance(node, Select):
        return profile_predicate(node.predicate, catalog.get(node.table), bindings)
    if isinstance(node, UnionAll):
        return profile_query(node.left, bindings, catalog) + profile_query(node.right, bindings, catalog)
    return ()


def simplify_predicate(node: Any, table: Table, bindings: dict[str, Any]) -> Any:
    if isinstance(node, StaticBool):
        return node
    if isinstance(node, Comparison):
        if isinstance(node.left, Column) and isinstance(node.right, Literal):
            return simplify_literal_comparison(node, table)
        return simplify_comparison(node, table, bindings)
    if isinstance(node, And):
        left = simplify_predicate(node.left, table, bindings)
        right = simplify_predicate(node.right, table, bindings)
        if isinstance(left, StaticBool) and isinstance(right, StaticBool):
            return StaticBool(and3(left.value, right.value))
        if isinstance(left, StaticBool):
            if left.value is Tri.FALSE:
                return StaticBool(Tri.FALSE)
            if left.value is Tri.TRUE:
                return right
            if isinstance(right, StaticBool):
                return StaticBool(and3(left.value, right.value))
        if isinstance(right, StaticBool):
            if right.value is Tri.FALSE:
                return StaticBool(Tri.FALSE)
            if right.value is Tri.TRUE:
                return left
            if isinstance(left, StaticBool):
                return StaticBool(and3(left.value, right.value))
        return And(left, right)
    if isinstance(node, Or):
        left = simplify_predicate(node.left, table, bindings)
        right = simplify_predicate(node.right, table, bindings)
        if isinstance(left, StaticBool) and isinstance(right, StaticBool):
            return StaticBool(or3(left.value, right.value))
        if isinstance(left, StaticBool):
            if left.value is Tri.TRUE:
                return StaticBool(Tri.TRUE)
            if left.value is Tri.FALSE:
                return right
            if isinstance(right, StaticBool):
                return StaticBool(or3(left.value, right.value))
        if isinstance(right, StaticBool):
            if right.value is Tri.TRUE:
                return StaticBool(Tri.TRUE)
            if right.value is Tri.FALSE:
                return left
            if isinstance(left, StaticBool):
                return StaticBool(or3(left.value, right.value))
        return Or(left, right)
    if isinstance(node, Not):
        operand = simplify_predicate(node.operand, table, bindings)
        if isinstance(operand, StaticBool):
            return StaticBool(not3(operand.value))
        return Not(operand)
    return node


def simplify_comparison(node: Comparison, table: Table, bindings: dict[str, Any]) -> Any:
    column_side = node.left if isinstance(node.left, Column) else node.right if isinstance(node.right, Column) else None
    param_side = node.left if isinstance(node.left, Param) else node.right if isinstance(node.right, Param) else None
    if column_side is None or param_side is None or param_side.name not in bindings:
        return node
    value = bindings[param_side.name]
    stats = table.stats[column_side.name]
    category = categorize(value, node.operator, stats)
    if category in {RangeCategory.NULL, RangeCategory.TYPE_MISMATCH}:
        return StaticBool(Tri.UNKNOWN)
    if category is RangeCategory.EQUALITY_ABSENT and stats.null_count == 0:
        return StaticBool(Tri.FALSE)
    if category is RangeCategory.BELOW_MIN:
        if node.operator in {">", ">="}:
            return StaticBool(Tri.FALSE) if stats.null_count == 0 else node
        if node.operator in {"<", "<="}:
            return StaticBool(Tri.TRUE) if stats.null_count == 0 else node
    if category is RangeCategory.ABOVE_MAX:
        if node.operator in {"<", "<="}:
            return StaticBool(Tri.FALSE) if stats.null_count == 0 else node
        if node.operator in {">", ">="}:
            return StaticBool(Tri.TRUE) if stats.null_count == 0 else node
    return node


def simplify_literal_comparison(node: Comparison, table: Table) -> Any:
    stats = table.stats[node.left.name]
    value = node.right.value
    if value is None:
        return StaticBool(Tri.UNKNOWN)
    if node.operator == "=" and value not in stats.values and stats.null_count == 0:
        return StaticBool(Tri.FALSE)
    return node


def plan_query(node: Any, bindings: dict[str, Any], catalog: Catalog) -> Any:
    if isinstance(node, UnionAll):
        return UnionAllPlan(plan_query(node.left, bindings, catalog), plan_query(node.right, bindings, catalog))
    if not isinstance(node, Select):
        raise TypeError("unsupported query")
    table = catalog.get(node.table)
    predicate = simplify_predicate(node.predicate, table, bindings)
    source = plan_select(table, predicate, bindings)
    if node.outputs:
        return ProjectPlan(source=source, table=node.table, columns=tuple(column.name for column in node.outputs))
    return source


def plan_select(table: Table, predicate: Any, bindings: dict[str, Any]) -> Any:
    if isinstance(predicate, StaticBool):
        if predicate.value in {Tri.FALSE, Tri.UNKNOWN}:
            return EmptyPlan(reason=f"static_{predicate.value.value}")
        return FullScanPlan(table=table.name, predicate=predicate)
    candidate = index_candidate(predicate, table, bindings)
    if candidate is None:
        return FullScanPlan(table=table.name, predicate=predicate)
    comparison, profile = candidate
    index_operator = comparison.operator
    if isinstance(comparison.left, Param):
        index_operator = {
            "<": ">",
            "<=": ">=",
            ">": "<",
            ">=": "<=",
            "=": "=",
            "<>": "<>",
        }[index_operator]
    return IndexSeekPlan(
        table=table.name,
        column=profile.column,
        operator=index_operator,
        parameter=profile.parameter,
        predicate=predicate,
    )


def index_candidate(node: Any, table: Table, bindings: dict[str, Any]) -> tuple[Comparison, WorkingProfile] | None:
    candidates = collect_index_candidates(node, table, bindings)
    if not candidates:
        return None
    def rank(item: tuple[Comparison, WorkingProfile]) -> tuple[int, str]:
        _, profile = item
        rank_by_category = {
            RangeCategory.EQUALITY_PRESENT: 0,
            RangeCategory.IN_RANGE: 1,
        }
        return (rank_by_category.get(profile.range_category, 9), profile.column)
    return sorted(candidates, key=rank)[0]


def collect_index_candidates(node: Any, table: Table, bindings: dict[str, Any]) -> tuple[tuple[Comparison, WorkingProfile], ...]:
    if isinstance(node, Comparison):
        profiles = profile_comparison(node, table, bindings)
        if not profiles:
            return ()
        profile = profiles[0]
        if profile.column not in table.indexes:
            return ()
        if profile.operator not in {"=", "<", "<=", ">", ">="}:
            return ()
        if profile.range_category not in {RangeCategory.EQUALITY_PRESENT, RangeCategory.IN_RANGE}:
            return ()
        return ((node, profile),)
    if isinstance(node, And):
        return collect_index_candidates(node.left, table, bindings) + collect_index_candidates(node.right, table, bindings)
    return ()
