"""复制器：一致性快照 + 变更缓存 + 水位后回放。

流程：
1. 读取带一致性水位的全量快照（扫描期间源库仍在变更）。
2. 缓存水位之后的后续变更（允许重叠拉取与重复投递）。
3. 快照装入暂存区；仅回放 commit_seq > 水位的事件，
   并按事务ID去重，防双计、防漏变更。
4. 暂存区一次性原子发布；快照失败则目标库保持原状。
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Optional

from .fixture import FixtureSource, SnapshotError
from .store import TableStore


@dataclass
class ReplayStats:
    cached: int = 0
    applied: int = 0
    skipped_before_watermark: int = 0
    skipped_duplicate_txn: int = 0


class ChangeCache:
    """缓存快照期间及之后的变更，回放时按提交序号排序。"""

    def __init__(self) -> None:
        self._events = []

    def add(self, events) -> None:
        self._events.extend(events)

    def ordered(self):
        return sorted(self._events, key=lambda e: e.commit_seq)


class Replicator:
    def __init__(self, source: FixtureSource, target: TableStore, overlap: int = 0):
        self.source = source
        self.target = target
        self.overlap = overlap  # 拉取时向前回退的序号数，验证水位过滤
        self.applied_txns: set[str] = set()
        self.stats = ReplayStats()

    def run(self, during_scan=None, extra_events=None) -> Optional[int]:
        cache = ChangeCache()
        try:
            snap = self.source.snapshot(during_scan=during_scan)
        except SnapshotError:
            return None  # 快照失败：不发布任何混合状态

        # 缓存后续变更（向前回退 overlap，模拟订阅重叠窗口）
        cache.add(self.source.changes_since(max(0, snap.watermark - self.overlap)))
        if extra_events:
            cache.add(extra_events)  # 模拟重复投递
        self.stats.cached += len(cache._events)

        staging = TableStore()
        for table, pk, row in snap.rows:
            staging.load_snapshot_row(table, pk, row)

        for event in cache.ordered():
            if event.commit_seq <= snap.watermark:
                self.stats.skipped_before_watermark += 1
                continue
            if event.txn_id in self.applied_txns:
                self.stats.skipped_duplicate_txn += 1
                continue
            staging.apply_event(event)
            self.applied_txns.add(event.txn_id)
            self.stats.applied += 1

        self.target.publish_from(staging)
        return snap.watermark
