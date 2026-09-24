"""宽表透视引擎：键列 + 类别列 -> 稳定列序的宽表。

特性：
- 类别集合可增长，输出列按“首次出现”顺序追加，列序稳定；
- 同键同类别冲突按指定聚合（SUM）合并，支持撤回（ADD / RETRACT）；
- 每次输出模式扩张产生 SchemaChanged 事件，旧批次视图可对齐新模式（补 NULL）；
- 输入批次自带列类型并做严格校验；
- SQL 风格 NULL（None）与 NaN（float('nan')）是两种不同的值；
- INTEGER 用整数精确求和，FLOAT 用 math.fsum 求和；
- 仅依赖标准库，不调用任何现成查询引擎。
"""

from __future__ import annotations

import math
from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Callable, Mapping, Sequence

# SQL NULL：用 None 表示；NaN 用 float("nan") 表示，二者语义不同。
NULL: None = None
NAN: float = float("nan")

__all__ = [
    "NULL",
    "NAN",
    "DataType",
    "Agg",
    "Op",
    "Column",
    "Schema",
    "Row",
    "Batch",
    "SchemaChanged",
    "WideView",
    "BatchResult",
    "PivotEngine",
]


class DataType(Enum):
    INTEGER = "INTEGER"
    FLOAT = "FLOAT"
    STRING = "STRING"
    BOOLEAN = "BOOLEAN"


class Agg(Enum):
    SUM = "SUM"


class Op(Enum):
    ADD = "ADD"
    RETRACT = "RETRACT"


@dataclass(frozen=True)
class Column:
    """带类型的列描述。"""

    name: str
    type: DataType


@dataclass(frozen=True)
class Schema:
    """逻辑模式：若干键列 + 一个类别列 + 一个值列。"""

    key_columns: tuple[str, ...]
    category_column: str
    value_column: str
    value_type: DataType
    agg: Agg = Agg.SUM

    def __post_init__(self) -> None:
        if not self.key_columns:
            raise ValueError("至少需要一个键列")
        names = [*self.key_columns, self.category_column, self.value_column]
        if len(set(names)) != len(names):
            raise ValueError(f"列名重复: {names}")
        if self.agg is Agg.SUM and self.value_type not in (DataType.INTEGER, DataType.FLOAT):
            raise ValueError(f"SUM 仅支持 INTEGER/FLOAT，得到 {self.value_type.name}")


@dataclass(frozen=True)
class Row:
    """一行输入；op 决定加入还是撤回。"""

    values: Mapping[str, Any]
    op: Op = Op.ADD

    @classmethod
    def add(cls, values: Mapping[str, Any]) -> "Row":
        return cls(dict(values), Op.ADD)

    @classmethod
    def retract(cls, values: Mapping[str, Any]) -> "Row":
        return cls(dict(values), Op.RETRACT)


@dataclass(frozen=True)
class Batch:
    """一次输入：自带列类型的一批行。"""

    columns: tuple[Column, ...]
    rows: tuple[Row, ...]

    @classmethod
    def of(cls, columns: Sequence[Column], rows: Sequence[Row | Mapping[str, Any]]) -> "Batch":
        norm_rows = tuple(r if isinstance(r, Row) else Row.add(r) for r in rows)
        return cls(tuple(columns), norm_rows)


@dataclass(frozen=True)
class SchemaChanged:
    """输出模式变更事件：类别列被追加时产生。"""

    version: int
    added_categories: tuple[Any, ...]
    categories: tuple[Any, ...]


def _same_value(a: Any, b: Any) -> bool:
    """多重集相等判定：NULL 只等于 NULL，NaN 与 NaN 相匹配。"""
    if a is None or b is None:
        return a is None and b is None
    if isinstance(a, float) and isinstance(b, float):
        if math.isnan(a) and math.isnan(b):
            return True
    return a == b


def _check_scalar(value: Any, dtype: DataType, what: str) -> None:
    if value is None:
        return
    if isinstance(value, bool):
        ok = dtype is DataType.BOOLEAN
    elif isinstance(value, int):
        ok = dtype is DataType.INTEGER
    elif isinstance(value, float):
        ok = dtype is DataType.FLOAT
    elif isinstance(value, str):
        ok = dtype is DataType.STRING
    else:
        ok = False
    if not ok:
        raise TypeError(f"{what} 的值 {value!r} 与声明类型 {dtype.name} 不匹配")


def _sum(values: list[Any], dtype: DataType) -> Any:
    """SUM 语义：忽略 NULL；不含非 NULL 值时为 NULL；出现 NaN 则结果为 NaN。

    INTEGER 使用内置整数求和（任意精度、精确）；FLOAT 使用 math.fsum。
    """
    non_null = [v for v in values if v is not None]
    if not non_null:
        return NULL
    if any(isinstance(v, float) and math.isnan(v) for v in non_null):
        return NAN
    if dtype is DataType.INTEGER:
        total = 0
        for v in non_null:
            total += v
        return total
    return math.fsum(non_null)


_AGGREGATORS: dict[Agg, Callable[[list[Any], DataType], Any]] = {Agg.SUM: _sum}


@dataclass(frozen=True)
class WideView:
    """某一模式版本下宽表的不可超值照。"""

    schema: Schema
    data_version: int
    schema_version: int
    categories: tuple[Any, ...]
    rows: tuple[tuple[tuple[Any, ...], tuple[Any, ...]], ...]
    _key_columns: tuple[Column, ...] = field(default=(), init=False, repr=False)
    _output_columns: tuple[Column, ...] = field(default=(), init=False, repr=False)

    @property
    def version(self) -> int:
        return self.schema_version

    def key_columns(self) -> tuple[Column, ...]:
        return self._key_columns

    def output_columns(self) -> tuple[Column, ...]:
        return self._output_columns

    def records(self) -> list[dict[str, Any]]:
        result = []
        for key, vals in self.rows:
            record = dict(zip(self.schema.key_columns, key))
            record.update(zip(self.categories, vals))
            result.append(record)
        return result

    def tuples(self) -> list[tuple[Any, ...]]:
        return [key + vals for key, vals in self.rows]

    def keys(self) -> list[tuple[Any, ...]]:
        return [key for key, _ in self.rows]

    def aligned_to(self, target: "WideView | Sequence[Any]") -> "WideView":
        """按目标模式对齐：新增类别列对旧数据补 NULL，列序保持稳定。"""
        if isinstance(target, WideView):
            target_cats = target.categories
            target_version = target.schema_version
        else:
            target_cats = tuple(target)
            target_version = self.schema_version
        if target_cats[: len(self.categories)] != self.categories:
            raise ValueError("目标类别序列必须以当前类别序列为前缀（类别只允许追加）")
        aligned_rows = []
        for key, vals in self.rows:
            by_cat = dict(zip(self.categories, vals))
            aligned_rows.append((key, tuple(by_cat.get(cat, NULL) for cat in target_cats)))
        cat_cols = tuple(Column(str(cat), self.schema.value_type) for cat in target_cats)
        view = WideView(
            schema=self.schema,
            data_version=self.data_version,
            schema_version=target_version,
            categories=target_cats,
            rows=tuple(aligned_rows),
        )
        object.__setattr__(view, "_key_columns", self._key_columns)
        object.__setattr__(view, "_output_columns", self._key_columns + cat_cols)
        return view


@dataclass(frozen=True)
class BatchResult:
    version_before: int
    version_after: int
    events: tuple[SchemaChanged, ...]
    view: WideView


class PivotEngine:
    """增量维护宽表的引擎。"""

    def __init__(self, schema: Schema, *, on_event: Callable[[SchemaChanged], None] | None = None) -> None:
        self._schema = schema
        self._on_event = on_event
        self._key_types: dict[str, DataType] = {}
        self._categories: list[Any] = []
        self._category_set: set[Any] = set()
        self._key_order: list[tuple[Any, ...]] = []
        self._key_present: set[tuple[Any, ...]] = set()
        self._cells: dict[tuple[tuple[Any, ...], Any], list[Any]] = {}
        self._events: list[SchemaChanged] = []
        self._snapshots: dict[int, WideView] = {}
        self._version = 0
        self._snapshots[0] = self._materialize(0, 0)

    @property
    def version(self) -> int:
        return self._version

    @property
    def events(self) -> tuple[SchemaChanged, ...]:
        return tuple(self._events)

    def apply_batch(self, batch: Batch) -> BatchResult:
        before = self._version
        self._validate_batch_columns(batch)
        col_type = {c.name: c.type for c in batch.columns}

        # 第一阶段：纯类型/结构校验，不修改状态。
        parsed: list[tuple[Op, tuple[Any, ...], Any, Any]] = []
        new_categories: list[Any] = []
        for row in batch.rows:
            if not isinstance(row, Row):
                raise TypeError("批次行必须是 Row 或 dict（请用 Batch.of 构造）")
            vals = row.values
            missing = [n for n in (*self._schema.key_columns, self._schema.category_column, self._schema.value_column)
                       if n not in vals]
            if missing:
                raise ValueError(f"行缺少列: {missing}; 行={dict(vals)}")
            key_parts = []
            for name in self._schema.key_columns:
                v = vals[name]
                _check_scalar(v, col_type[name], f"键列 {name}")
                if v is None:
                    raise ValueError(f"键列 {name} 不允许 NULL")
                key_parts.append(v)
            cat = vals[self._schema.category_column]
            _check_scalar(cat, col_type[self._schema.category_column], "类别列")
            if cat is None:
                raise ValueError("类别列不允许 NULL")
            value = vals[self._schema.value_column]
            _check_scalar(value, col_type[self._schema.value_column], "值列")
            key = tuple(key_parts)
            parsed.append((row.op, key, cat, value))
            if cat not in self._category_set and cat not in new_categories:
                new_categories.append(cat)

        # 撤回的存在性也在修改前校验，保证错误时不产生半批次状态。
        for op, key, cat, value in parsed:
            if op is Op.RETRACT:
                cell = self._cells.get((key, cat))
                if cell is None or not any(_same_value(v, value) for v in cell):
                    raise KeyError(f"无法撤回不存在的行: key={key!r}, 类别={cat!r}, 值={value!r}")

        # 第二阶段：应用变更。
        for op, key, cat, value in parsed:
            cell = self._cells.setdefault((key, cat), [])
            if op is Op.ADD:
                cell.append(value)
                if key not in self._key_present:
                    self._key_present.add(key)
                    self._key_order.append(key)
            else:
                for idx, existing in enumerate(cell):
                    if _same_value(existing, value):
                        del cell[idx]
                        break
                if not cell:
                    del self._cells[(key, cat)]
                    if not any((key, c) in self._cells for c in self._categories):
                        self._key_present.discard(key)
                        self._key_order.remove(key)

        events: list[SchemaChanged] = []
        if new_categories:
            for cat in new_categories:
                if cat in self._category_set:
                    continue
                if cat in self._schema.key_columns:
                    raise ValueError(f"类别值 {cat!r} 与键列同名，无法作为输出列")
                self._category_set.add(cat)
                self._categories.append(cat)
            self._version += 1
            event = SchemaChanged(
                version=self._version,
                added_categories=tuple(new_categories),
                categories=tuple(self._categories),
            )
            self._events.append(event)
            events.append(event)
            self._snapshots[self._version] = self._materialize(self._version, self._version)
            if self._on_event is not None:
                self._on_event(event)

        view = self.view()
        return BatchResult(before, self._version, tuple(events), view)

    def _validate_batch_columns(self, batch: Batch) -> None:
        names = [c.name for c in batch.columns]
        if len(names) != len(set(names)):
            raise ValueError(f"批次存在重复列名: {names}")
        by_name = {c.name: c.type for c in batch.columns}
        required = {
            *self._schema.key_columns,
            self._schema.category_column,
            self._schema.value_column,
        }
        if set(names) != required:
            raise ValueError(f"批次列集合 {set(names)} 与模式要求 {required} 不一致")
        if by_name[self._schema.category_column] is not DataType.STRING:
            raise ValueError("类别列必须为 STRING 类型")
        if by_name[self._schema.value_column] is not self._schema.value_type:
            raise ValueError(
                f"值列类型 {by_name[self._schema.value_column].name} "
                f"与模式 {self._schema.value_type.name} 不一致"
            )
        for name in self._schema.key_columns:
            dtype = by_name[name]
            old = self._key_types.setdefault(name, dtype)
            if old is not dtype:
                raise ValueError(f"键列 {name} 在不同批次中的类型不一致")

    def _materialize(self, data_version: int, schema_version: int) -> WideView:
        aggregate = _AGGREGATORS[self._schema.agg]
        rows: list[tuple[tuple[Any, ...], tuple[Any, ...]]] = []
        for key in self._key_order:
            values = []
            for cat in self._categories:
                cell = self._cells.get((key, cat))
                values.append(aggregate(cell, self._schema.value_type) if cell else NULL)
            rows.append((key, tuple(values)))
        key_cols = tuple(Column(n, self._key_types.get(n, DataType.STRING)) for n in self._schema.key_columns)
        cat_cols = tuple(Column(str(cat), self._schema.value_type) for cat in self._categories)
        view = WideView(
            schema=self._schema,
            data_version=data_version,
            schema_version=schema_version,
            categories=tuple(self._categories),
            rows=tuple(rows),
        )
        object.__setattr__(view, "_key_columns", key_cols)
        object.__setattr__(view, "_output_columns", key_cols + cat_cols)
        return view

    def view(self) -> WideView:
        return self._materialize(self._version, self._version)

    def snapshot(self, version: int) -> WideView:
        try:
            return self._snapshots[version]
        except KeyError:
            raise KeyError(f"未知模式版本: {version}（已知: {sorted(self._snapshots)}）") from None

    def align(self, view: WideView, version: int | None = None) -> WideView:
        return view.aligned_to(self.snapshot(self._version if version is None else version))
