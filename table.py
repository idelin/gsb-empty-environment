"""带列类型的批次输入（纯标准库，不调用任何现成查询引擎）。

- 每个 Batch 携带列模式（名称 + 类型），逐行校验。
- None 表示 null，任何列都允许；NaN 只允许出现在 FLOAT 列，
  且与 null 是两种不同的取值，分别计数。
- INT 列只接受精确的 int（拒绝 bool 与 float），整数计算全程精确。
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from typing import Iterator, List, Sequence, Tuple

from histogram import FixedHistogram, Number


class ColumnType(Enum):
    INT = "int"
    FLOAT = "float"
    TEXT = "text"
    BOOL = "bool"


_CHECKS = {
    ColumnType.INT: lambda v: type(v) is int,
    ColumnType.FLOAT: lambda v: type(v) in (int, float),
    ColumnType.TEXT: lambda v: type(v) is str,
    ColumnType.BOOL: lambda v: type(v) is bool,
}


@dataclass(frozen=True)
class Column:
    name: str
    type: ColumnType


class Batch:
    """一批带列类型的行。"""

    def __init__(self, columns: Sequence[Column], rows: Sequence[Sequence[object]]):
        self.columns: Tuple[Column, ...] = tuple(columns)
        if not self.columns:
            raise ValueError("至少需要一列")
        names = [c.name for c in self.columns]
        if len(set(names)) != len(names):
            raise ValueError("列名必须唯一")
        self._index = {c.name: i for i, c in enumerate(self.columns)}
        self.rows: List[Tuple[object, ...]] = [self._check_row(r) for r in rows]

    def _check_row(self, row: Sequence[object]) -> Tuple[object, ...]:
        row = tuple(row)
        if len(row) != len(self.columns):
            raise ValueError(
                f"行列数 {len(row)} 与模式列数 {len(self.columns)} 不一致")
        for col, v in zip(self.columns, row):
            if v is None:
                continue  # null，任何列都允许
            if not _CHECKS[col.type](v):
                raise TypeError(
                    f"列 {col.name!r} 期望 {col.type.value}，"
                    f"得到 {type(v).__name__}: {v!r}")
        return row

    def __len__(self) -> int:
        return len(self.rows)

    def column_index(self, name: str) -> int:
        try:
            return self._index[name]
        except KeyError:
            raise KeyError(f"未知列: {name!r}") from None

    def iter_column(self, name: str) -> Iterator[object]:
        i = self.column_index(name)
        for row in self.rows:
            yield row[i]


def histogram_from_batch(batch: Batch, column: str,
                         boundaries: Sequence[Number]) -> FixedHistogram:
    """从批次的一个数值列构建定宽直方图；null 与 NaN 分别单独计数。"""
    col = batch.columns[batch.column_index(column)]
    if col.type not in (ColumnType.INT, ColumnType.FLOAT):
        raise TypeError(f"列 {column!r} 不是数值列（{col.type.value}）")
    h = FixedHistogram(boundaries)
    for v in batch.iter_column(column):
        h.add(v)  # None -> nulls，NaN -> nans，见 FixedHistogram.add
    return h
