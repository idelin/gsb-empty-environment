"""同步编排：一致性快照 -> 扫描期间缓存变更 -> 幂等回放 -> 原子发布。"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Callable, Optional

from .cache import ChangeCache
from .source import SourceDatabase
from .target import TargetDatabase


@dataclass(frozen=True)
class SyncReport:
    watermark: int
    high_water: int
    snapshot_rows: int
    cached_events: int
    cache_dropped: int
    applied: int
    skipped: int
    tables: tuple[str, ...] = field(default_factory=tuple)


class SnapshotSynchronizer:
    """全量初始同步。

    流程：
      1. 打开一致性快照，记录水位 watermark（与快照数据同一视图）；
      2. 逐行扫描快照写入目标暂存区，行上标记 seq=watermark；
      3. 扫描过程中持续把 commit_seq > watermark 的变更拉入缓存；
      4. 扫描结束后兜底再拉一次，确保不漏；
      5. 按 commit_seq 顺序幂等回放缓存（快照已含的事件自动跳过）；
      6. 单事务原子发布整代结果；任何失败丢弃暂存区，不发布混合状态。
    """

    def __init__(self, source: SourceDatabase, target: TargetDatabase):
        self.source = source
        self.target = target

    def _drain(self, cache: ChangeCache, last_fetched: int) -> int:
        """把 last_fetched 之后已提交的变更拉进缓存，返回新的拉取位点。"""
        cache.add_all(self.source.fetch_changes_since(last_fetched))
        return cache.high_water

    def run(
        self,
        during_scan: Optional[Callable[[int], None]] = None,
    ) -> SyncReport:
        snapshot = self.source.open_snapshot()
        watermark = snapshot.watermark
        cache = ChangeCache(watermark)
        snapshot_rows = 0
        applied = 0
        skipped = 0
        tables: set[str] = set()
        try:
            self.target.reset_staging()
            last_fetched = watermark
            for tbl in snapshot.tables():
                tables.add(tbl)
                self.target.ensure_staging_table(tbl)
                for pk, data in snapshot.iter_rows(tbl):
                    self.target.stage_row(tbl, pk, data, watermark)
                    snapshot_rows += 1
                    if during_scan is not None:
                        during_scan(snapshot_rows)
                    # 扫描期间增量缓存后续变更，防漏。
                    last_fetched = self._drain(cache, last_fetched)
            # 兜底：快照结束后到发布前提交的变更也要收进来。
            self._drain(cache, last_fetched)
            for event in cache.ordered():
                tables.add(event.table)
                self.target.ensure_staging_table(event.table)
                if self.target.apply_event(event):
                    applied += 1
                else:
                    skipped += 1
            self.target.publish()
        except BaseException:
            self.target.abort_staging()
            raise
        finally:
            snapshot.close()
        return SyncReport(
            watermark=watermark,
            high_water=cache.high_water,
            snapshot_rows=snapshot_rows,
            cached_events=len(cache),
            cache_dropped=cache.dropped,
            applied=applied,
            skipped=skipped,
            tables=tuple(sorted(tables)),
        )
