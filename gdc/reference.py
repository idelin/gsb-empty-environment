"""独立全量集合参考核验。

参考模型不依赖增量聚合器的任何中间状态：它把事件流应用到一份
全量行集合（pk -> row），再从行集合整体重算期望的支持计数，
用于核验乱序、重复与恢复后的聚合器状态。
"""
from __future__ import annotations

from typing import Any, Dict, List

from .aggregator import NULL, GroupedDistinctCounter, NullPolicy, _UniqueNull
from .events import DELETE, INSERT, UPDATE, ChangeEvent


class ReferenceModel:
    def __init__(self, config):
        self.config = config
        self.rows: Dict[tuple, Dict[str, Any]] = {}  # 全量行集合

    def apply(self, event: ChangeEvent) -> None:
        if event.table != self.config.table:
            return
        if event.op in (INSERT, UPDATE):
            if event.after is None:
                raise ValueError("insert/update 事件缺少 after 行镜像")
            self.rows[event.pk] = dict(event.after)
        elif event.op == DELETE:
            self.rows.pop(event.pk, None)
        else:
            raise ValueError(f"unknown op: {event.op!r}")

    def expected_counts(self) -> Dict[tuple, Dict[tuple, int]]:
        """从全量行集合独立重算 (组 -> {值: 支持计数})。"""
        cfg = self.config
        result: Dict[tuple, Dict[tuple, int]] = {}
        for pk, row in self.rows.items():
            if cfg.row_filter is not None and not cfg.row_filter(row):
                continue
            vals = []
            has_null = False
            for i, col in enumerate(cfg.value_cols):
                v = row.get(col)
                if v is None:
                    has_null = True
                    vals.append(NULL if cfg.null_equal else _UniqueNull(pk, i))
                else:
                    vals.append(v)
            if has_null and cfg.null_policy is NullPolicy.IGNORE:
                continue
            group = tuple(NULL if row.get(c) is None else row.get(c) for c in cfg.group_cols)
            bucket = result.setdefault(group, {})
            key = tuple(vals)
            bucket[key] = bucket.get(key, 0) + 1
        return result

    def verify(self, counter: GroupedDistinctCounter) -> List[str]:
        """比对聚合器状态与全量重算结果，返回不一致描述列表。"""
        expected = self.expected_counts()
        actual = counter.snapshot()
        problems: List[str] = []
        for group in sorted(set(expected) | set(actual), key=repr):
            exp = expected.get(group, {})
            act = actual.get(group, {})
            if exp != act:
                problems.append(f"group={group!r}: expected={exp!r} actual={act!r}")
        return problems
