"""Plan node DAG: typed, versioned operators with static input arity.

Each node is an allow-listed operator carrying its own type tag and a node
``version``. Binding (field resolution + type checking) happens before any
relation is read, so mis-wired and mis-typed plans never start executing.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, ClassVar

from .errors import (
    BindingError,
    DocumentFormatError,
    TypeMismatchError,
    UnknownTableError,
)
from .expr import Expr, BoundExpr, expr_from_dict
from .types import (
    BOOL,
    FLOAT,
    INT,
    NULL,
    Column,
    Relation,
    Schema,
)
from .util import as_str, parse_object

NODE_VERSION = 1

AGG_FUNCS = frozenset({"count", "sum", "avg", "min", "max"})
COUNT_STAR_RETURN = INT
NUMERIC_TYPES = frozenset({INT, FLOAT})


@dataclass
class BoundNode:
    """Runtime state produced by binding a :class:`PlanNode`."""

    output_schema: Schema
    state: dict[str, Any] = field(default_factory=dict)


@dataclass
class PlanNode:
    id: str
    inputs: list[str] = field(default_factory=list)
    tag: ClassVar[str] = ""
    expected_arity: ClassVar[tuple[int, ...]] = ()
    version: ClassVar[int] = NODE_VERSION
    bound: BoundNode | None = field(default=None, repr=False)

    def to_dict(self) -> dict[str, Any]:
        data = {
            "op": self.tag,
            "version": self.version,
            "inputs": list(self.inputs),
        }
        data.update(self.extra_dict())
        return data

    def extra_dict(self) -> dict[str, Any]:
        raise NotImplementedError

    def to_dict_with_id(self) -> dict[str, Any]:
        data = self.to_dict()
        data["id"] = self.id
        ordered = {"id": self.id}
        ordered.update(data)
        return ordered

    def require_arity(self) -> None:
        if len(self.inputs) not in self.expected_arity:
            allowed = (
                "/".join(str(value) for value in self.expected_arity)
            )
            raise BindingError(
                f"node {self.id!r} ({self.tag}) expects {allowed} input(s), "
                f"got {len(self.inputs)}"
            )

    def bind(self, ctx: "BindContext") -> BoundNode:
        raise NotImplementedError

    def run(self, inputs: list[Relation], ctx) -> Relation:
        raise NotImplementedError


@dataclass
class NamedExpr:
    name: str
    expr: Expr

    def to_dict(self) -> dict[str, Any]:
        return {"name": self.name, "expr": self.expr.to_dict()}

    @classmethod
    def from_dict(cls, data: Any, where: str) -> "NamedExpr":
        obj = parse_object(data, where, required=("name", "expr"))
        return cls(
            as_str(obj["name"], f"{where}.name"),
            expr_from_dict(obj["expr"], f"{where}.expr"),
        )


class BindContext:
    """Resolves input schemas for nodes during pre-execution binding."""

    def __init__(self, catalog):
        self.catalog = catalog

    def schema_of(self, node_id: str) -> Schema:
        raise NotImplementedError


@dataclass
class ScanNode(PlanNode):
    table: str = ""
    tag: ClassVar[str] = "scan"
    expected_arity: ClassVar[tuple[int, ...]] = (0,)

    def extra_dict(self) -> dict[str, Any]:
        return {"table": self.table}

    @classmethod
    def build(cls, node_id: str, inputs, args: dict, where: str) -> "ScanNode":
        obj = parse_object(args, where, required=("table",))
        return cls(id=node_id, inputs=list(inputs), table=as_str(obj["table"], f"{where}.table"))

    def bind(self, ctx: BindContext) -> BoundNode:
        if self.table not in ctx.catalog:
            raise UnknownTableError(f"node {self.id!r}: unknown table {self.table!r}")
        return BoundNode(ctx.catalog.get(self.table).schema)

    def run(self, inputs: list[Relation], ctx) -> Relation:
        return ctx.catalog.get(self.table)


@dataclass
class FilterNode(PlanNode):
    predicate: Expr | None = None
    tag: ClassVar[str] = "filter"
    expected_arity: ClassVar[tuple[int, ...]] = (1,)

    def extra_dict(self) -> dict[str, Any]:
        return {"predicate": self.predicate.to_dict()}

    @classmethod
    def build(cls, node_id, inputs, args, where) -> "FilterNode":
        obj = parse_object(args, where, required=("predicate",))
        return cls(
            id=node_id,
            inputs=list(inputs),
            predicate=expr_from_dict(obj["predicate"], f"{where}.predicate"),
        )

    def bind(self, ctx: BindContext) -> BoundNode:
        self.require_arity()
        schema = ctx.schema_of(self.inputs[0])
        bound_predicate = self.predicate.bind(schema, f"node {self.id!r} predicate")
        if bound_predicate.output_type != BOOL:
            raise TypeMismatchError(
                f"node {self.id!r}: filter predicate must be BOOLEAN, "
                f"got {bound_predicate.output_type}"
            )
        return BoundNode(schema, {"predicate": bound_predicate})

    def run(self, inputs: list[Relation], ctx) -> Relation:
        relation = inputs[0]
        predicate = self.bound.state["predicate"]
        rows = [row for row in relation.rows if predicate.eval(row) is True]
        return Relation(relation.schema, rows)


@dataclass
class ProjectNode(PlanNode):
    items: list[NamedExpr] = field(default_factory=list)
    tag: ClassVar[str] = "project"
    expected_arity: ClassVar[tuple[int, ...]] = (1,)

    def extra_dict(self) -> dict[str, Any]:
        return {"items": [item.to_dict() for item in self.items]}

    @classmethod
    def build(cls, node_id, inputs, args, where) -> "ProjectNode":
        obj = parse_object(args, where, required=("items",))
        items_data = obj["items"]
        if not isinstance(items_data, list) or not items_data:
            raise DocumentFormatError(f"{where}.items: expected a non-empty list")
        items = [NamedExpr.from_dict(item, f"{where}.items") for item in items_data]
        return cls(id=node_id, inputs=list(inputs), items=items)

    def bind(self, ctx: BindContext) -> BoundNode:
        self.require_arity()
        input_schema = ctx.schema_of(self.inputs[0])
        where = f"node {self.id!r}"
        columns: list[Column] = []
        bound_items: list[tuple[str, BoundExpr]] = []
        seen: set[str] = set()
        for item in self.items:
            bound_expr = item.expr.bind(input_schema, f"{where} output {item.name!r}")
            if item.name in seen:
                raise TypeMismatchError(
                    f"{where}: duplicate output column name {item.name!r}"
                )
            seen.add(item.name)
            columns.append(Column(item.name, bound_expr.output_type))
            bound_items.append((item.name, bound_expr))
        schema = Schema(tuple(columns))
        return BoundNode(schema, {"items": tuple(bound_items)})

    def run(self, inputs: list[Relation], ctx) -> Relation:
        relation = inputs[0]
        items = self.bound.state["items"]
        rows = [tuple(expr.eval(row) for _, expr in items) for row in relation.rows]
        return Relation(self.bound.output_schema, rows)


@dataclass
class HashJoinNode(PlanNode):
    left_keys: tuple[str, ...] = ()
    right_keys: tuple[str, ...] = ()
    tag: ClassVar[str] = "hashjoin"
    expected_arity: ClassVar[tuple[int, ...]] = (2,)

    def extra_dict(self) -> dict[str, Any]:
        return {"left_keys": list(self.left_keys), "right_keys": list(self.right_keys)}

    @classmethod
    def build(cls, node_id, inputs, args, where) -> "HashJoinNode":
        obj = parse_object(args, where, required=("left_keys", "right_keys"))
        left_keys = cls._key_list(obj["left_keys"], f"{where}.left_keys")
        right_keys = cls._key_list(obj["right_keys"], f"{where}.right_keys")
        return cls(id=node_id, inputs=list(inputs), left_keys=left_keys, right_keys=right_keys)

    @staticmethod
    def _key_list(data, where) -> tuple[str, ...]:
        if not isinstance(data, list) or not data:
            raise DocumentFormatError(f"{where}: expected a non-empty list of column names")
        return tuple(as_str(value, where) for value in data)

    def bind(self, ctx: BindContext) -> BoundNode:
        self.require_arity()
        where = f"node {self.id!r}"
        left_schema = ctx.schema_of(self.inputs[0])
        right_schema = ctx.schema_of(self.inputs[1])
        if len(self.left_keys) != len(self.right_keys):
            raise BindingError(
                f"{where}: join key count differs "
                f"({len(self.left_keys)} vs {len(self.right_keys)})"
            )
        left_indexes, right_indexes = [], []
        left_types, right_types = [], []
        for left_key, right_key in zip(self.left_keys, self.right_keys):
            left_index = left_schema.index_of(left_key)
            if left_index < 0:
                raise BindingError(f"{where}: unknown left join column {left_key!r}")
            right_index = right_schema.index_of(right_key)
            if right_index < 0:
                raise BindingError(f"{where}: unknown right join column {right_key!r}")
            left_type = left_schema.columns[left_index].data_type
            right_type = right_schema.columns[right_index].data_type
            if left_type != right_type:
                raise TypeMismatchError(
                    f"{where}: join keys {left_key!r}({left_type}) and "
                    f"{right_key!r}({right_type}) have different types"
                )
            left_indexes.append(left_index)
            right_indexes.append(right_index)
            left_types.append(left_type)
            right_types.append(right_type)

        kept_right = [
            index for index in range(len(right_schema.columns))
            if index not in set(right_indexes)
        ]
        output_columns = list(left_schema.columns) + [
            right_schema.columns[index] for index in kept_right
        ]
        names = [column.name for column in output_columns]
        if len(names) != len(set(names)):
            duplicates = sorted({name for name in names if names.count(name) > 1})
            raise BindingError(
                f"{where}: ambiguous output column(s) after join: {duplicates}"
            )
        schema = Schema(tuple(output_columns))
        return BoundNode(
            schema,
            {
                "left_indexes": tuple(left_indexes),
                "right_indexes": tuple(right_indexes),
                "kept_right": tuple(kept_right),
            },
        )

    def run(self, inputs: list[Relation], ctx) -> Relation:
        left_relation, right_relation = inputs
        left_indexes = self.bound.state["left_indexes"]
        right_indexes = self.bound.state["right_indexes"]
        kept_right = self.bound.state["kept_right"]
        buckets: dict[tuple, list[tuple]] = {}
        for row in right_relation.rows:
            key = tuple(row[index] for index in right_indexes)
            buckets.setdefault(key, []).append(row)
        rows: list[tuple] = []
        for left_row in left_relation.rows:
            key = tuple(left_row[index] for index in left_indexes)
            for right_row in buckets.get(key, ()):
                rows.append(
                    tuple(left_row)
                    + tuple(right_row[index] for index in kept_right)
                )
        return Relation(self.bound.output_schema, rows)


def _agg_result_type(func: str, value_type: str, where: str) -> str:
    if func == "count":
        return INT
    if value_type not in NUMERIC_TYPES:
        raise TypeMismatchError(
            f"{where}: aggregate {func!r} requires numeric input, got {value_type}"
        )
    if func in ("sum", "min", "max"):
        return value_type
    return FLOAT


@dataclass
class AggregateNode(PlanNode):
    group_keys: tuple[str, ...] = ()
    aggregates: tuple[dict, ...] = ()
    tag: ClassVar[str] = "aggregate"
    expected_arity: ClassVar[tuple[int, ...]] = (1,)

    def extra_dict(self) -> dict[str, Any]:
        return {
            "group_keys": list(self.group_keys),
            "aggregates": [
                {"func": item["func"], "alias": item["alias"], "expr": item["expr"]}
                for item in self.aggregates
            ],
        }

    @classmethod
    def build(cls, node_id, inputs, args, where) -> "AggregateNode":
        obj = parse_object(args, where, required=("aggregates",), optional=("group_keys",))
        groups_raw = obj.get("group_keys", [])
        if not isinstance(groups_raw, list):
            raise DocumentFormatError(f"{where}.group_keys: expected a list")
        group_keys = tuple(as_str(value, f"{where}.group_keys") for value in groups_raw)
        aggs_raw = obj["aggregates"]
        if not isinstance(aggs_raw, list):
            raise DocumentFormatError(f"{where}.aggregates: expected a list")
        aggregates = []
        for index, raw in enumerate(aggs_raw):
            item_where = f"{where}.aggregates[{index}]"
            item = parse_object(
                raw,
                item_where,
                required=("func", "alias", "expr"),
            )
            func = as_str(item["func"], f"{item_where}.func")
            if func not in AGG_FUNCS:
                raise DocumentFormatError(f"{item_where}.func: unknown aggregate {func!r}")
            alias = as_str(item["alias"], f"{item_where}.alias")
            expr_data = item["expr"]
            if expr_data == {"e": "star"} or expr_data == "*":
                if func != "count":
                    raise BindingError(f"{item_where}: only count supports '*'")
                expr_payload = {"e": "star"}
            else:
                expr_payload = expr_from_dict(expr_data, item_where).to_dict()
            aggregates.append({"func": func, "alias": alias, "expr": expr_payload})
        return cls(
            id=node_id,
            inputs=list(inputs),
            group_keys=group_keys,
            aggregates=tuple(aggregates),
        )

    def bind(self, ctx: BindContext) -> BoundNode:
        self.require_arity()
        where = f"node {self.id!r}"
        schema = ctx.schema_of(self.inputs[0])
        group_indexes, group_columns = [], []
        for key in self.group_keys:
            index = schema.index_of(key)
            if index < 0:
                raise BindingError(f"{where}: unknown group column {key!r}")
            group_indexes.append(index)
            group_columns.append(schema.columns[index])

        output_names = [column.name for column in group_columns]
        bound_aggs = []
        for item in self.aggregates:
            item_where = f"{where} aggregate {item['alias']!r}"
            if item["alias"] in output_names:
                raise TypeMismatchError(
                    f"{where}: output name {item['alias']!r} collides with a "
                    f"group key or another aggregate"
                )
            if item.get("expr") == {"e": "star"}:
                arg_type = INT
                bound_arg = None
            else:
                arg_expr = expr_from_dict(item["expr"], item_where)
                bound_arg = arg_expr.bind(schema, item_where)
                arg_type = bound_arg.output_type
            result_type = _agg_result_type(item["func"], arg_type, item_where)
            output_names.append(item["alias"])
            bound_aggs.append(
                {
                    "func": item["func"],
                    "alias": item["alias"],
                    "arg": bound_arg,
                    "type": result_type,
                }
            )
        output_schema = Schema(
            tuple(group_columns)
            + tuple(Column(item["alias"], item["type"]) for item in bound_aggs)
        )
        return BoundNode(
            output_schema,
            {"group_indexes": tuple(group_indexes), "aggregates": tuple(bound_aggs)},
        )

    def run(self, inputs: list[Relation], ctx) -> Relation:
        relation = inputs[0]
        group_indexes = self.bound.state["group_indexes"]
        aggregates = self.bound.state["aggregates"]
        groups: dict[tuple, list[tuple]] = {}
        for row in relation.rows:
            key = tuple(row[index] for index in group_indexes)
            groups.setdefault(key, []).append(row)
        ordered_keys = sorted(
            groups,
            key=lambda key: tuple(_sort_value(value) for value in key),
        )
        rows: list[tuple] = []
        for key in ordered_keys:
            rows.append(tuple(key) + tuple(_evaluate_aggregate(item, groups[key]) for item in aggregates))
        return Relation(self.bound.output_schema, rows)


def _sort_value(value):
    # NULL sorts first; other values sort by (type name, value).
    if value is NULL:
        return (0, 0)
    return (1, type(value).__name__, value)


def _evaluate_aggregate(item: dict, rows: list[tuple]):
    func = item["func"]
    bound_arg = item["arg"]
    if func == "count" and bound_arg is None:
        return len(rows)
    values = [bound_arg.eval(row) for row in rows]
    non_null = [value for value in values if value is not NULL]
    if func == "count":
        return len(non_null)
    if not non_null:
        return NULL
    if func == "sum":
        return sum(non_null)
    if func == "avg":
        total = sum(non_null)
        return total / len(non_null)
    if func == "min":
        return min(non_null)
    return max(non_null)


@dataclass
class UnionAllNode(PlanNode):
    tag: ClassVar[str] = "unionall"
    expected_arity: ClassVar[tuple[int, ...]] = (2,)

    def extra_dict(self) -> dict[str, Any]:
        return {}

    @classmethod
    def build(cls, node_id, inputs, args, where) -> "UnionAllNode":
        if args:
            raise DocumentFormatError(f"{where}: unionall takes no arguments")
        return cls(id=node_id, inputs=list(inputs))

    def bind(self, ctx: BindContext) -> BoundNode:
        self.require_arity()
        where = f"node {self.id!r}"
        left_schema = ctx.schema_of(self.inputs[0])
        right_schema = ctx.schema_of(self.inputs[1])
        if len(left_schema) != len(right_schema):
            raise TypeMismatchError(
                f"{where}: union branches have different column counts "
                f"({len(left_schema)} vs {len(right_schema)})"
            )
        for index, (left_col, right_col) in enumerate(zip(left_schema.columns, right_schema.columns)):
            if left_col.data_type != right_col.data_type:
                raise TypeMismatchError(
                    f"{where}: column {index} types differ: "
                    f"{left_col.data_type} vs {right_col.data_type}"
                )
            if left_col.name != right_col.name:
                raise TypeMismatchError(
                    f"{where}: column {index} names differ: "
                    f"{left_col.name!r} vs {right_col.name!r}"
                )
        return BoundNode(left_schema)

    def run(self, inputs: list[Relation], ctx) -> Relation:
        left_relation, right_relation = inputs
        return Relation(
            self.bound.output_schema,
            list(left_relation.rows) + list(right_relation.rows),
        )


NODE_REGISTRY: dict[str, type[PlanNode]] = {}


def register_node(cls: type[PlanNode]) -> type[PlanNode]:
    NODE_REGISTRY[cls.tag] = cls
    return cls


for _node_cls in (
    ScanNode,
    FilterNode,
    ProjectNode,
    HashJoinNode,
    AggregateNode,
    UnionAllNode,
):
    register_node(_node_cls)
