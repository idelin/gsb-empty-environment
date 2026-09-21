"""不依赖索引的参考实现：全量线性扫描。

与 :class:`bitemp.core.BitemporalStore` 语义完全一致，但不做任何索引，
用于随机版本序列的对照测试。裁决规则在此处独立重写一遍（不 import
core.resolve_winner），保证对照测试能交叉验证两边的实现。
"""

from __future__ import annotations

from dataclasses import replace
from typing import Any

from .core import INF, Version

_KEEP = object()


def _winner(cands: list[Version]) -> Version | None:
    best: Version | None = None
    for v in cands:
        key = (v.priority, v.tx_from)
        if best is None:
            best = v
            continue
        bkey = (best.priority, best.tx_from)
        if key > bkey or (key == bkey and v.record_id < best.record_id):
            best = v
    return best


class ReferenceStore:
    """与 BitemporalStore 同 API 的暴力扫描实现。"""

    def __init__(self) -> None:
        self._all: dict[str, Version] = {}  # record_id -> 最新形态（含已关闭）

    def assert_version(
        self,
        object_id: str,
        valid_from: float,
        valid_to: float,
        payload: Any,
        *,
        at: float,
        priority: int = 0,
        record_id: str,
    ) -> Version:
        if not valid_from < valid_to:
            raise ValueError("empty valid interval")
        if record_id in self._all:
            raise ValueError(f"duplicate record_id {record_id!r}")
        v = Version(object_id, valid_from, valid_to, payload, priority,
                    record_id, at, INF)
        self._all[record_id] = v
        return v

    def correct(
        self,
        record_id: str,
        payload: Any = _KEEP,
        *,
        at: float,
        new_record_id: str,
        valid_from: float | None = None,
        valid_to: float | None = None,
        priority: int | None = None,
    ) -> Version:
        old = self._close(record_id, at)
        return self.assert_version(
            old.object_id,
            old.valid_from if valid_from is None else valid_from,
            old.valid_to if valid_to is None else valid_to,
            old.payload if payload is _KEEP else payload,
            at=at,
            priority=old.priority if priority is None else priority,
            record_id=new_record_id,
        )

    def retract(self, record_id: str, *, at: float) -> Version:
        return self._close(record_id, at)

    def _close(self, record_id: str, at: float) -> Version:
        old = self._all.get(record_id)
        if old is None or old.tx_to != INF:
            raise KeyError(record_id)
        if at < old.tx_from:
            raise ValueError("close before tx_from")
        closed = replace(old, tx_to=at)
        self._all[record_id] = closed
        return closed

    def _visible(self, object_id: str, as_of: float) -> list[Version]:
        return [
            v
            for v in self._all.values()
            if v.object_id == object_id and v.tx_from <= as_of < v.tx_to
        ]

    def candidates(
        self, object_id: str, valid_time: float, *, as_of: float
    ) -> list[Version]:
        return sorted(
            (
                v
                for v in self._visible(object_id, as_of)
                if v.valid_from <= valid_time < v.valid_to
            ),
            key=lambda v: v.record_id,
        )

    def query(
        self, object_id: str, valid_time: float, *, as_of: float
    ) -> Version | None:
        return _winner(self.candidates(object_id, valid_time, as_of=as_of))

    def snapshot(self, object_id: str, *, as_of: float) -> list[Version]:
        return sorted(
            self._visible(object_id, as_of),
            key=lambda v: (v.valid_from, v.valid_to, v.record_id),
        )

    def gc(self, horizon: float) -> int:
        dead = [rid for rid, v in self._all.items() if v.tx_to <= horizon]
        for rid in dead:
            del self._all[rid]
        return len(dead)
