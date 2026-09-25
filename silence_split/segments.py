"""片段模型:稳定 ID、前后保留量扩展与相邻交叠裁剪。"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Segment:
    """一个可导出片段。

    ``core_start``/``core_end`` 为有声核心区(采样点);
    ``start``/``end`` 为叠加前后保留量并与邻段裁剪后的导出区间,
    所有片段的导出区间两两不相交,不会重复导出样本。
    """

    id: str
    core_start: int
    core_end: int
    start: int
    end: int

    @property
    def core_duration(self) -> int:
        return self.core_end - self.core_start

    @property
    def duration(self) -> int:
        return self.end - self.start


def make_segment_id(core_start: int) -> str:
    """稳定片段 ID:由核心起点派生。

    重分段后只要核心起点不变,ID 即不变;合并时保留最前段的 ID,
    拆分后首段保留原 ID,因而可跨修订追溯。
    """
    return f"seg-{core_start:08x}"


def build_segments(core_windows, total_samples, window_size, pre_pad, post_pad):
    """把窗口核心区间转换为带保留量且互不重叠的片段列表。"""
    cores = [(s * window_size, min(e * window_size, total_samples))
             for s, e in core_windows]
    cores = [(s, e) for s, e in cores if e > s]
    rows = []
    for cs, ce in cores:
        rows.append([max(0, cs - pre_pad), min(total_samples, ce + post_pad), cs, ce])
    for left, right in zip(rows, rows[1:]):
        # 相邻保留区交叠时,以两段核心之间的中点为界裁剪,保证导出区间不相交。
        boundary = (left[3] + right[2]) // 2
        if left[1] > boundary:
            left[1] = boundary
        if right[0] < boundary:
            right[0] = boundary
    return [
        Segment(id=make_segment_id(r[2]), core_start=r[2], core_end=r[3],
                start=r[0], end=r[1])
        for r in rows
    ]
