"""事件时间去抖器（可暂停 / 可恢复），仅依赖 Python 标准库。

核心语义
--------
- 每个来源（source）的状态完全隔离，各自维护"已确认值"与"候选值"。
- 候选值稳定满 ``threshold`` 个**事件时间**单位后才被确认：
  候选从 ``candidate_since`` 起计时，当事件时钟推进到
  ``candidate_since + threshold`` 时确认（边界取等，满即确认）。
- 未知值（``None``，即 ``UNKNOWN``）会中断候选计时：清空该来源的候选，
  但不影响已确认值；之后任何新候选都必须重新累计完整门限时长。
- 重复事件不延长稳定计时：与当前候选相同的值不会重置 ``candidate_since``；
  与已确认值相同的事件直接取消挂起的候选（视为回退，无需再确认）。
- 确认产生的变化进入出站队列（outbox），由 ``drain()`` 消费，
  每条变化只投递一次。

同一时刻多事件的顺序
--------------------
1. 事件时钟先推进到该时刻：所有截止时间 ``<= ts`` 的候选在此时刻确认，
   确认时间记为其精确截止时间（``candidate_since + threshold``）。
2. 然后按 **ingest 调用顺序** 依次应用该时刻的事件；
   同一来源同一时刻的多个事件，后到的覆盖先到的（last write wins），
   候选计时以最后一次改变值的事件时间为准。
3. 时钟只前进不后退：``ts < now`` 的事件或推进请求抛出 ``ValueError``。

暂停 / 恢复
-----------
本去抖器完全由事件时间驱动，没有后台线程，"暂停"即停止调用
``ingest``/``advance_to``；``save(path)`` 把完整状态写入 JSON 快照，
``Debouncer.restore(path)`` 从快照恢复。恢复后：
- 已确认但未消费的变化仍在 outbox 中，不会丢失，也不会重复投递；
- 尚未满足时长的候选保留 ``candidate_since``，继续累计剩余时长；
- 已确认值保留用于去重，不会因旧事件重放而再次发送。

持久化内容（save 快照）
-----------------------
- ``threshold``、当前事件时钟 ``now``；
- 每个来源：已确认值、候选值、候选起始时间；
- outbox 中已确认但未消费的变化（含确认时间）。
注意：状态值必须可被 JSON 序列化（str/int/float/bool/None/list/dict）。

旧事件的释放
------------
- 原始事件从不留存，只保留按来源聚合的派生状态，内存为 O(来源数)；
- 候选状态在确认、被新值替换、被未知值中断、或回退到已确认值时释放；
- outbox 条目在 ``drain()`` 消费后释放，之后的快照不再包含它们；
- 已确认值按来源长期保留（用于抑制重复投递），直到进程丢弃该实例。
"""

from __future__ import annotations

import json
from collections import deque
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Union

#: 未知值哨兵：摄入 None 表示该来源当前值未知，会中断候选计时。
UNKNOWN = None

PathLike = Union[str, Path]


@dataclass(frozen=True)
class Change:
    """一次已确认的状态变化。"""

    source: str
    value: Any
    confirmed_at: float


@dataclass
class _SourceState:
    has_confirmed: bool = False
    confirmed: Any = None
    candidate: Any = None
    candidate_since: float = 0.0


class Debouncer:
    """按事件时间确认状态变化的去抖器。"""

    SCHEMA_VERSION = 1

    def __init__(self, threshold: float) -> None:
        if threshold <= 0:
            raise ValueError("threshold must be positive")
        self.threshold = float(threshold)
        self.now = 0.0
        self._sources: dict[str, _SourceState] = {}
        self._outbox: deque[Change] = deque()

    # ------------------------------------------------------------------ intake
    def ingest(self, source: str, value: Any, ts: float) -> None:
        """摄入一条事件。``value is None`` 表示未知值，会中断候选计时。"""
        self._settle(ts)
        state = self._sources.setdefault(source, _SourceState())
        if value is UNKNOWN:
            state.candidate = None
            return
        if state.candidate is not None and state.candidate == value:
            return  # 与候选重复：不延长稳定计时
        if state.has_confirmed and state.confirmed == value:
            state.candidate = None  # 回退到已确认值：取消挂起的候选
            return
        state.candidate = value
        state.candidate_since = float(ts)

    # -------------------------------------------------------------------- time
    def advance_to(self, ts: float) -> None:
        """把事件时钟推进到 ``ts``，确认所有到期的候选。"""
        self._settle(ts)

    def _settle(self, ts: float) -> None:
        if ts < self.now:
            raise ValueError(f"time cannot go backwards: {ts} < {self.now}")
        self.now = float(ts)
        for name, state in self._sources.items():
            if state.candidate is None:
                continue
            deadline = state.candidate_since + self.threshold
            if deadline <= ts:
                state.has_confirmed = True
                state.confirmed = state.candidate
                self._outbox.append(Change(name, state.candidate, deadline))
                state.candidate = None

    # ------------------------------------------------------------------ output
    def drain(self) -> list[Change]:
        """取出并清空所有已确认但未消费的变化（按确认顺序）。"""
        changes = list(self._outbox)
        self._outbox.clear()
        return changes

    @property
    def pending_count(self) -> int:
        """已确认但未消费的变化条数。"""
        return len(self._outbox)

    # ------------------------------------------------------------- persistence
    def save(self, path: PathLike) -> None:
        """把完整状态写入 JSON 快照（暂停）。"""
        payload = {
            "version": self.SCHEMA_VERSION,
            "threshold": self.threshold,
            "now": self.now,
            "sources": {
                name: {
                    "has_confirmed": state.has_confirmed,
                    "confirmed": state.confirmed,
                    "candidate": state.candidate,
                    "candidate_since": state.candidate_since,
                }
                for name, state in self._sources.items()
            },
            "outbox": [
                {
                    "source": change.source,
                    "value": change.value,
                    "confirmed_at": change.confirmed_at,
                }
                for change in self._outbox
            ],
        }
        Path(path).write_text(
            json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8"
        )

    @classmethod
    def restore(cls, path: PathLike) -> "Debouncer":
        """从 JSON 快照恢复（恢复），不丢候选、不重发已确认变化。"""
        payload = json.loads(Path(path).read_text(encoding="utf-8"))
        if payload.get("version") != cls.SCHEMA_VERSION:
            raise ValueError(f"unsupported snapshot version: {payload.get('version')}")
        debouncer = cls(payload["threshold"])
        debouncer.now = payload["now"]
        for name, state in payload["sources"].items():
            debouncer._sources[name] = _SourceState(**state)
        for change in payload["outbox"]:
            debouncer._outbox.append(Change(**change))
        return debouncer
