from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from .aggregate_node import Aggregate
from .aggregates import DISTRIBUTIVE_FUNCTIONS, AggExpr
from .expressions import (
    ColRef,
    Expression,
    combine_conjunction,
    is_null_rejecting,
    split_conjunction,
)
from .operators import (
    Filter,
    Join,
    Limit,
    LocalTopK,
    MergeTopK,
    Node,
    Scan,
    Sort,
)
from .relations import Column, Schema, Table


@dataclass(frozen=True)
class RewriteProof:
    rule: str
    safe: bool
    reason: str
    premises: tuple[str, ...]


@dataclass(frozen=True)
class PlannedQuery:
    original: Node
    rewritten: Node
    changed: bool
    proofs: tuple[RewriteProof, ...]


@dataclass(frozen=True)
class PartitionScan(Node):
    table_name: str
    partition_id: int
    resolved_schema: Schema

    @property
    def schema(self) -> Schema:
        return self.resolved_schema

    def execute(self, tables: dict[str, Any]) -> Any:
        from .relations import Relation
        table = tables[self.table_name]
        return Relation(self.resolved_schema, table.partitions[self.partition_id])


def resolve_schema(node: Node, tables: dict[str, Table]) -> Node:
    if isinstance(node, Scan):
        return node
    if isinstance(node, Filter):
        return Filter(resolve_schema(node.source, tables), node.predicate)
    if isinstance(node, Join):
        return Join(
            resolve_schema(node.left, tables),
            resolve_schema(node.right, tables),
            node.join_type,
            node.left_keys,
            node.right_keys,
            node.predicate,
        )
    if isinstance(node, Aggregate):
        return Aggregate(
            resolve_schema(node.source, tables),
            node.group_keys,
            node.aggregates,
            node.mode,
            node.outer_pad,
        )
    if isinstance(node, Sort):
        return Sort(resolve_schema(node.source, tables), node.order)
    if isinstance(node, Limit):
        return Limit(resolve_schema(node.source, tables), node.offset, node.limit)
    if isinstance(node, LocalTopK):
        return LocalTopK(
            node.partition_id,
            resolve_schema(node.source, tables),
            node.order,
            node.offset,
            node.limit,
        )
    if isinstance(node, MergeTopK):
        return MergeTopK(
            tuple(resolve_schema(child, tables) for child in node.children),
            node.order,
            node.offset,
            node.limit,
        )
    return node


def _scan(node: Node) -> Scan | None:
    return node if isinstance(node, Scan) else None


def _columns(node: Node) -> frozenset[Column]:
    return frozenset(node.schema.columns)


def _rewrite_children(node: Node, tables: dict[str, Table], proofs: list[RewriteProof]) -> Node:
    if isinstance(node, Filter):
        return Filter(optimize_node(node.source, tables, proofs), node.predicate)
    if isinstance(node, Join):
        return Join(
            optimize_node(node.left, tables, proofs),
            optimize_node(node.right, tables, proofs),
            node.join_type,
            node.left_keys,
            node.right_keys,
            node.predicate,
        )
    if isinstance(node, Aggregate):
        return Aggregate(
            optimize_node(node.source, tables, proofs),
            node.group_keys,
            node.aggregates,
            node.mode,
            node.outer_pad,
        )
    if isinstance(node, Sort):
        return Sort(optimize_node(node.source, tables, proofs), node.order)
    if isinstance(node, Limit):
        return Limit(optimize_node(node.source, tables, proofs), node.offset, node.limit)
    return node


def optimize_node(node: Node, tables: dict[str, Table], proofs: list[RewriteProof]) -> Node:
    if isinstance(node, Scan):
        return node
    if isinstance(node, Filter) and isinstance(node.source, Join):
        rewritten = _try_outer_filter(node, tables, proofs)
        if rewritten is not None:
            return optimize_node(rewritten, tables, proofs)
    if isinstance(node, Aggregate) and isinstance(node.source, Join):
        rewritten = _try_aggregate_pushdown(node, tables, proofs)
        if rewritten is not None:
            return rewritten
    if isinstance(node, Limit) and isinstance(node.source, Sort) and isinstance(node.source.source, Scan):
        rewritten = _try_distributed_sort_limit(node, node.source, node.source.source, tables, proofs)
        if rewritten is not None:
            return rewritten
    if isinstance(node, Sort) and isinstance(node.source, Limit) and isinstance(node.source.source, Scan):
        rewritten = _try_distributed_sort_limit(node.source, node, node.source.source, tables, proofs)
        if rewritten is not None:
            return rewritten
    return _rewrite_children(node, tables, proofs)


def _try_outer_filter(
    filter_node: Filter,
    tables: dict[str, Table],
    proofs: list[RewriteProof],
) -> Node | None:
    join = filter_node.source
    if not isinstance(join, Join):
        return None
    if join.join_type not in {"inner", "left", "right", "full"}:
        return None
    left_columns = _columns(join.left)
    right_columns = _columns(join.right)
    push_left: list[Expression] = []
    right_predicates: list[Expression] = []
    residual: list[Expression] = []
    rejected_reasons: list[str] = []
    convert_left_to_inner = False

    for predicate in split_conjunction(filter_node.predicate):
        refs = predicate.references()
        if join.join_type == "inner":
            if refs <= left_columns:
                push_left.append(predicate)
            elif refs <= right_columns:
                right_predicates.append(predicate)
            else:
                rejected_reasons.append("mixed predicate must remain above an inner join")
                residual.append(predicate)
        elif join.join_type == "left":
            if refs <= left_columns:
                push_left.append(predicate)
            elif refs <= right_columns and is_null_rejecting(predicate, right_columns):
                right_predicates.append(predicate)
                convert_left_to_inner = True
            else:
                if refs & right_columns:
                    rejected_reasons.append("right predicate is not provably null-rejecting or is mixed")
                residual.append(predicate)
        else:
            proofs.append(
                RewriteProof(
                    "outer-join-filter-pushdown",
                    False,
                    "right and full outer join rewrite are not enabled",
                    ("only inner and left outer joins are rewritten by this planner",),
                )
            )
            return None

    safe = bool(push_left or right_predicates) and not residual and not rejected_reasons
    if not safe:
        proofs.append(
            RewriteProof(
                "outer-join-filter-pushdown",
                False,
                "; ".join(rejected_reasons) or "residual predicate must remain above the join",
                (
                    "the original Filter remains above the Join",
                    "pushing the whole predicate is required by this conservative rule",
                ),
            )
        )
        return None

    pushed_left = optimize_node(join.left, tables, proofs)
    pushed_right = optimize_node(join.right, tables, proofs)
    new_left = Filter(pushed_left, combine_conjunction(tuple(push_left))) if push_left else pushed_left
    new_right = Filter(pushed_right, combine_conjunction(tuple(right_predicates))) if right_predicates else pushed_right
    result = Join(
        new_left,
        new_right,
        "inner" if convert_left_to_inner else "left",
        join.left_keys,
        join.right_keys,
        join.predicate,
    )
    proofs.append(
        RewriteProof(
            "outer-join-filter-pushdown",
            True,
            "all conjuncts have a safe one-side interpretation",
            (
                "left-only predicates run before a left outer join without removing unmatched rows",
                "right-only null-rejecting predicates remove exactly the unmatched rows",
                "the join is collapsed to an inner join when a right null-rejecting predicate exists",
            ),
        )
    )
    return result


def _try_aggregate_pushdown(
    aggregate_node: Aggregate,
    tables: dict[str, Table],
    proofs: list[RewriteProof],
) -> Node | None:
    join = aggregate_node.source
    if not isinstance(join, Join) or join.predicate is not None:
        proofs.append(
            RewriteProof(
                "aggregate-pushdown",
                False,
                "aggregate source is not a simple equi-join",
                ("only an equi-join without an additional predicate is supported",),
            )
        )
        return None
    if aggregate_node.mode != "complete":
        return None
    if join.join_type != "left":
        proofs.append(
            RewriteProof(
                "aggregate-pushdown",
                False,
                f"{join.join_type} join aggregate pushdown is not enabled in this conservative planner",
                ("only a proven left outer join shape is rewritten",),
            )
        )
        return None

    left_scan = _scan(join.left)
    right_scan = _scan(join.right)
    if left_scan is None or right_scan is None:
        proofs.append(
            RewriteProof(
                "aggregate-pushdown",
                False,
                "join inputs must be scans in this bounded planner",
                ("fan-out and visibility of intermediate operators are not inferred",),
            )
        )
        return None

    left_columns = _columns(join.left)
    right_columns = _columns(join.right)
    group_keys = set(aggregate_node.group_keys)
    left_group = tuple(key for key in aggregate_node.group_keys if key in left_columns)
    right_group = tuple(key for key in aggregate_node.group_keys if key in right_columns)
    mixed_group = [key for key in aggregate_node.group_keys if key not in left_columns | right_columns]
    if mixed_group:
        proofs.append(
            RewriteProof(
                "aggregate-pushdown",
                False,
                "group key is not supplied by one join input",
                ("all GROUP BY columns must be direct left or right columns",),
            )
        )
        return None

    for aggregate in aggregate_node.aggregates:
        references = frozenset() if aggregate.source is None else aggregate.source.references()
        if not references <= right_columns:
            proofs.append(
                RewriteProof(
                    "aggregate-pushdown",
                    False,
                    "an aggregate references the preserved side or both sides",
                    ("only aggregates of null-supplying/right columns are pushed",),
                )
            )
            return None
        if aggregate.function not in DISTRIBUTIVE_FUNCTIONS:
            proofs.append(
                RewriteProof(
                    "aggregate-pushdown",
                    False,
                    f"{aggregate.function} is not distributive",
                    ("partial/final decomposition is limited to count, sum, avg, min, max",),
                )
            )
            return None

    if join.join_type == "left":
        required_left = set(join.left_keys)
        if not required_left <= group_keys:
            proofs.append(
                RewriteProof(
                    "aggregate-pushdown",
                    False,
                    "GROUP BY does not expose the full left join key",
                    (
                        "the key is required to distinguish unmatched preserved rows",
                        "aggregation cannot invent rows hidden by the join",
                    ),
                )
            )
            return None
        left_table = tables[left_scan.table_name]
        if not required_left <= left_table.unique_columns:
            proofs.append(
                RewriteProof(
                    "aggregate-pushdown",
                    False,
                    "preserved-side join key is not declared unique",
                    ("duplicate preserved keys would multiply pushed partial aggregates",),
                )
            )
            return None
    partial = Aggregate(
        join.right,
        tuple(dict.fromkeys(right_group + join.right_keys)),
        aggregate_node.aggregates,
        mode="partial",
    )
    pushed_join = Join(
        join.left,
        partial,
        join.join_type,
        join.left_keys,
        join.right_keys,
        None,
    )
    result = Aggregate(
        pushed_join,
        aggregate_node.group_keys,
        aggregate_node.aggregates,
        mode="final",
        outer_pad=join.join_type == "left",
    )
    proofs.append(
        RewriteProof(
            "aggregate-pushdown",
            True,
            "distributive aggregates can be combined from disjoint right groups",
            (
                "all aggregates reference right input columns only",
                "join is an equi-join without extra predicates",
                "required grouping keys survive the join",
                "left outer joins have a unique preserved key and count unmatched rows once",
            ),
        )
    )
    return result


def _try_distributed_sort_limit(
    limit_node: Limit,
    sort_node: Sort,
    scan_node: Scan,
    tables: dict[str, Table],
    proofs: list[RewriteProof],
) -> Node | None:
    table = tables[scan_node.table_name]
    if limit_node.limit is None:
        proofs.append(
            RewriteProof(
                "local-topk-merge",
                False,
                "OFFSET without LIMIT requires all rows",
                ("a finite per-partition prefix cannot be chosen",),
            )
        )
        return None
    if not sort_node.order:
        proofs.append(
            RewriteProof(
                "local-topk-merge",
                False,
                "empty ORDER BY has no defined key",
                ("a deterministic merge key is required",),
            )
        )
        return None

    local_fetch = limit_node.offset + limit_node.limit
    children = tuple(
        LocalTopK(partition_id, PartitionScan(scan_node.table_name, partition_id, table.schema), sort_node.order, 0, local_fetch)
        for partition_id in range(len(table.partitions))
    )
    proofs.append(
        RewriteProof(
            "local-topk-merge",
            True,
            f"each partition keeps local rank [0, {local_fetch}); global merge applies OFFSET {limit_node.offset} and LIMIT {limit_node.limit}",
            (
                "sort keys are totally ordered by SQL key plus stable provenance tie-breaker",
                "global rows outside the first offset+limit rows cannot enter the answer",
                "each local partition must retain offset+limit rows, not only limit rows",
                "multiset duplicates are retained by tuple-based rows",
            ),
        )
    )
    return MergeTopK(children, sort_node.order, limit_node.offset, limit_node.limit)


def optimize_query(root: Node, tables: dict[str, Table]) -> PlannedQuery:
    resolved = resolve_schema(root, tables)
    proofs: list[RewriteProof] = []
    rewritten = optimize_node(resolved, tables, proofs)
    return PlannedQuery(resolved, rewritten, rewritten != resolved, tuple(proofs))
