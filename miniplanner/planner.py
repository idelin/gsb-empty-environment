"""Parameter-sensitive plan selection and a local multiset executor."""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Mapping, Sequence

from miniplanner.catalog import Catalog, ColumnStats, Table, logical_type, sql_compare
from miniplanner.evaluator import (
    TRUE,
    bind_and_simplify,
    eval_predicate,
    eval_scalar,
    flatten_and,
    is_true,
)
from miniplanner.expr import (
    ColRef,
    Comparison,
    ComparisonOp,
    Expr,
    Literal,
    Param,
    expr_key,
    walk_exprs,
)
from miniplanner.query import Query

INDEX_SELECTIVITY_THRESHOLD = 0.5


@dataclass(frozen=True)
class Access:
    column: str
    op: ComparisonOp
    scalar: Expr
    selectivity: float
    key_category: str

    def value(self, params: Sequence[Any]) -> Any:
        return eval_scalar(self.scalar, {}, params)


@dataclass(frozen=True)
class Plan:
    name: str
    query: Query
    predicate: Expr
    access: Access | None
    description: str
    uses_index: bool = False
    always_empty: bool = False
    simplification: str = "none"

    def describe(self) -> str:
        if self.access is None:
            return self.description
        return f"{self.description} on {self.access.column} using {scalar_label(self.access.scalar)}"


def scalar_label(expr: Expr) -> str:
    if isinstance(expr, Param):
        return f"${expr.index}"
    if isinstance(expr, Literal):
        return repr(expr.value)
    return "?"


@dataclass
class ExecuteResult:
    rows: list[dict[str, Any]]
    plan_name: str
    plan_description: str
    cache_hit: bool
    catalog_version: int


@dataclass
class PlanCacheStats:
    hits: int = 0
    misses: int = 0
    invalidations: int = 0
    replans: int = 0


@dataclass
class PlanCache:
    entries: dict[tuple[Any, ...], Plan] = field(default_factory=dict)
    catalog_version: int = 0
    stats: PlanCacheStats = field(default_factory=PlanCacheStats)


class PreparedStatement:
    def __init__(self, planner: "QueryPlanner", query: Query) -> None:
        self.planner = planner
        self.query = query

    def execute(self, params: Sequence[Any] | None = None) -> ExecuteResult:
        return self.planner.execute(self.query, params or ())

    def plan_for(self, params: Sequence[Any] | None = None) -> Plan:
        return self.planner.plan_for(self.query, params or ())


class QueryPlanner:
    def __init__(self, catalog: Catalog) -> None:
        self.catalog = catalog
        self.cache = PlanCache(catalog_version=catalog.version)

    def prepare(self, query: Query) -> PreparedStatement:
        self.catalog.get(query.table)
        return PreparedStatement(self, query)

    def invalidate_cache(self) -> None:
        self.cache.entries.clear()
        self.cache.stats.invalidations += 1
        self.cache.catalog_version = self.catalog.version

    def execute(self, query: Query, params: Sequence[Any]) -> ExecuteResult:
        plan, cache_hit = self._get_or_create_plan(query, params)
        rows = self._execute_plan(plan, params)
        return ExecuteResult(
            rows=rows,
            plan_name=plan.name,
            plan_description=plan.description,
            cache_hit=cache_hit,
            catalog_version=self.catalog.version,
        )

    def plan_for(self, query: Query, params: Sequence[Any]) -> Plan:
        plan, _ = self._get_or_create_plan(query, params)
        return plan

    def _get_or_create_plan(self, query: Query, params: Sequence[Any]) -> tuple[Plan, bool]:
        if len(params) != query.arity():
            raise ValueError(f"expected {query.arity()} parameters, got {len(params)}")
        if self.cache.catalog_version != self.catalog.version:
            self.invalidate_cache()
        table = self.catalog.get(query.table)
        bound_predicate = (
            bind_and_simplify(query.predicate, table, params)
            if query.predicate is not None
            else TRUE
        )
        cache_key = (
            query.key(),
            self.catalog.version,
            self._parameter_profile(query, table, params),
        )
        cached = self.cache.entries.get(cache_key)
        if cached is not None:
            self.cache.stats.hits += 1
            return cached, True
        self.cache.stats.misses += 1
        self.cache.stats.replans += 1
        plan = self._select_plan(query, bound_predicate, table, params)
        self.cache.entries[cache_key] = plan
        return plan, False

    def _select_plan(self, query: Query, bound: Expr, table: Table, params: Sequence[Any]) -> Plan:
        if isinstance(bound, Literal) and bound.value is not True:
            return Plan(
                name="EMPTY_RESULT",
                query=query,
                predicate=bound,
                access=None,
                description="top-level predicate is false or unknown under SQL three-valued logic",
                always_empty=True,
                simplification="false" if bound.value is False else "unknown",
            )
        terms = flatten_and(bound)
        if any(isinstance(term, Literal) and term.value is False for term in terms):
            return Plan(
                name="EMPTY_RESULT",
                query=query,
                predicate=bound,
                access=None,
                description="stats or null proven contradiction; no rows are examined",
                always_empty=True,
                simplification="false",
            )
        if terms and all(isinstance(term, Literal) and term.value is not True for term in terms):
            return Plan(
                name="EMPTY_RESULT",
                query=query,
                predicate=bound,
                access=None,
                description="top-level predicate is unknown under SQL three-valued logic",
                always_empty=True,
                simplification="unknown",
            )

        best_access: Access | None = None
        for term in terms:
            access = self._sargable_access(term, table, params)
            if access is not None and (best_access is None or access.selectivity < best_access.selectivity):
                best_access = access

        if best_access is not None and best_access.selectivity <= INDEX_SELECTIVITY_THRESHOLD:
            return Plan(
                name="INDEX_RANGE_SCAN",
                query=query,
                predicate=bound,
                access=best_access,
                description=(
                    f"index scan on {table.name}.{best_access.column} "
                    f"({best_access.op} {scalar_label(best_access.scalar)}), residual predicate"
                ),
                uses_index=True,
                simplification="bound",
            )

        return Plan(
            name="FULL_SCAN",
            query=query,
            predicate=bound,
            access=None,
            description=f"full scan on {table.name}, evaluate predicate per row",
            simplification="bound",
        )

    def _sargable_access(self, term: Expr, table: Table, params: Sequence[Any]) -> Access | None:
        if not isinstance(term, Comparison):
            return None
        match = self._column_scalar(term)
        if match is None:
            return None
        column, op, scalar = match
        if op == "<>":
            return None
        stats = table.column_stats(column)
        value = eval_scalar(scalar, {}, params)
        category = table.value_category(column, value)
        selectivity = self._selectivity(stats, table.row_count, op, category)
        key_category = f"{logical_type(value)}:{category}"
        return Access(column=column, op=op, scalar=scalar, selectivity=selectivity, key_category=key_category)

    def _column_scalar(self, expr: Comparison) -> tuple[str, ComparisonOp, Expr] | None:
        if isinstance(expr.left, ColRef) and not isinstance(expr.right, ColRef):
            return expr.left.name, expr.op, expr.right
        if isinstance(expr.right, ColRef) and not isinstance(expr.left, ColRef):
            flipped = {"=": "=", "<>": "<>", "<": ">", "<=": ">=", ">": "<", ">=": "<="}
            return expr.right.name, flipped[expr.op], expr.left
        return None

    def _selectivity(
        self,
        stats: ColumnStats,
        row_count: int,
        op: ComparisonOp,
        category: str,
    ) -> float:
        if row_count <= 0:
            return 0.0
        nonnull = row_count - stats.null_count
        nonnull_fraction = max(nonnull, 0) / row_count
        if category in {"null", "empty_domain"}:
            return 0.0
        if category == "incompatible":
            return 0.0 if op == "=" else 1.0
        if op == "=":
            if category in {"below_min", "above_max"}:
                return 0.0
            if stats.distinct_nonnull > 0:
                return 1.0 / stats.distinct_nonnull
            return nonnull_fraction
        if category == "below_min":
            return 0.0 if op in {"<", "<="} else nonnull_fraction
        if category == "above_max":
            return nonnull_fraction if op in {"<", "<="} else 0.0
        estimates = {
            "<": {"within_low": 0.17, "within_mid": 0.50, "within_high": 0.83},
            "<=": {"within_low": 0.17, "within_mid": 0.50, "within_high": 0.83},
            ">": {"within_low": 0.83, "within_mid": 0.50, "within_high": 0.17},
            ">=": {"within_low": 0.83, "within_mid": 0.50, "within_high": 0.17},
        }
        return nonnull_fraction * estimates[op][category]

    def _parameter_profile(
        self,
        query: Query,
        table: Table,
        params: Sequence[Any],
    ) -> tuple[tuple[Any, ...], ...]:
        parameter_types = tuple(
            (index, logical_type(params[index])) for index in range(query.arity())
        )
        column_facts: list[tuple[Any, ...]] = []
        scalar_facts: list[tuple[Any, ...]] = []
        if query.predicate is not None:
            for node in walk_exprs(query.predicate):
                if isinstance(node, Comparison):
                    match = self._column_scalar(node)
                    if match is not None:
                        column, _, scalar = match
                        value = eval_scalar(scalar, {}, params)
                        column_facts.append(
                            expr_key(node)
                            + (logical_type(value), table.value_category(column, value))
                        )
                    elif not isinstance(node.left, ColRef) and not isinstance(node.right, ColRef):
                        left = eval_scalar(node.left, {}, params)
                        right = eval_scalar(node.right, {}, params)
                        scalar_facts.append(
                            expr_key(node)
                            + (
                                logical_type(left),
                                "scalar",
                                logical_type(right),
                                _bool_or_null(sql_compare(left, right, node.op)),
                            )
                        )
        return (
            ("parameter_types", parameter_types),
            ("column_comparisons", tuple(sorted(column_facts, key=repr))),
            ("scalar_comparisons", tuple(sorted(scalar_facts, key=repr))),
        )

    def _execute_plan(self, plan: Plan, params: Sequence[Any]) -> list[dict[str, Any]]:
        table = self.catalog.get(plan.query.table)
        if plan.always_empty:
            return []

        if plan.access is not None:
            value = plan.access.value(params)
            row_ids = table.index_lookup(plan.access.column, plan.access.op, value)
            candidates = (table.rows[row_id] for row_id in row_ids)
        else:
            candidates = iter(table.rows)

        rows = []
        for row in candidates:
            result = eval_predicate(plan.predicate, row, params)
            if is_true(result):
                rows.append(self._project(row, plan.query.projection))
        return rows

    def _project(self, row: Mapping[str, Any], projection: Sequence[str] | None) -> dict[str, Any]:
        if projection is None:
            return dict(row)
        return {column: row.get(column) for column in projection}


def _bool_or_null(value: bool | None) -> bool | None:
    return value
