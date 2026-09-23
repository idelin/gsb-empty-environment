"""下游消费者组：显式成员变更、水位推进与安全快照。"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Callable


class MembershipError(Exception):
    """成员变更或快照不合法。"""


@dataclass
class Consumer:
    consumer_id: str
    watermark: int  # 已消费的最大提交序号


@dataclass
class Snapshot:
    snapshot_id: str
    seq: int


class ConsumerGroup:
    def __init__(self, horizon_fn: Callable[[], int]) -> None:
        self._horizon_fn = horizon_fn
        self.consumers: dict[str, Consumer] = {}
        self.snapshots: dict[str, Snapshot] = {}

    def _check_safe_start(self, kind: str, name: str, seq: int) -> None:
        floor = self._horizon_fn()
        if seq < floor:
            raise MembershipError(
                f"{kind} '{name}' 起点 seq {seq} 低于安全快照下限 {floor}: "
                f"必须从 seq >= {floor} 的快照开始"
            )

    def register(self, consumer_id: str, start_seq: int) -> Consumer:
        if consumer_id in self.consumers:
            raise MembershipError(f"消费者 '{consumer_id}' 已存在")
        self._check_safe_start("消费者", consumer_id, start_seq)
        consumer = Consumer(consumer_id, start_seq)
        self.consumers[consumer_id] = consumer
        return consumer

    def deregister(self, consumer_id: str) -> None:
        try:
            del self.consumers[consumer_id]
        except KeyError:
            raise MembershipError(f"消费者 '{consumer_id}' 不存在") from None

    def advance(self, consumer_id: str, watermark: int) -> None:
        try:
            consumer = self.consumers[consumer_id]
        except KeyError:
            raise MembershipError(f"消费者 '{consumer_id}' 不存在") from None
        if watermark < consumer.watermark:
            raise MembershipError(
                f"消费者 '{consumer_id}' 水位不可回退: {consumer.watermark} -> {watermark}"
            )
        consumer.watermark = watermark

    def open_snapshot(self, snapshot_id: str, seq: int) -> Snapshot:
        if snapshot_id in self.snapshots:
            raise MembershipError(f"快照 '{snapshot_id}' 已存在")
        self._check_safe_start("快照", snapshot_id, seq)
        snapshot = Snapshot(snapshot_id, seq)
        self.snapshots[snapshot_id] = snapshot
        return snapshot

    def close_snapshot(self, snapshot_id: str) -> None:
        try:
            del self.snapshots[snapshot_id]
        except KeyError:
            raise MembershipError(f"快照 '{snapshot_id}' 不存在") from None
