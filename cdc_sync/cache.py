"""变更缓存：只收留水位之后的事件，去重、按提交序号排序。"""

from __future__ import annotations

from typing import Iterable

from .models import ChangeEvent


class ChangeCache:
    """扫描快照期间缓存后续变更。

    - commit_seq <= watermark 的事件直接拒绝（快照已包含，防双计）；
    - 同一事件重复投递只保留一份（按 tx_id/table/pk/commit_seq 去重）；
    - 回放顺序按 (commit_seq, table, pk) 确定，保证可重放、结果可预期。
    """

    def __init__(self, watermark: int):
        self.watermark = watermark
        self.high_water = watermark
        self.dropped = 0
        self._events: dict[tuple[str, str, str, int], ChangeEvent] = {}

    def add(self, event: ChangeEvent) -> bool:
        if event.commit_seq is None:
            raise ValueError("入缓存的事件必须已分配 commit_seq")
        if event.commit_seq <= self.watermark:
            self.dropped += 1
            return False
        key = (event.tx_id, event.table, event.pk, event.commit_seq)
        if key in self._events:
            self.dropped += 1
            return False
        self._events[key] = event
        self.high_water = max(self.high_water, event.commit_seq)
        return True

    def add_all(self, events: Iterable[ChangeEvent]) -> int:
        return sum(1 for ev in events if self.add(ev))

    def ordered(self) -> list[ChangeEvent]:
        return sorted(
            self._events.values(),
            key=lambda ev: (ev.commit_seq, ev.table, ev.pk),
        )

    def __len__(self) -> int:
        return len(self._events)
