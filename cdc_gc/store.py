"""物化状态与墓碑：按提交序号应用变更，迟到旧事件不得复活已删除实体。"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from .events import ChangeEvent, Op


@dataclass(frozen=True)
class Tombstone:
    table: str
    pk: str
    delete_seq: int
    tx_id: str
    before: dict[str, Any]


@dataclass(frozen=True)
class ApplyResult:
    event: ChangeEvent
    outcome: str  # applied / deleted / stale / blocked_by_tombstone / below_horizon
    detail: str = ""


class MaterializedStore:
    def __init__(self) -> None:
        self._state: dict[str, dict[str, tuple[dict[str, Any], int]]] = {}
        self._tombstones: dict[str, dict[str, Tombstone]] = {}
        self.reclaimed_horizon = 0  # 已回收墓碑的最大删除序号
        self.dropped: list[ApplyResult] = []

    def get(self, table: str, pk: str) -> dict[str, Any] | None:
        row = self._state.get(table, {}).get(pk)
        return row[0] if row else None

    def tombstone(self, table: str, pk: str) -> Tombstone | None:
        return self._tombstones.get(table, {}).get(pk)

    def tombstones(self) -> list[Tombstone]:
        all_tombs = [t for tbl in self._tombstones.values() for t in tbl.values()]
        return sorted(all_tombs, key=lambda t: (t.delete_seq, t.table, t.pk))

    def remove_tombstone(self, tomb: Tombstone) -> None:
        del self._tombstones[tomb.table][tomb.pk]

    def apply(self, event: ChangeEvent) -> ApplyResult:
        state = self._state.setdefault(event.table, {})
        tombs = self._tombstones.setdefault(event.table, {})
        current = state.get(event.pk)
        tomb = tombs.get(event.pk)

        if current is None and tomb is None and event.commit_seq <= self.reclaimed_horizon:
            return self._drop(
                event,
                "below_horizon",
                f"seq {event.commit_seq} <= 已回收水位 {self.reclaimed_horizon}, 不可补写",
            )
        if tomb is not None and tomb.delete_seq >= event.commit_seq:
            return self._drop(
                event,
                "blocked_by_tombstone",
                f"墓碑删除点 {tomb.delete_seq} >= 事件 seq {event.commit_seq}, 不得复活",
            )
        if current is not None and current[1] >= event.commit_seq:
            return self._drop(
                event,
                "stale",
                f"当前版本 seq {current[1]} >= 事件 seq {event.commit_seq}, 丢弃乱序旧值",
            )

        if event.op is Op.DELETE:
            before = event.before if event.before is not None else (current[0] if current else {})
            state.pop(event.pk, None)
            tombs[event.pk] = Tombstone(
                event.table, event.pk, event.commit_seq, event.tx_id, before
            )
            return ApplyResult(event, "deleted")

        state[event.pk] = (event.after, event.commit_seq)
        tombs.pop(event.pk, None)  # 更新的插入是新一世代，取代旧墓碑
        return ApplyResult(event, "applied")

    def _drop(self, event: ChangeEvent, outcome: str, detail: str) -> ApplyResult:
        result = ApplyResult(event, outcome, detail)
        self.dropped.append(result)
        return result
