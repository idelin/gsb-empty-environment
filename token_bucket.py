"""可修订的令牌桶回放引擎。

语义约定
--------
* 时间轴上使用 ``Fraction`` 精确有理数，任何数值计算都不会积累浮点漂移。
* 配置由若干 ``Segment(start, capacity, rate)`` 组成，按 ``start`` 升序；
  每段生效区间为 ``[start, 下一段 start)``，首段向 ``-inf`` 延伸，
  末段向 ``+inf`` 延伸。段边界上发生容量收缩时立即截断桶内令牌。
* 事件按 ``(time, seq)`` 全序排列；同一时刻的请求按 ``seq`` 依次判定。
* 初始状态：在首个事件时刻桶是满的（等于该时刻生效段的容量）。
* 每次判定消费 1 个令牌：``level >= 1`` 放行，否则拒绝。

修订模型
--------
``add_request`` / ``replace_config`` 返回 ``RevisionRange``：
事件序列中实际被重算的最小连续下标区间（闭区间）。区间之外的结果
保持原对象身份与版本号不变。重算采用提前停止：令牌桶的未来演化只
取决于 ``(当前时刻, 当前水位, 未来配置, 未来事件)``，因此当某个事件
重算后的事前/事后水位与旧记录逐位相同、且从该时刻起新旧配置一致时，
后续所有结果必然逐位相同，可以立即停止。
"""

from __future__ import annotations

from bisect import bisect_right
from dataclasses import dataclass
from fractions import Fraction
from math import inf
from typing import Optional, Sequence, Tuple, Union

Number = Union[int, Fraction]

__all__ = [
    "Segment",
    "Config",
    "Request",
    "Decision",
    "RevisionRange",
    "TokenBucketEngine",
]


def _fr(value) -> Fraction:
    if isinstance(value, Fraction):
        return value
    return Fraction(value)


# ---------------------------------------------------------------------------
# 配置
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class Segment:
    """一段配置：从 ``start``（含）起生效，容量 ``capacity``，速率 ``rate``。"""

    start: Fraction
    capacity: Fraction
    rate: Fraction

    def __init__(self, start, capacity, rate):
        object.__setattr__(self, "start", _fr(start))
        object.__setattr__(self, "capacity", _fr(capacity))
        object.__setattr__(self, "rate", _fr(rate))


class Config:
    """分段令牌桶配置（不可变）。"""

    def __init__(self, segments: Sequence[Union[Segment, tuple]]):
        segs = [
            s if isinstance(s, Segment) else Segment(*s) for s in segments
        ]
        if not segs:
            raise ValueError("config needs at least one segment")
        segs.sort(key=lambda s: s.start)
        for prev, cur in zip(segs, segs[1:]):
            if cur.start <= prev.start:
                raise ValueError("segment starts must be strictly increasing")
        for s in segs:
            if s.capacity < 0 or s.rate < 0:
                raise ValueError("capacity and rate must be non-negative")
        self._segments: Tuple[Segment, ...] = tuple(segs)
        self._starts: Tuple[Fraction, ...] = tuple(s.start for s in segs)

    @property
    def segments(self) -> Tuple[Segment, ...]:
        return self._segments

    def segment_index(self, t: Fraction) -> int:
        idx = bisect_right(self._starts, t) - 1
        return max(idx, 0)

    def at(self, t: Fraction) -> Segment:
        return self._segments[self.segment_index(t)]

    def capacity_at(self, t: Fraction) -> Fraction:
        return self.at(t).capacity

    def advance(self, level: Fraction, t_from: Fraction, t_to: Fraction) -> Fraction:
        """把水位 ``level`` 从 ``t_from`` 推进到 ``t_to``（含沿途封顶/截断）。

        每进入一个段先按该段容量截断（处理容量收缩），再按速率线性补充并
        封顶；最后在 ``t_to`` 时刻再按该时刻生效容量截断一次，保证边界
        时刻的请求看到收缩后的水位。
        """
        if t_to < t_from:
            raise ValueError("t_to must be >= t_from")
        idx = self.segment_index(t_from)
        cur = t_from
        while True:
            seg = self._segments[idx]
            level = min(level, seg.capacity)
            if cur >= t_to:
                break
            nxt = self._segments[idx + 1].start if idx + 1 < len(self._segments) else t_to
            end = min(nxt, t_to)
            level = min(seg.capacity, level + seg.rate * (end - cur))
            if end >= t_to:
                break
            cur = end
            idx += 1
        return min(level, self.capacity_at(t_to))

    def diff_interval(self, other: "Config"):
        """返回 ``(diff_start, frozen_from)``。

        两个配置作为时间函数在 ``[diff_start, frozen_from)`` 上可能不同，
        在 ``frozen_from`` 及之后逐点相同。无差异时返回 ``(None, None)``；
        差异延伸到无穷远时 ``frozen_from`` 为 ``math.inf``。
        """
        bounds = sorted(set(self._starts) | set(other._starts))
        edges = [bounds[0] - 1] + bounds + [bounds[-1] + 2]
        diff_start = None
        frozen_from = None
        for left, right in zip(edges, edges[1:]):
            sample = (left + right) / 2
            a, b = self.at(sample), other.at(sample)
            if (a.capacity, a.rate) != (b.capacity, b.rate):
                lo = left if left in bounds else -inf
                if diff_start is None:
                    diff_start = lo
                frozen_from = right if right in bounds else inf
        if diff_start is None:
            return None, None
        return diff_start, frozen_from


# ---------------------------------------------------------------------------
# 记录
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class Request:
    request_id: int
    time: Fraction
    seq: int


@dataclass(frozen=True)
class Decision:
    """一次判定的完整结果。``version`` 由引擎单调分配，重写即换新版本。"""

    request_id: int
    time: Fraction
    seq: int
    level_before: Fraction
    allowed: bool
    reason: str
    level_after: Fraction
    version: int


@dataclass(frozen=True)
class RevisionRange:
    """一次修订实际重算的最小连续下标区间（闭区间，基于修订后的事件序）。"""

    start: Optional[int]
    end: Optional[int]
    recomputed_ids: Tuple[int, ...]
    stopped_early: bool
    note: str

    @property
    def is_empty(self) -> bool:
        return self.start is None

    @classmethod
    def empty(cls, note: str) -> "RevisionRange":
        return cls(None, None, (), False, note)


# ---------------------------------------------------------------------------
# 引擎
# ---------------------------------------------------------------------------

class TokenBucketEngine:
    """可修订的令牌桶回放引擎。"""

    def __init__(self, segments: Sequence[Union[Segment, tuple]]):
        self._config = segments if isinstance(segments, Config) else Config(segments)
        self._events: list[Request] = []
        self._records: list[Optional[Decision]] = []
        self._version = 0
        self._next_id = 0

    # -- 只读视图 ----------------------------------------------------------

    @property
    def config(self) -> Config:
        return self._config

    @property
    def events(self) -> Tuple[Request, ...]:
        return tuple(self._events)

    def results(self) -> list[Decision]:
        return list(self._records)

    # -- 变更入口 ----------------------------------------------------------

    def add_request(self, time, seq: Optional[int] = None) -> Tuple[int, RevisionRange]:
        """补录一条（可能迟到的）请求，返回 ``(request_id, RevisionRange)``。"""
        t = _fr(time)
        if seq is None:
            seqs = [e.seq for e in self._events if e.time == t]
            seq = max(seqs) + 1 if seqs else 0
        if any(e.time == t and e.seq == seq for e in self._events):
            raise ValueError(f"duplicate (time={t}, seq={seq})")
        req = Request(self._next_id, t, seq)
        self._next_id += 1
        keys = [(e.time, e.seq) for e in self._events]
        pos = bisect_right(keys, (t, seq))
        self._events.insert(pos, req)
        self._records.insert(pos, None)
        rng = self._revise(pos, -inf)
        return req.request_id, RevisionRange(
            rng.start, rng.end, rng.recomputed_ids, rng.stopped_early,
            f"inserted request id={req.request_id} at index {pos}",
        )

    def replace_config(self, segments: Sequence[Union[Segment, tuple]]) -> RevisionRange:
        """整体替换配置，只重算新旧配置差异区间所覆盖的事件。"""
        new = segments if isinstance(segments, Config) else Config(segments)
        diff_start, frozen_from = self._config.diff_interval(new)
        if diff_start is None:
            return RevisionRange.empty("config unchanged")
        self._config = new
        start = 0
        while start < len(self._events) and self._events[start].time < diff_start:
            start += 1
        if start == len(self._events):
            return RevisionRange.empty("no events at/after first config difference")
        return self._revise(start, frozen_from)

    # -- 核心：增量修订 ----------------------------------------------------

    def _next_version(self) -> int:
        self._version += 1
        return self._version

    def _revise(self, start: int, frozen_from) -> RevisionRange:
        """从下标 ``start`` 起重放，收敛即停。

        提前停止条件（充分）：当前事件重算后的事前水位、事后水位与旧记录
        逐位相等，且 ``event.time >= frozen_from``（此后新旧配置逐点相同）。
        令牌桶状态转移只依赖 (time, level, config, 后续事件)，故此后所有
        结果必然逐位相同，旧记录保持身份与版本。
        """
        if start == 0:
            last_time = self._events[0].time
            level = self._config.capacity_at(last_time)
        else:
            prev = self._records[start - 1]
            last_time, level = prev.time, prev.level_after

        i = start
        stopped_early = False
        while i < len(self._events):
            ev = self._events[i]
            level_before = self._config.advance(level, last_time, ev.time)
            allowed = level_before >= 1
            level_after = level_before - 1 if allowed else level_before
            old = self._records[i]
            if (
                old is not None
                and old.request_id == ev.request_id
                and old.level_before == level_before
                and old.level_after == level_after
                and ev.time >= frozen_from
            ):
                stopped_early = True
                break
            reason = (
                f"allowed: level {level_before} >= 1, consumed 1 token"
                if allowed
                else f"rejected: level {level_before} < 1 token"
            )
            self._records[i] = Decision(
                request_id=ev.request_id,
                time=ev.time,
                seq=ev.seq,
                level_before=level_before,
                allowed=allowed,
                reason=reason,
                level_after=level_after,
                version=self._next_version(),
            )
            last_time, level = ev.time, level_after
            i += 1

        end = i - 1
        if end < start:
            return RevisionRange.empty("recomputation converged immediately")
        return RevisionRange(
            start=start,
            end=end,
            recomputed_ids=tuple(e.request_id for e in self._events[start : end + 1]),
            stopped_early=stopped_early,
            note="",
        )
