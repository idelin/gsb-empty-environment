"""按键列去重引擎：保留首条/末条。

特性：
- 表按"带列类型的批次"输入（schema + 行列表），可多次 add_batch 增量更新。
- null 参与去重时按相等处理（null == null）；NaN 与 null 不同，NaN == NaN。
- 整数精确计算（Python int 任意精度，不经浮点）。
- 自研链式哈希表：即使所有键哈希相同（碰撞），仍按 == 区分值。
- 保留末条时，更新按声明事件序（批次到达序 + 批内行序）生效。
- 结果顺序由被保留行的原始全局序号决定。
- 空键列表示全表同一组。
"""

from __future__ import annotations

import math
from typing import Any, Callable, Hashable, Iterable, Iterator, Sequence

INT = "int"
FLOAT = "float"
STR = "str"
BOOL = "bool"
SUPPORTED_TYPES = (INT, FLOAT, STR, BOOL)

KEEP_FIRST = "first"
KEEP_LAST = "last"


class SchemaError(ValueError):
    """列定义非法或批次间 schema 不一致。"""


class RowTypeError(TypeError):
    """行数据与声明的列类型不符。"""


def _normalize(col_type: str, value: Any) -> tuple:
    """把单元格值规范化为可哈希、可比较的去重键元。

    - None            -> ("null",)          所有 null 互相相等
    - float NaN       -> ("nan",)           所有 NaN 互相相等，且与 null 不同
    - float -0.0      -> ("float", 0.0)     与 0.0 相等（IEEE 语义）
    - int             -> ("int", value)     任意精度，精确比较
    """
    if value is None:
        return ("null",)
    if col_type == INT:
        if isinstance(value, bool) or not isinstance(value, int):
            raise RowTypeError(f"int 列收到非 int 值: {value!r}")
        return ("int", value)
    if col_type == FLOAT:
        if isinstance(value, bool) or not isinstance(value, float):
            raise RowTypeError(f"float 列收到非 float 值: {value!r}")
        if math.isnan(value):
            return ("nan",)
        if value == 0.0:
            value = 0.0  # 归一化 -0.0
        return ("float", value)
    if col_type == STR:
        if not isinstance(value, str):
            raise RowTypeError(f"str 列收到非 str 值: {value!r}")
        return ("str", value)
    if col_type == BOOL:
        if not isinstance(value, bool):
            raise RowTypeError(f"bool 列收到非 bool 值: {value!r}")
        return ("bool", value)
    raise SchemaError(f"不支持的列类型: {col_type!r}")


class HashTable:
    """链式哈希表（哈希 -> [(key, value), ...]）。

    哈希只用于分桶；同桶内一律用 == 判定键是否相同，
    因此即使 hash_fn 对所有键返回同一值（强制碰撞），
    不同的键依然被区分开。
    """

    def __init__(self, hash_fn: Callable[[Hashable], int] | None = None):
        self._hash = hash_fn if hash_fn is not None else hash
        self._buckets: dict[int, list[tuple[Hashable, Any]]] = {}
        self._size = 0

    def get(self, key: Hashable, default: Any = None) -> Any:
        for k, v in self._buckets.get(self._hash(key), ()):
            if k == key:
                return v
        return default

    def set(self, key: Hashable, value: Any) -> None:
        bucket = self._buckets.setdefault(self._hash(key), [])
        for i, (k, _) in enumerate(bucket):
            if k == key:
                bucket[i] = (k, value)  # 保留首次插入的键对象
                return
        bucket.append((key, value))
        self._size += 1

    def __contains__(self, key: Hashable) -> bool:
        for k, _ in self._buckets.get(self._hash(key), ()):
            if k == key:
                return True
        return False

    def __len__(self) -> int:
        return self._size

    def values(self) -> Iterator[Any]:
        for bucket in self._buckets.values():
            for _, v in bucket:
                yield v


class DedupEngine:
    """跨批次按键列去重。

    key_columns: 参与去重的列名列表；空列表表示全表同一组。
    keep: "first" 保留首条，"last" 保留末条（末条按事件序：先批次序、后批内行序）。
    hash_fn: 可选自定义哈希函数（测试碰撞时传入 lambda k: 0）。
    """

    def __init__(
        self,
        key_columns: Sequence[str],
        keep: str = KEEP_FIRST,
        hash_fn: Callable[[Hashable], int] | None = None,
    ):
        if keep not in (KEEP_FIRST, KEEP_LAST):
            raise ValueError(f"keep 必须是 'first' 或 'last'，收到: {keep!r}")
        self._key_columns = list(key_columns)
        self._keep = keep
        self._table = HashTable(hash_fn)
        self._schema: tuple[tuple[str, str], ...] | None = None
        self._key_idx: tuple[int, ...] | None = None
        self._next_seq = 0  # 全局行序号，按事件序单调递增

    def _init_schema(self, columns: Sequence[tuple[str, str]]) -> None:
        names = [name for name, _ in columns]
        if len(set(names)) != len(names):
            raise SchemaError(f"列名重复: {names}")
        for _, col_type in columns:
            if col_type not in SUPPORTED_TYPES:
                raise SchemaError(f"不支持的列类型: {col_type!r}")
        unknown = [k for k in self._key_columns if k not in names]
        if unknown:
            raise SchemaError(f"键列不存在于表中: {unknown}")
        index = {name: i for i, name in enumerate(names)}
        self._schema = tuple(columns)
        self._key_idx = tuple(index[k] for k in self._key_columns)

    def add_batch(
        self,
        columns: Sequence[tuple[str, str]],
        rows: Iterable[Sequence[Any]],
    ) -> None:
        """追加一个批次。所有批次的 schema 必须一致。"""
        schema = tuple(columns)
        if self._schema is None:
            self._init_schema(schema)
        elif schema != self._schema:
            raise SchemaError(f"批次 schema 与首个批次不一致: {schema} != {self._schema}")

        assert self._schema is not None and self._key_idx is not None
        col_types = self._schema
        key_idx = self._key_idx
        for row in rows:
            row = tuple(row)
            if len(row) != len(col_types):
                raise RowTypeError(
                    f"行列数 {len(row)} 与列定义数 {len(col_types)} 不符: {row!r}"
                )
            key = tuple(_normalize(col_types[i][1], row[i]) for i in key_idx)
            seq = self._next_seq
            self._next_seq += 1
            if self._keep == KEEP_FIRST:
                if key not in self._table:
                    self._table.set(key, (seq, row))
            else:  # KEEP_LAST：后到事件覆盖同键旧值
                self._table.set(key, (seq, row))

    def result_entries(self) -> list[tuple[int, tuple]]:
        """返回 (原始全局序号, 行)，按序号升序——即结果顺序由保留行的原序号决定。"""
        return sorted(self._table.values(), key=lambda item: item[0])

    def result(self) -> list[tuple]:
        """去重后的行，顺序为保留行的原始序号升序。"""
        return [row for _, row in self.result_entries()]
