"""独立全量集合参考：对原始事件去重、按提交序号排序后重放为行集合，
再从全量行集合一次性计算各组 distinct 数。用于核验增量引擎，
对乱序、重复投递健壮；不依赖引擎的任何中间状态。"""
from __future__ import annotations

from typing import Iterable

from .engine import group_key_of, passes_filter, value_key_of
from .events import ChangeEvent


def reference_counts(events: Iterable[ChangeEvent], group_cols, distinct_cols,
                     *, null_equal=True, filters=None) -> dict:
    dedup = {}
    for e in events:
        dedup[e.event_id] = e  # 重复投递去重

    rows: dict[str, dict] = {}
    for e in sorted(dedup.values(), key=lambda e: e.commit_seq):
        if e.after is None:
            rows.pop(e.pk, None)
        else:
            rows[e.pk] = e.after

    values: dict[str, set] = {}
    for pk, row in rows.items():
        if not passes_filter(row, filters):
            continue
        g = group_key_of(row, group_cols)
        v = value_key_of(row, distinct_cols, pk, null_equal)
        values.setdefault(g, set()).add(v)
    return {g: len(vs) for g, vs in values.items()}
