"""共同水位管理。

共同水位 = 两个副本各自已应用提交序号进度的较小值。
只允许比较水位之下（含）的事件；水位单调不回退。
"""
from __future__ import annotations


class CommonWatermarkRollbackError(Exception):
    """共同水位回退（新水位低于当前水位）时抛出。"""


class WatermarkTracker:
    def __init__(self, initial: int = -1) -> None:
        self._current = initial

    @property
    def current(self) -> int:
        return self._current

    def advance(self, new_watermark: int) -> int:
        if new_watermark < self._current:
            raise CommonWatermarkRollbackError(
                f"共同水位不允许回退: 当前 {self._current}, 试图回退到 {new_watermark}"
            )
        self._current = new_watermark
        return self._current
