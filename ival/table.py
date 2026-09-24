"""带列类型的列式批次表（不依赖 pyarrow，纯标准库实现）。

一个 :class:`Table` 由若干 :class:`RecordBatch` 组成；每个批次是
``列名 -> 等长 list`` 的列式存储，列类型由 :class:`Schema` 声明。

输入校验在批次追加时完成，所以后续连接算法可以信任列类型。
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Iterator

from .values import BOOL, FLOAT64, INT64, STRING, check_type


@dataclass(frozen=True)
class Field:
    name: str
    type: str


@dataclass(frozen=True)
class Schema:
    fields: tuple[Field, ...]

    @classmethod
    def from_pairs(cls, pairs: list[tuple[str, str]]) -> "Schema":
        names = [n for n, _ in pairs]
        if len(names) != len(set(names)):
            raise ValueError(f"列名重复: {names}")
        return cls(tuple(Field(n, t) for n, t in pairs))

    def type_of(self, name: str) -> str:
        for f in self.fields:
            if f.name == name:
                return f.type
        raise KeyError(f"列 {name!r} 不在 schema 中: {[f.name for f in self.fields]}")

    def index_of(self, name: str) -> int:
        for i, f in enumerate(self.fields):
            if f.name == name:
                return i
        raise KeyError(name)


class RecordBatch:
    """一个列式批次：列的顺序与 schema.fields 一致。"""

    def __init__(self, schema: Schema, columns: dict[str, list[Any]]):
        if set(columns) != {f.name for f in schema.fields}:
            raise ValueError("批次列集合必须与 schema 完全一致（不缺列、不多列）")
        length = len(next(iter(columns.values())))
        for name, col in columns.items():
            if not isinstance(col, list):
                raise TypeError(f"列 {name!r} 必须是 list")
            if len(col) != length:
                raise ValueError(f"列 {name!r} 长度 {len(col)} 与批次长度 {length} 不一致")
            typ = schema.type_of(name)
            for v in col:
                check_type(typ, v)
        self.schema = schema
        self.length = length
        self._cols = [columns[f.name] for f in schema.fields]

    def column_at(self, index: int) -> list[Any]:
        return self._cols[index]

    def column(self, name: str) -> list[Any]:
        return self._cols[self.schema.index_of(name)]


class Table:
    """由一个或多个批次组成的表，对外暴露全局行号。"""

    def __init__(self, schema: Schema, batches: list[RecordBatch] | None = None):
        self.schema = schema
        self.batches: list[RecordBatch] = []
        self._offsets: list[int] = []
        for b in batches or []:
            self.append_batch(b)

    def append_batch(self, batch: RecordBatch) -> None:
        if batch.schema != self.schema:
            raise ValueError("批次 schema 与表 schema 不一致")
        self._offsets.append(sum(b.length for b in self.batches))
        self.batches.append(batch)

    @property
    def num_rows(self) -> int:
        return sum(b.length for b in self.batches)

    def locate(self, row_id: int) -> tuple[int, int]:
        """全局行号 -> (批次下标, 批次内行号)。"""
        if not 0 <= row_id < self.num_rows:
            raise IndexError(row_id)
        import bisect

        bi = bisect.bisect_right(self._offsets, row_id) - 1
        return bi, row_id - self._offsets[bi]

    def value(self, column: str, row_id: int) -> Any:
        bi, ri = self.locate(row_id)
        return self.batches[bi].column(column)[ri]

    def rows(self) -> Iterator[tuple[int, dict[str, Any]]]:
        """按全局行号迭代 ``{列名: 值}``。"""
        for row_id in range(self.num_rows):
            yield row_id, {f.name: self.value(f.name, row_id) for f in self.schema.fields}

    def column_values(self, name: str) -> list[Any]:
        """跨批次拼出整列（全局行号即下标）。"""
        idx = self.schema.index_of(name)
        out: list[Any] = []
        for b in self.batches:
            out.extend(b.column_at(idx))
        return out


def table_from_rows(pairs: list[tuple[str, str]], rows: list[dict[str, Any]]) -> Table:
    """便利构造：行式数据 -> 单批次列式 Table。"""
    schema = Schema.from_pairs(pairs)
    cols = {name: [r.get(name) for r in rows] for name, _ in pairs}
    return Table(schema, [RecordBatch(schema, cols)])


__all__ = [
    "Field",
    "Schema",
    "RecordBatch",
    "Table",
    "table_from_rows",
    "INT64",
    "FLOAT64",
    "BOOL",
    "STRING",
]
