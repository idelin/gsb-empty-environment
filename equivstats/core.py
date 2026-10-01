"""核心数据结构：记录、等价类计数、显式 null 策略、增量删除。

定义：
- 等价类（equivalence class）：准标识符 qid 元组相同的记录集合。
- 敏感值计数：每个等价类内 sensitive 值的多重计数（Counter）。
- null 策略（显式配置）：
    NullPolicy.IGNORE      —— None 不计入类别，也不计入熵的总数；
    NullPolicy.AS_CATEGORY —— None 视为一个独立类别参与计数与熵。
注意：计数层（本模块）对 None 一律如实记录，策略只在指标层生效，
从而同一数据可在两种策略下分别评估，且便于独立重算校验。
"""

from __future__ import annotations

from collections import Counter, defaultdict
from dataclasses import dataclass
from enum import Enum
from typing import Hashable, Iterable


class NullPolicy(Enum):
    IGNORE = "ignore"
    AS_CATEGORY = "as_category"


@dataclass(frozen=True)
class Record:
    qid: tuple
    sensitive: Hashable | None


class Dataset:
    """按等价类维护敏感值计数，支持增量删除与可回滚的层级合并。"""

    def __init__(self, null_policy: NullPolicy = NullPolicy.IGNORE, log_base: float = 2):
        self.null_policy = null_policy
        self.log_base = log_base
        self._counts: dict[tuple, Counter] = defaultdict(Counter)
        self._records: list[Record] = []
        # 每次层级合并（generalize）压入一批 (qid, old_value, new_value, count)，
        # 用于保留原贡献以支持撤回（rollback）。
        self._history: list[list[tuple]] = []

    # ---- 增量维护 ----

    def add(self, record: Record) -> None:
        self._records.append(record)
        self._counts[record.qid][record.sensitive] += 1

    def add_many(self, records: Iterable[Record]) -> None:
        for record in records:
            self.add(record)

    def remove(self, record: Record) -> None:
        """增量删除：仅更新受影响等价类的计数，不重扫全量数据。"""
        self._records.remove(record)
        counter = self._counts[record.qid]
        counter[record.sensitive] -= 1
        if counter[record.sensitive] == 0:
            del counter[record.sensitive]
        if not counter:
            del self._counts[record.qid]

    def remove_indices(self, indices: Iterable[int]) -> list[Record]:
        removed = []
        for index in sorted(indices, reverse=True):
            record = self._records[index]
            self.remove(record)
            removed.append(record)
        return removed

    # ---- 层级合并（保留原贡献，支持撤回） ----

    def generalize(self, mapping: dict) -> None:
        """按 mapping {原敏感值: 合并后值} 合并计数，原贡献记入历史。"""
        batch = []
        for qid, counter in self._counts.items():
            for old_value, new_value in mapping.items():
                if old_value == new_value or old_value not in counter:
                    continue
                count = counter.pop(old_value)
                counter[new_value] += count
                batch.append((qid, old_value, new_value, count))
        self._history.append(batch)

    def rollback(self) -> None:
        """撤回上一次层级合并，逐条恢复原贡献。"""
        if not self._history:
            raise IndexError("no generalization to roll back")
        batch = self._history.pop()
        for qid, old_value, new_value, count in reversed(batch):
            counter = self._counts[qid]
            counter[new_value] -= count
            if counter[new_value] == 0:
                del counter[new_value]
            counter[old_value] += count

    # ---- 只读视图 ----

    @property
    def records(self) -> list[Record]:
        return list(self._records)

    def class_counts(self) -> dict[tuple, Counter]:
        return {qid: Counter(counter) for qid, counter in self._counts.items()}
