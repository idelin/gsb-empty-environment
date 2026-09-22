"""Built-in operators: typed specs, binding, and bag-semantics execution.

No SQL engine is involved. Each operator spec has:

* ``TAG`` -- the serialized ``@type`` tag;
* ``ARITY`` -- accepted number of input nodes (int or tuple of ints);
* ``bind(...)`` -- resolved at validation time: it computes the output schema
  and returns an executable ``BoundOp`` after full type checking.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from plan_dag.catalog import Catalog
from plan_dag.datatypes import (
    BOOLEAN,
    DOUBLE,
    INTEGER,
    NULL_TYPE,
    DataType,
    Field,
    Schema,
    is_numeric,
)
from plan_dag.errors import (
    AmbiguousColumnError,
    BindingError,
    InvalidDocumentError,
    PlanExecutionError,
)
from plan_dag.expr import BoundExpr, Expr, expr_from_data
from plan_dag.runtime import BoundNode, BoundOp, Relation
from plan_dag.tags import (
    SPEC_REGISTRY,
    check_type,
    require_keys,
    require_object,
    require_tag,
)


class ColumnResolver:
    """Resolves unqualified column names against ordered input schemas."""

    def __init__(self, children: list[BoundNode], node_id: str):
        self.children = children
        self.node_id = node_id

    def resolve(self, name: str) -> tuple[int, DataType]:
        hits: list[tuple[int, DataType]] = []
        offset = 0
        for child in self.children:
            for f in child.schema:
                if f.name == name:
                    hits.append((offset + child.schema.index_of(name), f.type))
            offset += len(child.schema)
        if not hits:
            available = [f.name for c in self.children for f in c.schema]
            raise BindingError(
                f"node {self.node_id!r}: unknown column {name!r}; have {available}"
            )
        if len(hits) > 1:
            raise AmbiguousColumnError(
                f"node {self.node_id!r}: column {name!r} is ambiguous "
                f"({len(hits)} matches across inputs)"
            )
        return hits[0]


class OperatorSpec:
    TAG: str = ""
    ARITY: int | tuple[int, ...] = 0
    DATA_KEYS: set[str] = set()

    def bind(self, children: list[BoundNode], catalog: Catalog, node_id: str) -> BoundOp:
        raise NotImplementedError

    def to_data(self) -> dict:
        raise NotImplementedError

    @classmethod
    def from_data(cls, data: dict) -> "OperatorSpec":
        raise NotImplementedError

    def _check_arity(self, n: int, node_id: str) -> None:
        allowed = self.ARITY if isinstance(self.ARITY, tuple) else (self.ARITY,)
        if n not in allowed:
            raise InvalidDocumentError(
                f"node {node_id!r} ({self.TAG}) expects {sorted(allowed)} inputs, got {n}"
            )


@dataclass
class BoundScan(BoundOp):
    table_name: str
    schema: Schema

    def execute(self, inputs, context):
        context.scan_count += 1
        table = context.catalog.get(self.table_name)
        return [list(r) for r in table.rows]


@SPEC_REGISTRY.register
class ScanSpec(OperatorSpec):
    TAG = "scan"
    ARITY = 0
    DATA_KEYS = {"@type", "table"}

    def __init__(self, table: str):
        self.table = table

    def bind(self, children, catalog, node_id):
        self._check_arity(len(children), node_id)
        table = catalog.get(self.table)
        return BoundScan(table_name=self.table, schema=table.schema)

    def to_data(self):
        return {"@type": self.TAG, "table": self.table}

    @classmethod
    def from_data(cls, data):
        check_type(data.get("table"), str, "scan.table")
        return cls(data["table"])


@dataclass
class BoundFilter(BoundOp):
    predicate: BoundExpr
    schema: Schema

    def execute(self, inputs, context):
        rows = inputs[0]
        out: Relation = []
        for row in rows:
            value = self.predicate.eval(row)
            if value is None:
                continue
            if not isinstance(value, bool):
                raise PlanExecutionError("filter predicate is not BOOLEAN")
            if value:
                out.append(list(row))
        return out


@SPEC_REGISTRY.register
class FilterSpec(OperatorSpec):
    TAG = "filter"
    ARITY = 1
    DATA_KEYS = {"@type", "predicate"}

    def __init__(self, predicate: Expr):
        self.predicate = predicate

    def bind(self, children, catalog, node_id):
        self._check_arity(len(children), node_id)
        resolver = ColumnResolver(children, node_id)
        bound = self.predicate.bind(resolver)
        if bound.type != BOOLEAN and bound.type != NULL_TYPE:
            raise BindingError(
                f"node {node_id!r}: filter predicate must be BOOLEAN, got {bound.type}"
            )
        return BoundFilter(predicate=bound, schema=children[0].schema)

    def to_data(self):
        return {"@type": self.TAG, "predicate": self.predicate.to_data()}

    @classmethod
    def from_data(cls, data):
        return cls(expr_from_data(data["predicate"]))


@dataclass
class ProjectItem:
    name: str
    expr: Expr


@dataclass
class BoundProject(BoundOp):
    items: list[tuple[str, BoundExpr]]
    schema: Schema

    def execute(self, inputs, context):
        rows = inputs[0]
        out: Relation = []
        for row in rows:
            out.append([b.eval(row) for _, b in self.items])
        return out


def _project_items_from_data(data: dict) -> list[ProjectItem]:
    raw = data.get("items")
    if not isinstance(raw, list) or not raw:
        raise InvalidDocumentError("project 'items' must be a non-empty list")
    items: list[ProjectItem] = []
    for it in raw:
        it = require_object(it, "project item")
        require_keys(it, {"name", "expr"}, {"name", "expr"}, "project item")
        check_type(it.get("name"), str, "project item.name")
        items.append(ProjectItem(name=it["name"], expr=expr_from_data(it["expr"])))
    return items


@SPEC_REGISTRY.register
class ProjectSpec(OperatorSpec):
    TAG = "project"
    ARITY = 1
    DATA_KEYS = {"@type", "items"}

    def __init__(self, items: list[ProjectItem]):
        self.items = items

    def bind(self, children, catalog, node_id):
        self._check_arity(len(children), node_id)
        resolver = ColumnResolver(children, node_id)
        bound_items: list[tuple[str, BoundExpr]] = []
        fields: list[Field] = []
        for item in self.items:
            b = item.expr.bind(resolver)
            bound_items.append((item.name, b))
            fields.append(Field(item.name, b.type))
        return BoundProject(items=bound_items, schema=Schema(fields))

    def to_data(self):
        return {
            "@type": self.TAG,
            "items": [
                {"name": name, "expr": e.to_data()} for name, e in
                ((i.name, i.expr) for i in self.items)
            ],
        }

    @classmethod
    def from_data(cls, data):
        return cls(_project_items_from_data(data))


@dataclass
class BoundJoin(BoundOp):
    left_keys: tuple[tuple[int, int], ...]
    left_len: int
    left_outer: bool
    right_keep_idx: tuple[int, ...]
    schema: Schema

    def execute(self, inputs, context):
        left_rows, right_rows = inputs
        keep = self.right_keep_idx
        n_kept = len(keep)
        out: Relation = []
        for lrow in left_rows:
            matched = False
            for rrow in right_rows:
                ok = True
                for li, ri in self.left_keys:
                    lv = lrow[li]
                    rv = rrow[ri]
                    if lv is None or rv is None or lv != rv:
                        ok = False
                        break
                if ok:
                    matched = True
                    out.append(list(lrow) + [rrow[i] for i in keep])
            if not matched and self.left_outer:
                out.append(list(lrow) + [None] * n_kept)
        return out


def _join_keys_from_data(data: dict) -> list[tuple[str, str]]:
    raw = data.get("left_keys"), data.get("right_keys")
    lk, rk = raw
    for label, value in (("left_keys", lk), ("right_keys", rk)):
        if not isinstance(value, list) or not value or not all(isinstance(x, str) for x in value):
            raise InvalidDocumentError(f"join.{label} must be a non-empty list of strings")
    if len(lk) != len(rk):
        raise InvalidDocumentError("join left_keys/right_keys must have equal length")
    return list(zip(lk, rk))


@SPEC_REGISTRY.register
class JoinSpec(OperatorSpec):
    TAG = "join"
    ARITY = 2
    DATA_KEYS = {"@type", "join_type", "left_keys", "right_keys"}

    def __init__(
        self,
        left_keys: list[str],
        right_keys: list[str],
        join_type: str = "inner",
    ):
        if join_type not in ("inner", "left_outer"):
            raise InvalidDocumentError("join.join_type must be 'inner' or 'left_outer'")
        self.left_keys = list(left_keys)
        self.right_keys = list(right_keys)
        self.join_type = join_type

    def bind(self, children, catalog, node_id):
        self._check_arity(len(children), node_id)
        left, right = children
        left_resolver = ColumnResolver([left], node_id)
        right_resolver = ColumnResolver([right], node_id)
        pairs: list[tuple[int, int]] = []
        used_left: set[int] = set()
        used_right: set[int] = set()
        for lk, rk in zip(self.left_keys, self.right_keys):
            li, lt = left_resolver.resolve(lk)
            rj, rt = right_resolver.resolve(rk)
            if li in used_left or rj in used_right:
                raise BindingError(
                    f"node {node_id!r}: join key column reused in the same key list"
                )
            used_left.add(li)
            used_right.add(rj)
            if is_numeric(lt) != is_numeric(rt) or lt != rt:
                raise BindingError(
                    f"node {node_id!r}: join keys {lk!r} ({lt}) and {rk!r} ({rt}) "
                    "have mismatched types"
                )
            pairs.append((li, rj))
        right_key_local = {r for _, r in pairs}
        out_fields = list(left.schema.fields)
        for j, f in enumerate(right.schema):
            if j in right_key_local:
                continue
            if any(f.name == lf.name for lf in left.schema.fields):
                raise BindingError(
                    f"node {node_id!r}: join output column {f.name!r} collides with a "
                    "left input column (equality keys are the only allowed overlap)"
                )
            out_fields.append(f)
        right_keep_idx = tuple(
            j for j in range(len(right.schema)) if j not in right_key_local
        )
        return BoundJoin(
            left_keys=tuple(pairs),
            left_len=len(left.schema),
            left_outer=self.join_type == "left_outer",
            right_keep_idx=right_keep_idx,
            schema=Schema(out_fields),
        )

    def to_data(self):
        return {
            "@type": self.TAG,
            "join_type": self.join_type,
            "left_keys": self.left_keys,
            "right_keys": self.right_keys,
        }

    @classmethod
    def from_data(cls, data):
        join_type = data.get("join_type", "inner")
        if join_type not in ("inner", "left_outer"):
            raise InvalidDocumentError("join.join_type must be 'inner' or 'left_outer'")
        keys = _join_keys_from_data(data)
        return cls(
            left_keys=[l for l, _ in keys],
            right_keys=[r for _, r in keys],
            join_type=join_type,
        )


AGG_FUNCS = {"count_star", "count", "sum", "avg", "min", "max"}


@dataclass
class AggItem:
    func: str
    arg: Expr | None
    alias: str


@dataclass
class BoundAgg(BoundOp):
    key_exprs: tuple[BoundExpr, ...]
    aggs: tuple[tuple[str, BoundExpr | None, int], ...]
    key_fields: list[Field]
    agg_fields: list[Field]
    schema: Schema

    def execute(self, inputs, context):
        rows = inputs[0]
        groups: dict[tuple, dict] = {}
        order: list[tuple] = []
        for row in rows:
            key = tuple(e.eval(row) for e in self.key_exprs)
            if key not in groups:
                groups[key] = {}
                order.append(key)
            for idx, (func, arg, _) in enumerate(self.aggs):
                slot = groups[key].setdefault(idx, _new_accumulator(func))
                value = arg.eval(row) if arg is not None else None
                _update_accumulator(slot, func, value)
        out: Relation = []
        for key in order:
            values = list(key)
            for idx, (func, _, _) in enumerate(self.aggs):
                values.append(_finish_accumulator(groups[key].get(idx), func))
            out.append(values)
        return out


def _new_accumulator(func: str) -> list[Any]:
    if func in ("count_star", "count"):
        return [0]
    if func in ("sum", "avg"):
        return [0, 0, False]
    return [None]


def _update_accumulator(slot: list[Any], func: str, value: Any) -> None:
    if func == "count_star":
        slot[0] += 1
    elif func == "count":
        if value is not None:
            slot[0] += 1
    elif func in ("sum", "avg"):
        if value is not None:
            slot[0] += value
            slot[1] += 1
            slot[2] = True
    elif func in ("min", "max"):
        if value is not None and (slot[0] is None or
                                  (value < slot[0] if func == "min" else value > slot[0])):
            slot[0] = value


def _finish_accumulator(slot: list[Any] | None, func: str) -> Any:
    if slot is None:
        if func in ("count_star", "count"):
            return 0
        if func in ("sum", "avg"):
            return None
        return None
    if func in ("count_star", "count"):
        return slot[0]
    if func == "sum":
        return slot[0] if slot[2] else None
    if func == "avg":
        return (slot[0] / slot[1]) if slot[1] else None
    return slot[0]


def _agg_items_from_data(data: dict) -> list[AggItem]:
    raw = data.get("aggs")
    if not isinstance(raw, list) or not raw:
        raise InvalidDocumentError("aggregate 'aggs' must be a non-empty list")
    items: list[AggItem] = []
    for it in raw:
        it = require_object(it, "aggregate item")
        require_keys(it, {"func", "alias"}, {"func", "arg", "alias"}, "aggregate item")
        func = it.get("func")
        if func not in AGG_FUNCS:
            raise InvalidDocumentError(f"aggregate func must be one of {sorted(AGG_FUNCS)}, got {func!r}")
        check_type(it.get("alias"), str, "aggregate item.alias")
        if func == "count_star":
            if "arg" in it:
                raise InvalidDocumentError("count_star takes no 'arg'")
            items.append(AggItem(func=func, arg=None, alias=it["alias"]))
        else:
            if "arg" not in it:
                raise InvalidDocumentError(f"aggregate {func} requires 'arg'")
            items.append(AggItem(func=func, arg=expr_from_data(it["arg"]), alias=it["alias"]))
    return items


@SPEC_REGISTRY.register
class AggregateSpec(OperatorSpec):
    TAG = "aggregate"
    ARITY = 1
    DATA_KEYS = {"@type", "group_keys", "aggs"}

    def __init__(self, aggs: list[AggItem], group_keys: list[Expr] | None = None):
        self.aggs = aggs
        self.group_keys = list(group_keys or [])

    def bind(self, children, catalog, node_id):
        self._check_arity(len(children), node_id)
        child = children[0]
        resolver = ColumnResolver(children, node_id)
        bound_keys = tuple(e.bind(resolver) for e in self.group_keys)
        agg_fields: list[Field] = []
        bound_aggs: list[tuple[str, BoundExpr | None, int]] = []
        aliases = set()
        for item in self.aggs:
            if item.alias in aliases:
                raise BindingError(f"node {node_id!r}: duplicate output alias {item.alias!r}")
            aliases.add(item.alias)
            bound_arg = item.arg.bind(resolver) if item.arg is not None else None
            bound_aggs.append((item.func, bound_arg, 0))
            agg_fields.append(Field(item.alias, _agg_result_type(node_id, item.func, bound_arg)))
        key_named_fields: list[Field] = []
        for i, (expr, b) in enumerate(zip(self.group_keys, bound_keys)):
            from plan_dag.expr import ColumnRef as _CR
            kname = expr.name if isinstance(expr, _CR) else f"key_{i}"
            if kname in {f.name for f in key_named_fields} or kname in aliases:
                raise BindingError(f"node {node_id!r}: duplicate output column {kname!r}")
            key_named_fields.append(Field(kname, b.type))
        schema = Schema(key_named_fields + agg_fields)
        return BoundAgg(
            key_exprs=bound_keys,
            aggs=tuple(bound_aggs),
            key_fields=key_named_fields,
            agg_fields=agg_fields,
            schema=schema,
        )

    def to_data(self):
        out = {
            "@type": self.TAG,
            "group_keys": [e.to_data() for e in self.group_keys],
            "aggs": [],
        }
        for item in self.aggs:
            entry: dict[str, Any] = {"func": item.func, "alias": item.alias}
            if item.arg is not None:
                entry["arg"] = item.arg.to_data()
            out["aggs"].append(entry)
        return out

    @classmethod
    def from_data(cls, data):
        gk = data.get("group_keys", [])
        if not isinstance(gk, list):
            raise InvalidDocumentError("aggregate.group_keys must be a list")
        keys = [expr_from_data(e) for e in gk]
        return cls(aggs=_agg_items_from_data(data), group_keys=keys)


def _agg_result_type(node_id: str, func: str, arg: BoundExpr | None) -> DataType:
    if func in ("count_star", "count"):
        return INTEGER
    assert arg is not None
    if func in ("sum", "avg"):
        if not is_numeric(arg.type) and arg.type != NULL_TYPE:
            raise BindingError(
                f"node {node_id!r}: {func} argument must be numeric, got {arg.type}"
            )
        return DOUBLE if func == "avg" else (arg.type if arg.type != NULL_TYPE else INTEGER)
    if func in ("min", "max"):
        return arg.type
    raise BindingError(f"unknown aggregate {func!r}")


@dataclass
class BoundLimit(BoundOp):
    count: int
    schema: Schema

    def execute(self, inputs, context):
        return [list(r) for r in inputs[0][: self.count]]


@SPEC_REGISTRY.register
class LimitSpec(OperatorSpec):
    TAG = "limit"
    ARITY = 1
    DATA_KEYS = {"@type", "count"}

    def __init__(self, count: int):
        self.count = count

    def bind(self, children, catalog, node_id):
        self._check_arity(len(children), node_id)
        if not isinstance(self.count, int) or isinstance(self.count, bool) or self.count < 0:
            raise InvalidDocumentError("limit.count must be a non-negative integer")
        return BoundLimit(count=self.count, schema=children[0].schema)

    def to_data(self):
        return {"@type": self.TAG, "count": self.count}

    @classmethod
    def from_data(cls, data):
        count = data.get("count")
        if isinstance(count, bool) or not isinstance(count, int) or count < 0:
            raise InvalidDocumentError("limit.count must be a non-negative integer")
        return cls(count)


def spec_from_data(data: object) -> OperatorSpec:
    """Deserialize one operator spec through the operator allow-list."""
    obj = require_object(data, "operator")
    tag, cls = require_tag(obj, SPEC_REGISTRY, "operator")
    require_keys(obj, {"@type"}, cls.DATA_KEYS, f"operator {tag!r}")
    return cls.from_data(obj)
