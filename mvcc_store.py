"""MVCC 列式增量存储引擎（纯标准库实现，不依赖任何查询引擎）。

核心概念：
- DeltaLayer: 按行 ID 记录列更新的增量层，每层对应一个版本号。
- Snapshot:   快照读，按快照版本选择每行每列"<= 该版本的最新值"。
- Tombstone:  删除标记，删除只是写入一层墓碑，不物理抹除旧版本。
- Compaction: 按回收水位（最老活跃快照版本）合并增量层，
              水位以下折叠为基线层，水位以上原样保留，
              因此旧快照永远看不到比它新的值。
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from enum import Enum
from typing import Any, Dict, Iterable, List, Optional


class ColumnType(Enum):
    INT = "int"
    FLOAT = "float"
    STRING = "string"
    BOOL = "bool"


class _Tombstone:
    """行删除标记（墓碑）。单例，便于身份比较。"""

    _instance: Optional["_Tombstone"] = None

    def __new__(cls) -> "_Tombstone":
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:  # pragma: no cover - 仅调试用
        return "TOMBSTONE"


TOMBSTONE = _Tombstone()


@dataclass(frozen=True)
class ColumnSpec:
    name: str
    type: ColumnType


def _check_value(spec: ColumnSpec, value: Any) -> None:
    """按列类型校验值；None 表示 SQL 风格的 null，任何类型都可为 null。"""
    if value is None:
        return
    if spec.type is ColumnType.INT:
        # bool 是 int 的子类，必须显式排除；且拒绝 float，保证整数精确。
        if isinstance(value, bool) or not isinstance(value, int):
            raise TypeError(f"列 {spec.name} 要求精确 int，得到 {value!r}")
    elif spec.type is ColumnType.FLOAT:
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            raise TypeError(f"列 {spec.name} 要求 float，得到 {value!r}")
    elif spec.type is ColumnType.STRING:
        if not isinstance(value, str):
            raise TypeError(f"列 {spec.name} 要求 str，得到 {value!r}")
    elif spec.type is ColumnType.BOOL:
        if not isinstance(value, bool):
            raise TypeError(f"列 {spec.name} 要求 bool，得到 {value!r}")


@dataclass
class Batch:
    """一批带列类型的输入数据。每行是 {列名: 值}，必须含 "_row_id"。"""

    columns: List[ColumnSpec]
    rows: List[Dict[str, Any]]

    def validate(self) -> None:
        col_names = {c.name for c in self.columns}
        specs = {c.name: c for c in self.columns}
        for row in self.rows:
            if "_row_id" not in row:
                raise ValueError("每行必须包含 _row_id")
            for name, value in row.items():
                if name == "_row_id":
                    continue
                if name not in col_names:
                    raise ValueError(f"未知列: {name}")
                _check_value(specs[name], value)


class DeltaLayer:
    """一个版本的增量：updates[row_id][col] = value，deletes = 被删行集合。"""

    __slots__ = ("version", "updates", "deletes")

    def __init__(self, version: int) -> None:
        self.version = version
        self.updates: Dict[Any, Dict[str, Any]] = {}
        self.deletes: set = set()

    def is_empty(self) -> bool:
        return not self.updates and not self.deletes


class Snapshot:
    """活跃快照。持有期间会抬高回收水位，阻止其可见版本被压缩掉。"""

    def __init__(self, store: "MVCCStore", version: int) -> None:
        self._store = store
        self.version = version
        self._closed = False

    def scan(self) -> Dict[Any, Dict[str, Any]]:
        return self._store.scan_at(self.version)

    def get(self, row_id: Any) -> Optional[Dict[str, Any]]:
        return self._store.get_at(row_id, self.version)

    def close(self) -> None:
        if not self._closed:
            self._store._release_snapshot(self)
            self._closed = True

    def __enter__(self) -> "Snapshot":
        return self

    def __exit__(self, *exc: Any) -> None:
        self.close()


class MVCCStore:
    def __init__(self, schema: Iterable[ColumnSpec]) -> None:
        self.schema: Dict[str, ColumnSpec] = {c.name: c for c in schema}
        self.layers: List[DeltaLayer] = []  # 按版本升序
        self.current_version = 0
        self._snapshots: List[Snapshot] = []

    # ---------- 写入 ----------

    def apply_batch(self, batch: Batch) -> int:
        """把一批列更新写成一个新版本的增量层，返回版本号。"""
        batch.validate()
        for name in (c.name for c in batch.columns):
            if name not in self.schema:
                raise ValueError(f"列 {name} 不在表 schema 中")
        self.current_version += 1
        layer = DeltaLayer(self.current_version)
        for row in batch.rows:
            rid = row["_row_id"]
            cols = {k: v for k, v in row.items() if k != "_row_id"}
            if cols:
                layer.updates.setdefault(rid, {}).update(cols)
        self.layers.append(layer)
        return self.current_version

    def delete_rows(self, row_ids: Iterable[Any]) -> int:
        """删除行：写入一层墓碑，而不是物理删除旧数据。"""
        self.current_version += 1
        layer = DeltaLayer(self.current_version)
        layer.deletes.update(row_ids)
        self.layers.append(layer)
        return self.current_version

    # ---------- 快照读 ----------

    def open_snapshot(self) -> Snapshot:
        snap = Snapshot(self, self.current_version)
        self._snapshots.append(snap)
        return snap

    def _release_snapshot(self, snap: Snapshot) -> None:
        self._snapshots.remove(snap)

    def scan_at(self, version: int) -> Dict[Any, Dict[str, Any]]:
        """重建 version 时刻的全表状态：逐层应用 <= version 的增量。"""
        state: Dict[Any, Dict[str, Any]] = {}
        for layer in self.layers:
            if layer.version > version:
                break  # 层按版本升序，之后的层对本快照不可见
            for rid in layer.deletes:
                state.pop(rid, None)
            for rid, cols in layer.updates.items():
                state.setdefault(rid, {}).update(cols)
        return state

    def get_at(self, row_id: Any, version: int) -> Optional[Dict[str, Any]]:
        return self.scan_at(version).get(row_id)

    # ---------- 版本回收与压缩 ----------

    def gc_watermark(self) -> int:
        """回收水位：最老活跃快照的版本；无快照时为当前版本。

        水位及以下的历史可以被折叠/回收，因为没有读者还需要它们。
        """
        if self._snapshots:
            return min(s.version for s in self._snapshots)
        return self.current_version

    def compact(self) -> None:
        """合并增量层。

        - 版本 <= 水位的层折叠为一个基线层（版本号 = 水位），
          其中被墓碑删除的行彻底回收；
        - 版本 > 水位的层原样保留。

        由于基线层只包含水位时刻已可见的值，活跃旧快照的可见集不变，
        绝不会让旧快照看到比它新的值。
        """
        watermark = self.gc_watermark()
        old = [l for l in self.layers if l.version <= watermark]
        new = [l for l in self.layers if l.version > watermark]
        if not old:
            return
        base = DeltaLayer(watermark)
        for layer in old:
            for rid in layer.deletes:
                base.updates.pop(rid, None)
                base.deletes.add(rid)
            for rid, cols in layer.updates.items():
                base.deletes.discard(rid)
                base.updates.setdefault(rid, {}).update(cols)
        # 基线层里仍是墓碑的行：没有任何活跃快照能看到它们，物理回收。
        for rid in base.deletes:
            base.updates.pop(rid, None)
        base.deletes.clear()
        self.layers = ([] if base.is_empty() else [base]) + new


# ---------- 值语义：null 与 NaN 不同 ----------

def values_equal(a: Any, b: Any) -> bool:
    """null(None) 与 NaN 不相等；NaN 与 NaN 视为相等（同为浮点缺失）。"""
    if a is None or b is None:
        return a is None and b is None
    if isinstance(a, float) and isinstance(b, float):
        if math.isnan(a) or math.isnan(b):
            return math.isnan(a) and math.isnan(b)
    return a == b and type(a) is type(b)
