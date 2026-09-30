"""记录与数据集定义。仅用于合成数据。"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Record:
    """一条记录。

    rid:     记录唯一标识。
    subject: 主体标识。同一主体可有多条记录；
             等价类计数按“不同主体数”计（题面约定）。
    values:  准标识符取值元组，顺序与 hierarchies 列表一致；
             None 表示空值（单独类别）。
    """

    rid: str
    subject: str
    values: tuple


class Dataset:
    def __init__(self):
        self._records: dict[str, Record] = {}

    def add(self, record: Record) -> None:
        if record.rid in self._records:
            raise KeyError(f"记录 id 重复: {record.rid}")
        self._records[record.rid] = record

    def remove(self, rid: str) -> None:
        if rid not in self._records:
            raise KeyError(f"记录不存在: {rid}")
        del self._records[rid]

    @property
    def records(self) -> list[Record]:
        return list(self._records.values())

    def __len__(self) -> int:
        return len(self._records)
