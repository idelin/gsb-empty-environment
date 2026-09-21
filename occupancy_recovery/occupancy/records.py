"""占用记录的状态定义。

每条占用以一个不透明的占用 ID（``occupancy_id``）标识，start 与 end 可能
乱序到达、重复到达、被更正，也可能被撤回。任何时刻一个 ID 只处于以下五种
状态之一。
"""

from __future__ import annotations

import enum
from dataclasses import dataclass
from typing import Optional


class RecordStatus(enum.Enum):
    EMPTY = "empty"          # 尚未见到任何端点
    START_ONLY = "start_only"  # 只有 start，未配对（孤立 start）
    END_ONLY = "end_only"      # 只有 end（包括先到的 end，孤立 end）
    PAIRED = "paired"          # start/end 配对完成，形成有效半开区间
    WITHDRAWN = "withdrawn"    # 被撤回，不再对并发产生任何贡献


class Outcome(enum.Enum):
    RECORDED = "recorded"            # 端点被接受
    DUPLICATE = "duplicate"          # 同值端点重复上报，状态不变
    CORRECTED = "corrected"          # 端点更正成功，旧贡献已原子撤销
    REJECTED_INVALID = "rejected_invalid"  # end <= start 等非法，状态完全不变
    REJECTED_WITHDRAWN = "rejected_withdrawn"  # ID 已撤回，拒绝再次上报
    WITHDRAWN = "withdrawn"          # 撤回成功（配对贡献已移除 / 孤立记录清除）
    NOT_FOUND = "not_found"          # 撤回一个不存在的 ID


@dataclass(frozen=True)
class OperationResult:
    outcome: Outcome
    occupancy_id: str
    detail: str = ""
    status: RecordStatus = RecordStatus.EMPTY
    interval: Optional[tuple] = None  # 操作后形成的有效区间 (start, end)


@dataclass
class OccupancyRecord:
    occupancy_id: str
    status: RecordStatus = RecordStatus.EMPTY
    start: Optional[int] = None
    end: Optional[int] = None

    @property
    def is_active(self) -> bool:
        return self.status is RecordStatus.PAIRED

    @property
    def is_orphan(self) -> bool:
        return self.status in (RecordStatus.START_ONLY, RecordStatus.END_ONLY)

    def interval_or_none(self) -> Optional[tuple]:
        if self.status is RecordStatus.PAIRED:
            return (self.start, self.end)
        return None
