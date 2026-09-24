"""MVCC 列存引擎（纯标准库实现）。

设计要点：
- 每次批次提交产生一个 DeltaLayer，按 列 -> 行ID -> 值/墓碑 组织。
- 快照读固定一个版本号，只取 <= 该版本的最新一层值。
- 压缩水位 = 所有活跃快照的最小版本（无快照时为当前版本）；
  水位以下的层合并为一个基底层，每 (列, 行ID) 只保留 <= 水位的最新条目，
  因此旧快照永远看不到比其版本更新的值。
- 基底层中的墓碑可以安全丢弃（版本回收），因为底下已无任何更旧的值。
- null 用 None 表示，NaN 用 float('nan') 表示，两者严格区分。
- 整数使用 Python int，任意精度，计算精确。
"""

from __future__ import annotations

import math
from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Dict, Iterable, List, Optional, Tuple


class ColumnType(Enum):
    INT = "int"
    FLOAT = "float"
    TEXT = "text"
    BOOL = "bool"


#: 行删除标记（墓碑）。用独立单例对象，不会与任何合法值（含 None/NaN）混淆。
TOMBSTONE: Any = object()


def values_equal(a: Any, b: Any) -> bool:
    """区分 null 与 NaN 的值相等判断：None 只等于 None，NaN 等于 NaN。"""
    if a is None or b is None:
        return a is None and b is None
    if isinstance(a, float) and isinstance(b, float):
        if math.isnan(a) and math.isnan(b):
            return True
    return type(a) is type(b) and a == b


@dataclass(frozen=True)
class Batch:
    """一批带列类型的输入数据。每行必须含 "id" 键及 schema 中的全部列。"""

    schema: Dict[str, ColumnType]
    rows: Tuple[Dict[str, Any], ...]

    @staticmethod
    def of(schema: Dict[str, ColumnType], rows: Iterable[Dict[str, Any]]) -> "Batch":
        return Batch(schema=dict(schema), rows=tuple(rows))


@dataclass
class DeltaLayer:
    """一个版本对应的增量层：列名 -> {行ID: 值 或 TOMBSTONE}。"""

    version: int
    updates: Dict[str, Dict[int, Any]] = field(default_factory=dict)

    def entry_count(self) -> int:
        return sum(len(rows) for rows in self.updates.values())


class Snapshot:
    """活跃快照：固定读取版本，用 close() 或 with 语句释放。"""

    def __init__(self, store: "MvccStore", snapshot_id: int, version: int):
        self._store = store
        self._id = snapshot_id
        self.version = version
        self._closed = False

    def read(self, row_id: int, column: str) -> Tuple[bool, Any]:
        self._check_open()
        return self._store.read(row_id, column, self.version)

    def get(self, row_id: int, column: str) -> Any:
        found, value = self.read(row_id, column)
        if not found:
            raise KeyError(f"row {row_id} 在版本 {self.version} 不存在或已删除")
        return value

    def row_exists(self, row_id: int) -> bool:
        self._check_open()
        return self._store.row_exists(row_id, self.version)

    def scan(self, columns: Optional[List[str]] = None) -> List[Dict[str, Any]]:
        self._check_open()
        return self._store.scan(self.version, columns)

    def sum_column(self, column: str) -> Any:
        self._check_open()
        return self._store.sum_column(column, self.version)

    def close(self) -> None:
        if not self._closed:
            self._closed = True
            self._store._release_snapshot(self._id)

    def _check_open(self) -> None:
        if self._closed:
            raise RuntimeError("快照已释放")

    def __enter__(self) -> "Snapshot":
        return self

    def __exit__(self, *exc: Any) -> None:
        self.close()


class MvccStore:
    """按行 ID 组织增量层、支持快照读与水线压缩的列存表。"""

    def __init__(self, schema: Dict[str, ColumnType]):
        if not schema:
            raise ValueError("schema 不能为空")
        self.schema: Dict[str, ColumnType] = dict(schema)
        self.layers: List[DeltaLayer] = []  # 按版本升序
        self.current_version: int = 0
        self._snapshots: Dict[int, int] = {}  # snapshot_id -> 固定版本
        self._next_snapshot_id: int = 1

    # ---------- 写入 ----------

    def apply_batch(
        self, batch: Batch, deletes: Iterable[int] = ()
    ) -> int:
        """提交一个批次（可选附带整行删除的行ID集合），返回新版本号。"""
        if set(batch.schema) != set(self.schema):
            raise ValueError("批次 schema 与表 schema 不一致")
        for name, ctype in batch.schema.items():
            if self.schema[name] is not ctype:
                raise ValueError(f"列 {name} 类型不一致")

        updates: Dict[str, Dict[int, Any]] = {c: {} for c in self.schema}
        seen_ids: set[int] = set()
        for row in batch.rows:
            if set(row) != {"id", *self.schema}:
                raise ValueError(f"行键集合应为 id + {sorted(self.schema)}，得到 {sorted(row)}")
            row_id = row["id"]
            if isinstance(row_id, bool) or not isinstance(row_id, int):
                raise TypeError("行ID必须是 int")
            if row_id in seen_ids:
                raise ValueError(f"批次内行ID重复: {row_id}")
            seen_ids.add(row_id)
            for col in self.schema:
                self._check_value(col, row[col])
                updates[col][row_id] = row[col]

        delete_ids = set(deletes)
        overlap = delete_ids & seen_ids
        if overlap:
            raise ValueError(f"同一批次不能既写又删行: {sorted(overlap)}")
        for row_id in delete_ids:
            if isinstance(row_id, bool) or not isinstance(row_id, int):
                raise TypeError("删除的行ID必须是 int")
            for col in self.schema:
                updates[col][row_id] = TOMBSTONE

        version = self.current_version + 1
        self.layers.append(DeltaLayer(version=version, updates=updates))
        self.current_version = version
        return version

    def _check_value(self, column: str, value: Any) -> None:
        if value is None:  # null 对所有列类型合法，且与 NaN 不同
            return
        ctype = self.schema[column]
        if ctype is ColumnType.INT:
            if isinstance(value, bool) or not isinstance(value, int):
                raise TypeError(f"列 {column} 期望 int，得到 {type(value).__name__}")
        elif ctype is ColumnType.FLOAT:
            if isinstance(value, bool) or not isinstance(value, (int, float)):
                raise TypeError(f"列 {column} 期望 float，得到 {type(value).__name__}")
        elif ctype is ColumnType.TEXT:
            if not isinstance(value, str):
                raise TypeError(f"列 {column} 期望 str，得到 {type(value).__name__}")
        elif ctype is ColumnType.BOOL:
            if not isinstance(value, bool):
                raise TypeError(f"列 {column} 期望 bool，得到 {type(value).__name__}")

    # ---------- 快照 ----------

    def snapshot(self, version: Optional[int] = None) -> Snapshot:
        """开启快照；默认固定当前版本。版本不能超过当前版本。"""
        if version is None:
            version = self.current_version
        if version < 0 or version > self.current_version:
            raise ValueError(f"非法快照版本 {version}")
        sid = self._next_snapshot_id
        self._next_snapshot_id += 1
        self._snapshots[sid] = version
        return Snapshot(self, sid, version)

    def _release_snapshot(self, snapshot_id: int) -> None:
        self._snapshots.pop(snapshot_id, None)

    @property
    def watermark(self) -> int:
        """版本回收水位：活跃快照的最小版本；无快照时为当前版本。

        水位及以下只需保留每个键的最新条目，更旧的版本可以被回收。
        """
        if self._snapshots:
            return min(self._snapshots.values())
        return self.current_version

    # ---------- 读取 ----------

    def _resolve_version(self, version: Optional[int]) -> int:
        if version is None:
            return self.current_version
        if version < 0 or version > self.current_version:
            raise ValueError(f"非法读取版本 {version}")
        return version

    def read(
        self, row_id: int, column: str, version: Optional[int] = None
    ) -> Tuple[bool, Any]:
        """读取 (行ID, 列) 在指定版本的值。

        返回 (是否存在, 值)。行被删除或从未存在时返回 (False, None)。
        从新到旧扫描增量层，取第一个 version <= 读版本的条目。
        """
        if column not in self.schema:
            raise KeyError(f"未知列 {column}")
        v = self._resolve_version(version)
        for layer in reversed(self.layers):
            if layer.version > v:
                continue
            rows = layer.updates.get(column)
            if rows is not None and row_id in rows:
                value = rows[row_id]
                if value is TOMBSTONE:
                    return (False, None)
                return (True, value)
        return (False, None)

    def row_exists(self, row_id: int, version: Optional[int] = None) -> bool:
        first_col = next(iter(self.schema))
        found, _ = self.read(row_id, first_col, version)
        return found

    def scan(
        self,
        version: Optional[int] = None,
        columns: Optional[List[str]] = None,
    ) -> List[Dict[str, Any]]:
        """扫描指定版本下全部存活行，按行ID升序返回。"""
        v = self._resolve_version(version)
        cols = list(columns) if columns is not None else list(self.schema)
        for c in cols:
            if c not in self.schema:
                raise KeyError(f"未知列 {c}")
        row_ids: set[int] = set()
        for layer in self.layers:
            if layer.version > v:
                break
            for rows in layer.updates.values():
                row_ids.update(rows)
        result: List[Dict[str, Any]] = []
        for row_id in sorted(row_ids):
            if not self.row_exists(row_id, v):
                continue
            out: Dict[str, Any] = {"id": row_id}
            for c in cols:
                found, value = self.read(row_id, c, v)
                out[c] = value if found else None
            result.append(out)
        return result

    def sum_column(self, column: str, version: Optional[int] = None) -> Any:
        """列求和：INT 用 Python int 精确求和，FLOAT 用 math.fsum。"""
        ctype = self.schema.get(column)
        if ctype not in (ColumnType.INT, ColumnType.FLOAT):
            raise TypeError(f"列 {column} 不可求和")
        values = [
            row[column]
            for row in self.scan(version, [column])
            if row[column] is not None
        ]
        if ctype is ColumnType.INT:
            return sum(values)  # 任意精度，精确
        return math.fsum(values)

    # ---------- 压缩与版本回收 ----------

    def compact(self) -> Dict[str, int]:
        """把水位及以下的增量层合并为一个基底层。

        对每个 (列, 行ID) 只保留 <= 水位的最新条目，因此水位处的旧快照
        读到的值不变，也永远看不到水位之后的新值。基底层中的墓碑被丢弃
        （其下已无更旧值需要遮蔽），完成版本回收。
        """
        wm = self.watermark
        to_merge = [l for l in self.layers if l.version <= wm]
        if not to_merge:
            return {"watermark": wm, "merged_layers": 0,
                    "reclaimed_tombstones": 0, "layers_after": len(self.layers)}

        merged: Dict[str, Dict[int, Any]] = {c: {} for c in self.schema}
        for layer in to_merge:  # 旧 -> 新，后者覆盖前者
            for col, rows in layer.updates.items():
                merged[col].update(rows)

        reclaimed = 0
        for col in self.schema:
            rows = merged[col]
            dead = [rid for rid, val in rows.items() if val is TOMBSTONE]
            for rid in dead:
                del rows[rid]
                reclaimed += 1

        base = DeltaLayer(version=to_merge[-1].version, updates=merged)
        self.layers = [base] + [l for l in self.layers if l.version > wm]
        return {"watermark": wm, "merged_layers": len(to_merge),
                "reclaimed_tombstones": reclaimed, "layers_after": len(self.layers)}
