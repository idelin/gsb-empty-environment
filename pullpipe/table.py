"""本地（合成）关系表：内存表、惰性生成表、CSV 文件表。

不调用任何 SQL 引擎。表只负责按需「拉」出至多 N 行；
打开的文件句柄等资源挂在游标上，随游标 close 释放。
"""

from __future__ import annotations

import csv
import itertools
from dataclasses import dataclass
from typing import Callable, Iterator, Sequence

from .batch import Row


@dataclass(frozen=True, slots=True)
class Column:
    name: str


class Schema:
    __slots__ = ("columns", "_index")

    def __init__(self, columns: Sequence[str | Column]):
        cols = [Column(c) if isinstance(c, str) else c for c in columns]
        self.columns: tuple[Column, ...] = tuple(cols)
        self._index = {c.name: i for i, c in enumerate(cols)}

    @property
    def names(self) -> tuple[str, ...]:
        return tuple(c.name for c in self.columns)

    def index(self, name: str) -> int:
        try:
            return self._index[name]
        except KeyError:
            raise KeyError(f"column {name!r} not found; have {self.names}") from None

    def __len__(self) -> int:
        return len(self.columns)

    def __repr__(self) -> str:
        return f"Schema({self.names!r})"


class RowCursor:
    """行游标协议（鸭子类型）：read_rows(n) -> list，EOF 返回 []；close 幂等。"""

    def read_rows(self, max_rows: int) -> list[Row]:
        raise NotImplementedError

    def close(self) -> None:
        raise NotImplementedError


class ListCursor(RowCursor):
    __slots__ = ("_rows", "_pos", "closed")

    def __init__(self, rows: Sequence[Row]):
        self._rows = rows
        self._pos = 0
        self.closed = False

    def read_rows(self, max_rows: int) -> list[Row]:
        if self.closed:
            return []
        end = min(self._pos + max_rows, len(self._rows))
        part = list(self._rows[self._pos:end])
        self._pos = end
        return part

    def close(self) -> None:
        self.closed = True


class GeneratedCursor(RowCursor):
    """惰性生成游标：每次产出一行，绝不物化整张表。

    generator 在 close 时被显式 close()，其 try/finally 得以执行——
    这就是「百万行 limit 1 不完整扫描」时上游资源被释放的落点。
    """

    __slots__ = ("_gen", "closed", "rows_generated")

    def __init__(self, gen: Iterator[Row]):
        self._gen = gen
        self.closed = False
        self.rows_generated = 0

    def read_rows(self, max_rows: int) -> list[Row]:
        if self.closed:
            return []
        part: list[Row] = []
        for _ in range(max_rows):
            try:
                part.append(next(self._gen))
                self.rows_generated += 1
            except StopIteration:
                self.close()
                break
        return part

    def close(self) -> None:
        if self.closed:
            return
        self.closed = True
        self._gen.close()


class CsvCursor(RowCursor):
    """CSV 文件游标：文件句柄随行数释放；关闭幂等。"""

    __slots__ = ("_file", "_reader", "schema", "closed")

    def __init__(self, path: str, schema: Schema):
        self._file = open(path, "r", newline="", encoding="utf-8")
        self._reader = csv.reader(self._file)
        header = next(self._reader, [])
        if tuple(header) != schema.names:
            self._file.close()
            raise ValueError(f"CSV header {tuple(header)!r} does not match schema {schema.names!r}")
        self.schema = schema
        self.closed = False

    def read_rows(self, max_rows: int) -> list[Row]:
        if self.closed:
            return []
        part: list[Row] = []
        try:
            for _ in range(max_rows):
                raw = next(self._reader)
                part.append(tuple(raw))
        except StopIteration:
            self.close()  # 到底即释放文件句柄
        return part

    def close(self) -> None:
        if self.closed:
            return
        self.closed = True
        self._file.close()


class Table:
    schema: Schema

    def open(self) -> RowCursor:
        raise NotImplementedError


class ListTable(Table):
    def __init__(self, schema: Sequence[str] | Schema, rows: Sequence[Row]):
        self.schema = schema if isinstance(schema, Schema) else Schema(schema)
        if any(len(r) != len(self.schema) for r in rows):
            raise ValueError("row width does not match schema")
        self.rows = list(rows)

    def open(self) -> ListCursor:
        return ListCursor(self.rows)


class GeneratedTable(Table):
    """合成大表：``maker(n)`` 按行号 0..size-1 惰性产出第 n 行。"""

    def __init__(
        self,
        schema: Sequence[str] | Schema,
        size: int,
        maker: Callable[[int], Row],
    ):
        if size < 0:
            raise ValueError("size must be >= 0")
        self.schema = schema if isinstance(schema, Schema) else Schema(schema)
        self.size = size
        self._maker = maker
        self.last_cursor: GeneratedCursor | None = None

    def open(self) -> GeneratedCursor:
        table = self

        def generate() -> Iterator[Row]:
            for i in range(table.size):
                yield table._maker(i)

        cursor = GeneratedCursor(generate())
        self.last_cursor = cursor
        return cursor


class CsvTable(Table):
    def __init__(self, path: str, schema: Sequence[str] | Schema):
        self.path = path
        self.schema = schema if isinstance(schema, Schema) else Schema(schema)
        self.last_cursor: CsvCursor | None = None

    def open(self) -> CsvCursor:
        cursor = CsvCursor(self.path, self.schema)
        self.last_cursor = cursor
        return cursor
