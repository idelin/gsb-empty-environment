"""墓碑回收：所有下游水位越过删除点且无旧快照引用后才可回收。"""
from __future__ import annotations

from dataclasses import dataclass

from .group import ConsumerGroup
from .store import MaterializedStore, Tombstone


@dataclass(frozen=True)
class ReclaimDecision:
    tombstone: Tombstone
    reasons: tuple[str, ...]  # 阻塞原因；为空即可回收

    @property
    def reclaimable(self) -> bool:
        return not self.reasons


class GarbageCollector:
    def __init__(self, store: MaterializedStore, group: ConsumerGroup) -> None:
        self.store = store
        self.group = group

    def evaluate(self) -> list[ReclaimDecision]:
        decisions: list[ReclaimDecision] = []
        for tomb in self.store.tombstones():
            reasons: list[str] = []
            for consumer in self.group.consumers.values():
                if consumer.watermark < tomb.delete_seq:
                    reasons.append(
                        f"消费者 '{consumer.consumer_id}' 水位 {consumer.watermark} "
                        f"未越过删除点 {tomb.delete_seq}"
                    )
            for snapshot in self.group.snapshots.values():
                if snapshot.seq < tomb.delete_seq:
                    reasons.append(
                        f"快照 '{snapshot.snapshot_id}' 引用 seq {snapshot.seq} "
                        f"早于删除点 {tomb.delete_seq}"
                    )
            decisions.append(ReclaimDecision(tomb, tuple(reasons)))
        return decisions

    def sweep(self) -> list[Tombstone]:
        reclaimed: list[Tombstone] = []
        for decision in self.evaluate():
            if decision.reclaimable:
                self.store.remove_tombstone(decision.tombstone)
                self.store.reclaimed_horizon = max(
                    self.store.reclaimed_horizon, decision.tombstone.delete_seq
                )
                reclaimed.append(decision.tombstone)
        return reclaimed
