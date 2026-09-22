"""可暂停 / 可恢复的事件时间去抖器（仅标准库）。

问题背景
------
输入状态会抖动，下游只想接收"经过持续时间确认"的变化：某个候选值在
*事件时间* 上连续稳定满 ``threshold`` 才被视为确认变化并交给下游。

核心语义
------
- 每个来源（source）独立维护状态，来源之间完全隔离。
- ``value is None`` 表示"未知值"：它会中断当前候选计时（候选被清除），
  但不会改动已确认值。因此 None 是保留值，不能作为业务状态。
- 候选值与当前候选相同的事件是重复事件，**不会**延长稳定计时；
  候选值等于已确认值时，候选直接清除（视为回到已确认状态）。
- 候选稳定时长按 ``now - candidate_since >= threshold`` 判定，
  即"满"门限即确认（门限 3 时，候选始于 1，推进到 4 即确认）。
- 确认结果进入待消费队列，``drain()`` 是唯一的消费出口。

同一时刻多事件的顺序
------------------
- 事件按 ``ingest`` 的调用顺序逐一处理；同一事件时间戳下的多个事件
  不产生并列语义，后到者照常覆盖候选（候选起点取最后一次切换的时间）。
- 每个来源的事件时间戳必须非递减；且不得早于已 ``advance`` 到的时间
  （迟到事件直接抛 ``ValueError``，因为确认决策已经做出，无法回滚）。
- 一次 ``advance`` 中如有多个来源同时确认，按来源首次出现的顺序入队。

持久化说明（save / restore）
--------------------------
``save()`` 返回一个纯 dict（来源为 str、值为 JSON 标量时可直接
``json.dumps``），内容包括：

- ``threshold`` 与当前事件时间 ``now``；
- 每个来源的：已确认值（含"是否已设置"标记）、当前候选值及其起始时间、
  该源最近事件时间（用于迟到检测）；
- 已确认但尚未消费的 ``pending`` 队列。

不持久化任何事件日志：每个事件在 ``ingest`` 时即被折叠进上述 O(1) 状态，
旧事件随之立即释放；``pending`` 中的确认结果在 ``drain()`` 时释放；
来源状态在整个去抖器生命周期内保留，可用 ``drop_source()`` 显式释放。
因此恢复后：已确认且已消费的变化不会重发（pending 是唯一出口且随
checkpoint 持久化），尚未满足时长的候选连同其起始时间一起保留。
"""

from __future__ import annotations

from typing import Any, Dict, Hashable, List, NamedTuple, Optional


class Change(NamedTuple):
    """一次已确认的变化。"""

    source: Hashable
    value: Any
    at: float  # 确认发生的事件时间


class _SourceState:
    __slots__ = ("confirmed_set", "confirmed", "candidate", "candidate_since", "last_ts")

    def __init__(self) -> None:
        self.confirmed_set = False
        self.confirmed: Any = None
        self.candidate: Any = None
        self.candidate_since: Optional[float] = None
        self.last_ts: Optional[float] = None


class Debouncer:
    """按事件时间确认稳定变化的去抖器，支持 checkpoint 保存与恢复。"""

    VERSION = 1

    def __init__(self, threshold: float) -> None:
        if threshold < 0:
            raise ValueError("threshold 必须非负")
        self._threshold = threshold
        self._now: Optional[float] = None
        self._sources: Dict[Hashable, _SourceState] = {}
        self._pending: List[Change] = []

    # ------------------------------------------------------------------ 摄入

    def ingest(self, source: Hashable, value: Any, ts: float) -> None:
        """摄入一个事件。``value is None`` 表示未知值，会中断候选计时。"""
        if self._now is not None and ts < self._now:
            raise ValueError(f"事件时间 {ts} 早于已推进时间 {self._now}")
        state = self._sources.setdefault(source, _SourceState())
        if state.last_ts is not None and ts < state.last_ts:
            raise ValueError(f"来源 {source!r} 的事件时间回退：{ts} < {state.last_ts}")
        state.last_ts = ts

        if value is None:  # 未知值：中断候选，不动已确认值
            state.candidate = None
            state.candidate_since = None
            return
        if state.confirmed_set and value == state.confirmed:
            # 回到已确认状态：候选没有必要继续计时
            state.candidate = None
            state.candidate_since = None
            return
        if state.candidate is not None and value == state.candidate:
            return  # 重复事件：不延长稳定计时
        state.candidate = value
        state.candidate_since = ts

    # -------------------------------------------------------------- 推进时间

    def advance(self, now: float) -> None:
        """推进事件时间，确认所有稳定满门限的候选（结果进入待消费队列）。"""
        if self._now is not None and now < self._now:
            raise ValueError(f"时间不能回退：{now} < {self._now}")
        self._now = now
        for source, state in self._sources.items():  # 按来源首次出现顺序
            if state.candidate is None:
                continue
            if now - state.candidate_since >= self._threshold:
                state.confirmed = state.candidate
                state.confirmed_set = True
                state.candidate = None
                state.candidate_since = None
                self._pending.append(Change(source, state.confirmed, now))

    # ------------------------------------------------------------------ 消费

    def drain(self) -> List[Change]:
        """取出并清空所有已确认但未消费的变化。"""
        out = self._pending
        self._pending = []
        return out

    @property
    def pending_count(self) -> int:
        return len(self._pending)

    def drop_source(self, source: Hashable) -> None:
        """显式释放一个来源的全部状态（其未消费的确认结果一并丢弃）。"""
        self._sources.pop(source, None)
        self._pending = [c for c in self._pending if c.source != source]

    # ---------------------------------------------------------- 保存 / 恢复

    def save(self) -> Dict[str, Any]:
        """导出 checkpoint（纯 dict，来源为 str 且值为 JSON 标量时可序列化）。"""
        return {
            "version": self.VERSION,
            "threshold": self._threshold,
            "now": self._now,
            "pending": [[c.source, c.value, c.at] for c in self._pending],
            "sources": {
                source: {
                    "confirmed_set": st.confirmed_set,
                    "confirmed": st.confirmed,
                    "candidate": st.candidate,
                    "candidate_since": st.candidate_since,
                    "last_ts": st.last_ts,
                }
                for source, st in self._sources.items()
            },
        }

    @classmethod
    def restore(cls, state: Dict[str, Any]) -> "Debouncer":
        """从 ``save()`` 的产出恢复；恢复后行为与从未中断完全一致。"""
        if state.get("version") != cls.VERSION:
            raise ValueError(f"不支持的 checkpoint 版本：{state.get('version')!r}")
        debouncer = cls(state["threshold"])
        debouncer._now = state["now"]
        debouncer._pending = [Change(s, v, at) for s, v, at in state["pending"]]
        for source, raw in state["sources"].items():
            st = _SourceState()
            st.confirmed_set = raw["confirmed_set"]
            st.confirmed = raw["confirmed"]
            st.candidate = raw["candidate"]
            st.candidate_since = raw["candidate_since"]
            st.last_ts = raw["last_ts"]
            debouncer._sources[source] = st
        return debouncer
