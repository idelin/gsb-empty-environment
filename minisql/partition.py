"""分区方案：整数范围分区 + 有限枚举分区，NULL 键独立成区。

整数域上的关键技巧：把任意开闭边界归一化为闭区间 [lo, hi]
（开边界 lo -> lo+1，hi -> hi-1），这样边界判断变成精确的整数比较，
从机制上保证开闭边界不漏一行、不重一行。

RangeScheme 构造时校验所有区间首尾相接且覆盖整个整数线
（前一个 hi + 1 == 后一个 lo，首 lo 为 -inf，尾 hi 为 +inf），
即「覆盖性证明」：任何非 NULL 整数键恰好落入一个分区。
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Iterable, Mapping


@dataclass(frozen=True)
class Interval:
    """整数闭区间；lo=None 表示 -inf，hi=None 表示 +inf。"""

    lo: int | None
    hi: int | None

    @staticmethod
    def of(lo: int | None, lo_closed: bool, hi: int | None, hi_closed: bool) -> "Interval":
        """按原始开闭边界构造，归一化为整数闭区间。"""
        if lo is not None and not lo_closed:
            lo += 1
        if hi is not None and not hi_closed:
            hi -= 1
        return Interval(lo, hi)

    @property
    def is_empty(self) -> bool:
        return self.lo is not None and self.hi is not None and self.lo > self.hi

    def intersects(self, other: "Interval") -> bool:
        lo = self.lo if other.lo is None else other.lo if self.lo is None else max(self.lo, other.lo)
        hi = self.hi if other.hi is None else other.hi if self.hi is None else min(self.hi, other.hi)
        return lo is None or hi is None or lo <= hi

    def contains(self, value: int) -> bool:
        return (self.lo is None or self.lo <= value) and (self.hi is None or value <= self.hi)

    def __str__(self) -> str:
        lo = "-inf" if self.lo is None else str(self.lo)
        hi = "+inf" if self.hi is None else str(self.hi)
        return f"[{lo}, {hi}]"


# ---- 分区键约束（分析器据此推理） ----


@dataclass(frozen=True)
class KeyRange:
    interval: Interval


@dataclass(frozen=True)
class KeyEnum:
    values: frozenset


@dataclass(frozen=True)
class KeyEnumComplement:
    """catch-all 分区：键域 = 所有未显式列出的值（有限枚举的补集）。"""

    excluded: frozenset


@dataclass(frozen=True)
class KeyNull:
    """NULL 分区：键恒为 NULL。"""


class Partition:
    """一个物理分区：行以 list 存储，允许重复（多重集合语义）。"""

    def __init__(self, name: str, constraint: Any) -> None:
        self.name = name
        self.constraint = constraint
        self.rows: list[dict] = []

    def __repr__(self) -> str:
        return f"Partition({self.name}, rows={len(self.rows)})"


# ---- 分区方案 ----


class RangeScheme:
    """整数范围分区。spec: [(分区名, Interval), ...]，必须无缝覆盖整数线。"""

    def __init__(self, spec: Iterable[tuple[str, Interval]]) -> None:
        self.spec = list(spec)
        if not self.spec:
            raise ValueError("至少需要一个分区")
        for name, iv in self.spec:
            if iv.is_empty:
                raise ValueError(f"分区 {name} 的区间为空: {iv}")
        if self.spec[0][1].lo is not None or self.spec[-1][1].hi is not None:
            raise ValueError("范围分区必须覆盖 (-inf, +inf)")
        for (name_a, a), (name_b, b) in zip(self.spec, self.spec[1:]):
            if a.hi is None or b.lo is None or a.hi + 1 != b.lo:
                raise ValueError(f"分区 {name_a} 与 {name_b} 之间存在空隙或重叠: {a} -> {b}")

    def route(self, key: int) -> str:
        for name, iv in self.spec:
            if iv.contains(key):
                return name
        raise AssertionError("覆盖性校验保证不会到达这里")


class EnumScheme:
    """有限枚举分区。groups: {分区名: {值, ...}}；catch_all 接收未列出的非 NULL 值。"""

    def __init__(self, groups: Mapping[str, Iterable[Any]], catch_all: str | None = None) -> None:
        self.value_to_part: dict[Any, str] = {}
        self.groups: dict[str, frozenset] = {}
        for name, values in groups.items():
            vals = frozenset(values)
            if not vals:
                raise ValueError(f"枚举分区 {name} 不能为空")
            for v in vals:
                if v is None:
                    raise ValueError("NULL 不进入枚举分区，请使用 NULL 分区")
                if v in self.value_to_part:
                    raise ValueError(f"值 {v!r} 被分配到多个分区")
                self.value_to_part[v] = name
            self.groups[name] = vals
        self.catch_all = catch_all

    def route(self, key: Any) -> str:
        name = self.value_to_part.get(key)
        if name is not None:
            return name
        if self.catch_all is not None:
            return self.catch_all
        raise ValueError(f"值 {key!r} 不在枚举定义中，且未配置 catch_all")


class PartitionedTable:
    """合成关系表：本地内存存储，不调用任何 SQL 引擎。

    - 行是 dict，允许完全重复的行（多重集合/bag 语义）；
    - NULL 用 None 表示，键为 NULL 的行进入独立的 NULL 分区。
    """

    NULL_PARTITION = "p_null"

    def __init__(self, name: str, columns: Iterable[str], key_column: str, scheme: Any) -> None:
        self.name = name
        self.columns = list(columns)
        self.key_column = key_column
        if key_column not in self.columns:
            raise ValueError(f"分区键 {key_column} 不在列定义中")
        self.scheme = scheme
        self.partitions: list[Partition] = []
        self._by_name: dict[str, Partition] = {}
        if isinstance(scheme, RangeScheme):
            for part_name, iv in scheme.spec:
                self._add(Partition(part_name, KeyRange(iv)))
        elif isinstance(scheme, EnumScheme):
            for part_name, values in scheme.groups.items():
                self._add(Partition(part_name, KeyEnum(values)))
            if scheme.catch_all is not None:
                excluded = frozenset(scheme.value_to_part)
                self._add(Partition(scheme.catch_all, KeyEnumComplement(excluded)))
        else:
            raise TypeError(scheme)
        self._add(Partition(self.NULL_PARTITION, KeyNull()))

    def _add(self, part: Partition) -> None:
        self.partitions.append(part)
        self._by_name[part.name] = part

    def insert(self, row: Mapping[str, Any]) -> None:
        record = {c: row.get(c) for c in self.columns}
        key = record[self.key_column]
        if key is None:
            self._by_name[self.NULL_PARTITION].rows.append(record)
        else:
            self._by_name[self.scheme.route(key)].rows.append(record)

    @property
    def rows(self) -> list[dict]:
        """全表行（按分区顺序拼接，保留重复）。"""
        return [r for p in self.partitions for r in p.rows]
