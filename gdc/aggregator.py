"""按组维护值支持计数与 distinct 数的增量聚合器。

核心语义：
- 每个 (组, 值) 维护支持计数（supporter 行数），distinct 数 = 组内计数 > 0 的值个数。
- 相同值的两行删除其一，支持计数减一但仍计 1 个 distinct；删除最后一条才减 distinct。
- 删除/更新导致计数变负说明缺失事件，抛出 MissingEventError 拒绝。
- null 策略显式可配：IGNORE（含 null 的值不计入）或 COUNT_AS_VALUE（null 作为独立值）。
- null_equal 可配：False 时每个 null 出现都是独立 distinct 值（按主键区分）。
- 跨组更新原子执行：先撤销旧贡献再加入新贡献，失败时按已全部回滚。
- 状态可溢写磁盘；checkpoint 保存完整支持计数与输入去重集合（不是输出计数）。
"""
from __future__ import annotations

import os
import pickle
from dataclasses import dataclass
from enum import Enum
from typing import Any, Callable, Dict, Mapping, Optional, Set, Tuple

from .events import DELETE, INSERT, UPDATE, ChangeEvent


class MissingEventError(RuntimeError):
    """删除/更新会使支持计数变负：说明缺失了前置事件，拒绝应用。"""


class NullPolicy(Enum):
    IGNORE = "ignore"                    # 含 null 的值不参与统计
    COUNT_AS_VALUE = "count_as_value"    # null 作为独立值参与 distinct


class _NullType:
    """null 哨兵。按值相等，可安全 pickle / 跨进程恢复。"""
    __slots__ = ()

    def __repr__(self) -> str:
        return "NULL"

    def __eq__(self, other: object) -> bool:
        return isinstance(other, _NullType)

    def __hash__(self) -> int:
        return hash(_NullType)


NULL = _NullType()


@dataclass(frozen=True)
class _UniqueNull:
    """null_equal=False 时，每个 null 出现按 (主键, 列序号) 区分为独立值。"""
    pk: tuple
    col_index: int


def _normalize(value: Any) -> Any:
    return NULL if value is None else value


RowFilter = Callable[[Mapping[str, Any]], bool]


@dataclass
class DistinctConfig:
    table: str                              # 只消费该表的事件
    group_cols: Tuple[str, ...]             # 分组列
    value_cols: Tuple[str, ...]             # distinct 值列（可多列组合）
    null_policy: NullPolicy = NullPolicy.COUNT_AS_VALUE
    null_equal: bool = True                 # False 时 null != null
    row_filter: Optional[RowFilter] = None  # 条件过滤，作用于前后行镜像
    spill_threshold: int = 10000            # 单组值数超过该阈值即溢写


class GroupedDistinctCounter:
    def __init__(self, config: DistinctConfig, spill_dir: Optional[str] = None):
        self.config = config
        self._spill_dir = spill_dir
        # group_key -> {value_key: 支持计数}；已溢写的组占位为 None
        self._states: Dict[tuple, Optional[Dict[tuple, int]]] = {}
        self._spilled: Dict[tuple, str] = {}  # group_key -> 溢写文件路径
        self._spill_seq = 0
        self._processed: Set[str] = set()     # 输入去重集合
        self.last_commit_seq = -1
        self.stats = {"applied": 0, "dedup_skipped": 0, "filtered": 0}

    # ---- 键构造 ----

    def _group_key(self, row: Mapping[str, Any]) -> tuple:
        return tuple(_normalize(row.get(c)) for c in self.config.group_cols)

    def _value_key(self, row: Mapping[str, Any], pk: tuple) -> Tuple[tuple, bool]:
        vals = []
        has_null = False
        for i, col in enumerate(self.config.value_cols):
            v = row.get(col)
            if v is None:
                has_null = True
                vals.append(NULL if self.config.null_equal else _UniqueNull(pk, i))
            else:
                vals.append(v)
        return tuple(vals), has_null

    def _contribution(self, row: Optional[Mapping[str, Any]], pk: tuple) -> Optional[tuple]:
        """计算一行对 (组, 值) 的贡献；被过滤或 null 被忽略时返回 None。"""
        if row is None:
            return None
        cfg = self.config
        if cfg.row_filter is not None and not cfg.row_filter(row):
            return None
        value_key, has_null = self._value_key(row, pk)
        if has_null and cfg.null_policy is NullPolicy.IGNORE:
            return None
        return (self._group_key(row), value_key)

    # ---- 状态访问（含溢写载入） ----

    def _counts(self, group_key: tuple, create: bool = False) -> Optional[Dict[tuple, int]]:
        counts = self._states.get(group_key)
        if counts is None and group_key in self._spilled:
            path = self._spilled.pop(group_key)
            with open(path, "rb") as fh:
                counts = pickle.load(fh)
            os.unlink(path)
            self._states[group_key] = counts
        if counts is None and create:
            counts = {}
            self._states[group_key] = counts
        return counts

    def _maybe_spill(self, group_key: tuple) -> None:
        if not self._spill_dir:
            return
        counts = self._states.get(group_key)
        if counts is not None and len(counts) >= self.config.spill_threshold:
            os.makedirs(self._spill_dir, exist_ok=True)
            path = os.path.join(self._spill_dir, f"group_{self._spill_seq}.pkl")
            self._spill_seq += 1
            with open(path, "wb") as fh:
                pickle.dump(counts, fh)
            self._spilled[group_key] = path
            self._states[group_key] = None

    # ---- 计数原语 ----

    def _increment(self, group_key: tuple, value_key: tuple) -> None:
        counts = self._counts(group_key, create=True)
        assert counts is not None
        counts[value_key] = counts.get(value_key, 0) + 1
        self._maybe_spill(group_key)

    def _decrement(self, group_key: tuple, value_key: tuple) -> None:
        counts = self._counts(group_key)
        current = counts.get(value_key, 0) if counts else 0
        if current <= 0:
            raise MissingEventError(
                f"支持计数将变负（缺事件）: group={group_key!r} value={value_key!r}"
            )
        if current == 1:
            del counts[value_key]  # 删除最后支持者，distinct 准确减一
        else:
            counts[value_key] = current - 1
        if not counts:
            self._states.pop(group_key, None)

    # ---- 事件应用 ----

    def apply(self, event: ChangeEvent) -> bool:
        """应用一条事件；重复事件（同 event_id）跳过并返回 False。"""
        if event.event_id in self._processed:
            self.stats["dedup_skipped"] += 1
            return False
        if event.table == self.config.table:
            if event.op == INSERT:
                self._apply_insert(event)
            elif event.op == DELETE:
                self._apply_delete(event)
            elif event.op == UPDATE:
                self._apply_update(event)
            else:
                raise ValueError(f"unknown op: {event.op!r}")
            self.stats["applied"] += 1
        self._processed.add(event.event_id)
        self.last_commit_seq = max(self.last_commit_seq, event.commit_seq)
        return True

    def _apply_insert(self, event: ChangeEvent) -> None:
        contrib = self._contribution(event.after, event.pk)
        if contrib is None:
            self.stats["filtered"] += 1
            return
        self._increment(*contrib)

    def _apply_delete(self, event: ChangeEvent) -> None:
        contrib = self._contribution(event.before, event.pk)
        if contrib is None:
            self.stats["filtered"] += 1
            return
        self._decrement(*contrib)

    def _apply_update(self, event: ChangeEvent) -> None:
        """组迁移原子执行：撤销旧贡献 -> 加入新贡献，失败整体回滚。"""
        old = self._contribution(event.before, event.pk)
        new = self._contribution(event.after, event.pk)
        if old == new:
            return
        journal = []  # 回滚日志：("undo_remove"|"undo_add", contribution)
        try:
            if old is not None:
                self._decrement(*old)
                journal.append(("undo_remove", old))
            if new is not None:
                self._increment(*new)
                journal.append(("undo_add", new))
        except Exception:
            for action, contrib in reversed(journal):
                if action == "undo_remove":
                    self._increment(*contrib)   # 恢复被撤销的旧贡献
                else:
                    self._decrement(*contrib)   # 撤回已加入的新贡献
            raise

    # ---- 查询 ----

    def snapshot(self) -> Dict[tuple, Dict[tuple, int]]:
        """完整支持计数快照（会按需载入溢写组）。"""
        return {g: dict(self._counts(g) or {}) for g in list(self._states)}

    def distinct_counts(self) -> Dict[tuple, int]:
        return {g: len(counts) for g, counts in self.snapshot().items()}

    # ---- checkpoint / 恢复 ----

    def checkpoint(self, path: str) -> None:
        """checkpoint 保存完整支持计数与输入去重集合。

        注意：仅保存输出 distinct 数不足以恢复状态——同值多行的支持计数
        必须保留，否则后续删除无法正确维护 distinct。
        """
        data = {
            "table": self.config.table,
            "states": self.snapshot(),
            "processed": sorted(self._processed),
            "last_commit_seq": self.last_commit_seq,
        }
        tmp_path = path + ".tmp"
        with open(tmp_path, "wb") as fh:
            pickle.dump(data, fh)
        os.replace(tmp_path, path)  # 原子落盘

    @classmethod
    def restore(
        cls, path: str, config: DistinctConfig, spill_dir: Optional[str] = None
    ) -> "GroupedDistinctCounter":
        with open(path, "rb") as fh:
            data = pickle.load(fh)
        if data["table"] != config.table:
            raise ValueError(f"checkpoint 表 {data['table']!r} 与配置 {config.table!r} 不符")
        counter = cls(config, spill_dir=spill_dir)
        counter._states = {g: dict(counts) for g, counts in data["states"].items()}
        counter._processed = set(data["processed"])
        counter.last_commit_seq = data["last_commit_seq"]
        return counter
