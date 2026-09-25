"""分段补丁事件:创建、修订、合并、拆分、废弃。"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class SegmentCreated:
    segment_id: str
    start: int
    end: int
    kind: str = "created"


@dataclass(frozen=True)
class SegmentRevised:
    """已确认片段边界变化;version 为已导出片段的新版本号,未导出为 None。"""
    segment_id: str
    old_start: int
    old_end: int
    new_start: int
    new_end: int
    version: int | None
    kind: str = "revised"


@dataclass(frozen=True)
class SegmentMerged:
    """若干旧段并入存活段;surviving_id 为最前段的稳定 ID。"""
    surviving_id: str
    absorbed_ids: tuple
    start: int
    end: int
    kind: str = "merged"


@dataclass(frozen=True)
class SegmentSplit:
    """旧段拆分;首段保留 source_id,ranges 为全部产物 (id, start, end)。"""
    source_id: str
    created_ids: tuple
    ranges: tuple
    kind: str = "split"


@dataclass(frozen=True)
class SegmentDiscarded:
    segment_id: str
    exported: bool
    kind: str = "discarded"
