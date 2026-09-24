"""dedup - 纯标准库的批量去重引擎。

功能概要
--------
* 表按"带列类型的批次"输入（schema + 行数据）。
* 按指定键列去重，支持 keep="first"（保留首条）/ keep="last"（保留末条）。
* 支持分多批追加（批次更新）；保留末条时，对同一键的更新严格按声明事件序
  （add_batch 调用顺序 + 批内行顺序）生效。
* null（None）在去重时视为相等；NaN 与 null 不同，NaN 与 NaN 视为相等。
* 整数精确计算（Python 任意精度 int），不经过浮点。
* 自建哈希索引：哈希仅用于分桶，相等性始终比较完整规范化键，
  因此即使所有键哈希相同（哈希碰撞）也能正确区分值。
* 键列列表为空时，全表视为同一组。
* 结果顺序由被保留行的原始全局序号决定。

不依赖任何现成查询引擎（无 sqlite3 / pandas 等）。
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from typing import Any, Callable, Hashable, Iterable, Iterator, Sequence

__all__ = ["Column", "DedupEngine", "HashIndex", "NULL", "NAN"]


class _Token:
    """规范化键中使用的单例标记（NULL / NAN）。"""

    __slots__ = ("name",)

    def __init__(self, name: str) -> None:
        self.name = name

    def __repr__(self) -> str:  # pragma: no cover - 仅调试用
        return self.name


NULL = _Token("NULL")  # SQL null：去重时彼此相等
NAN = _Token("NAN")    # 浮点 NaN：与 NULL 不同，与自身相等


# ---------------------------------------------------------------------------
# 列类型与规范化
# ---------------------------------------------------------------------------

def _canon_int(value: Any) -> Hashable:
    if value is None:
        return NULL
    if isinstance(value, bool):
        return int(value)
    if isinstance(value, int):
        return value  # 任意精度，精确计算
    if isinstance(value, float):
        if math.isnan(value):
            raise TypeError("NaN 不是合法的 int 列值")
        if value.is_integer():
            return int(value)
    raise TypeError(f"不是合法的 int 列值: {value!r}")


def _canon_float(value: Any) -> Hashable:
    if value is None:
        return NULL
    if isinstance(value, bool):
        raise TypeError(f"不是合法的 float 列值: {value!r}")
    if isinstance(value, (int, float)):
        number = float(value)
        return NAN if math.isnan(number) else number
    raise TypeError(f"不是合法的 float 列值: {value!r}")


def _canon_str(value: Any) -> Hashable:
    if value is None:
        return NULL
    if isinstance(value, str):
        return value
    raise TypeError(f"不是合法的 str 列值: {value!r}")


def _canon_bool(value: Any) -> Hashable:
    if value is None:
        return NULL
    if isinstance(value, bool):
        return value
    raise TypeError(f"不是合法的 bool 列值: {value!r}")


_CANONIZERS: dict[str, Callable[[Any], Hashable]] = {
    "int": _canon_int,
    "float": _canon_float,
    "str": _canon_str,
    "bool": _canon_bool,
}


@dataclass(frozen=True)
class Column:
    """列定义：名称 + 类型（int / float / str / bool）。"""

    name: str
    type: str

    def __post_init__(self) -> None:
        if self.type not in _CANONIZERS:
            raise ValueError(f"不支持的列类型: {self.type!r}")


# ---------------------------------------------------------------------------
# 哈希索引（碰撞安全）
# ---------------------------------------------------------------------------

class HashIndex:
    """hash -> 桶 的索引；桶内逐条比较完整键，哈希碰撞不影响正确性。"""

    def __init__(self, hash_fn: Callable[[Hashable], int] = hash) -> None:
        self._hash_fn = hash_fn
        self._buckets: dict[int, list[tuple[Hashable, int]]] = {}

    def get(self, key: Hashable, default: Any = None) -> Any:
        bucket = self._buckets.get(self._hash_fn(key))
        if bucket:
            for stored_key, value in bucket:
                if stored_key == key:
                    return value
        return default

    def put(self, key: Hashable, value: int) -> None:
        bucket = self._buckets.setdefault(self._hash_fn(key), [])
        for i, (stored_key, _) in enumerate(bucket):
            if stored_key == key:
                bucket[i] = (key, value)
                return
        bucket.append((key, value))

    def __len__(self) -> int:
        return sum(len(bucket) for bucket in self._buckets.values())


# ---------------------------------------------------------------------------
# 去重引擎
# ---------------------------------------------------------------------------

_KEEP_MODES = ("first", "last")


class DedupEngine:
    """按指定键列对分批输入的行去重。

    参数
    ----
    schema:      列定义序列（名称 + 类型），所有批次共用。
    key_columns: 参与去重的键列名；为空序列时全表视为同一组。
    keep:        "first" 保留首条 / "last" 保留末条。
    hash_fn:     可选的自定义哈希函数（测试可强制常量哈希以验证碰撞安全）。
    """

    def __init__(
        self,
        schema: Sequence[Column],
        key_columns: Sequence[str],
        keep: str = "first",
        hash_fn: Callable[[Hashable], int] = hash,
    ) -> None:
        if keep not in _KEEP_MODES:
            raise ValueError(f"keep 必须是 {_KEEP_MODES} 之一，得到 {keep!r}")
        self._schema = list(schema)
        names = [c.name for c in self._schema]
        if len(set(names)) != len(names):
            raise ValueError("列名重复")
        positions = []
        for key_name in key_columns:
            if key_name not in names:
                raise ValueError(f"未知键列: {key_name!r}")
            positions.append(names.index(key_name))
        self._key_positions = tuple(positions)
        self._canon = tuple(_CANONIZERS[c.type] for c in self._schema)
        self._keep = keep
        self._index = HashIndex(hash_fn)   # 规范化键 -> 当前保留行的全局序号
        self._rows: dict[int, tuple] = {}  # 全局序号 -> 保留行
        self._seq = 0                      # 全局事件序号（跨批次单调递增）

    # -- 输入 ---------------------------------------------------------------

    def add_batch(
        self,
        rows: Iterable[Sequence[Any]],
        schema: Sequence[Column] | None = None,
    ) -> int:
        """追加一批行，返回本批处理的事件数。

        批次按调用顺序、行按批内顺序构成声明事件序；keep="last" 时
        后到的同键事件覆盖旧记录（即"更新旧 ID 按声明事件序处理"）。
        """
        if schema is not None and list(schema) != self._schema:
            raise ValueError("批次 schema 与引擎声明不一致")
        count = 0
        for row in rows:
            row = tuple(row)
            if len(row) != len(self._schema):
                raise ValueError(
                    f"行列数 {len(row)} 与 schema 列数 {len(self._schema)} 不一致: {row!r}"
                )
            self._ingest(row)
            count += 1
        return count

    def _ingest(self, row: tuple) -> None:
        key = tuple(self._canon[i](row[i]) for i in self._key_positions)
        seq = self._seq
        self._seq += 1
        existing = self._index.get(key)
        if existing is None:
            self._index.put(key, seq)
            self._rows[seq] = row
        elif self._keep == "last":
            del self._rows[existing]
            self._index.put(key, seq)
            self._rows[seq] = row
        # keep == "first"：丢弃后到重复行

    # -- 输出 ---------------------------------------------------------------

    def result(self) -> list[tuple]:
        """去重后的行，按被保留行的原始全局序号升序。"""
        return [self._rows[seq] for seq in sorted(self._rows)]

    def items(self) -> list[tuple[int, tuple]]:
        """(全局序号, 行) 列表，便于调试与演示。"""
        return [(seq, self._rows[seq]) for seq in sorted(self._rows)]

    def __len__(self) -> int:
        return len(self._rows)


# ---------------------------------------------------------------------------
# 演示样例
# ---------------------------------------------------------------------------

def _demo() -> None:
    schema = [
        Column("id", "int"),
        Column("name", "str"),
        Column("score", "float"),
    ]

    print("== 样例 1：保留末条 + 批次更新（按声明事件序） ==")
    engine = DedupEngine(schema, key_columns=["id"], keep="last")
    engine.add_batch([
        (1, "alice", 90.0),
        (2, "bob", None),          # null 分数
        (3, "carol", float("nan"))  # NaN 分数
    ])
    engine.add_batch([
        (1, "alice", 95.5),        # 更新 id=1
        (2, "bobby", 80.0),        # 更新 id=2
    ])
    engine.add_batch([(1, "alice", 91.0)])  # 再次更新 id=1
    for seq, row in engine.items():
        print(f"  seq={seq}  row={row}")

    print("== 样例 2：null 视为相等，NaN 与 null 不同 ==")
    engine = DedupEngine(
        [Column("k", "float"), Column("v", "str")],
        key_columns=["k"],
        keep="first",
    )
    engine.add_batch([
        (None, "null-1"),
        (None, "null-2"),          # 与上一行同组，丢弃
        (float("nan"), "nan-1"),
        (float("nan"), "nan-2"),   # 与上一行同组，丢弃
        (1.0, "one"),
    ])
    for seq, row in engine.items():
        print(f"  seq={seq}  row={row}")

    print("== 样例 3：空键列 -> 全表同一组（保留首条） ==")
    engine = DedupEngine(schema, key_columns=[], keep="first")
    engine.add_batch([(1, "a", 1.0), (2, "b", 2.0), (3, "c", 3.0)])
    print(f"  {engine.result()}")

    print("== 样例 4：大整数精确去重 ==")
    engine = DedupEngine([Column("id", "int"), Column("v", "str")],
                         key_columns=["id"], keep="first")
    engine.add_batch([
        (2**63, "x"),
        (2**63 + 1, "y"),   # 与 2**63 不同，必须保留
        (2**63, "dup"),     # 重复，丢弃
    ])
    print(f"  {engine.result()}")


if __name__ == "__main__":
    _demo()
