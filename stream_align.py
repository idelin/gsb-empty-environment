"""stream_align — 跨源最近采样对齐（as-of join with tolerance）。

模型
----
- 主流事件是锚点(anchor)，每条锚点独立对齐。
- 每个辅助源(source)为锚点在 [t-tol, t+tol] 窗口内选一条最近样本。
  排名键: (|dt|, time, seq) —— 距离最近优先；同距取较早时间；再同取较小 seq。
- 结果带单调递增的 version；迟到但更优的样本产生替换(upsert)事件。
- 水位线(watermark)越过 anchor.time + tol 后结果最终化(finalize)，不再改写。
- 样本回收规则: sample.time <= watermark - 2*tol 才可删除。
  证明: 未闭合锚点满足 T + tol > w 即 T > w - tol，其候选需满足
  s >= T - tol > w - 2*tol；未来的锚点同样要求 T + tol > w（否则即迟到），
  故 time <= w - 2*tol 的样本对任何现存或未来的合法锚点都不可用。

成本
----
设 S=单源存活样本数, W=窗口内候选数, A=未闭合锚点数, K=受影响的锚点数。
- add_sample / retract_sample: O(S) 插入(有序数组) + O(A) 找受影响锚点
  + 每锚点 O(log S + W) 重选最优。
- add_anchor: O(sources * (log S + W))。
- advance_watermark: O(A + S)。
- 内存: O(A * sources + S)。
若锚点规模更大，可把"找受影响锚点"换成按锚点时间的有序索引，
降为 O(log A + K)；此处保持实现直白。
"""

from __future__ import annotations

from bisect import bisect_left, bisect_right, insort
from dataclasses import dataclass, field


# ---------------------------------------------------------------- 数据模型

@dataclass(frozen=True)
class OutputEvent:
    """引擎输出。kind: 'upsert' | 'retract' | 'finalize'。"""
    kind: str
    anchor_id: str
    anchor_time: float
    matches: dict          # source -> sample_id | None
    missing: tuple         # 无匹配(缺失)的 source
    version: int
    finalized: bool = False


@dataclass
class _AnchorState:
    anchor_id: str
    time: float
    seq: int
    version: int = 0
    matches: dict = field(default_factory=dict)   # source -> sample_id | None


class _SourceBuffer:
    """单个辅助源的存活样本：by_id 索引 + 按时间有序的平行数组。"""

    __slots__ = ("times", "entries", "by_id")

    def __init__(self) -> None:
        self.times: list[float] = []
        self.entries: list[tuple] = []   # (time, seq, sample_id)，与 times 同步
        self.by_id: dict[str, tuple] = {}

    def add(self, time: float, seq: int, sample_id: str) -> None:
        idx = bisect_right(self.times, time)
        self.times.insert(idx, time)
        self.entries.insert(idx, (time, seq, sample_id))
        self.by_id[sample_id] = (time, seq)

    def remove(self, sample_id: str) -> tuple | None:
        rec = self.by_id.pop(sample_id, None)
        if rec is None:
            return None
        time, seq = rec
        idx = bisect_left(self.times, time)
        while self.entries[idx] != (time, seq, sample_id):
            idx += 1
        del self.times[idx]
        del self.entries[idx]
        return rec

    def best_in_window(self, center: float, tol: float):
        """窗口内最优样本 id；排名键 (|dt|, time, seq, id)。"""
        lo = bisect_left(self.times, center - tol)
        hi = bisect_right(self.times, center + tol)
        best_key = None
        best_id = None
        for i in range(lo, hi):
            t, seq, sid = self.entries[i]
            key = (abs(t - center), t, seq, sid)
            if best_key is None or key < best_key:
                best_key = key
                best_id = sid
        return best_id

    def drop_before(self, threshold: float) -> int:
        """删除 time <= threshold 的样本，返回删除数。"""
        n = bisect_right(self.times, threshold)
        if n == 0:
            return 0
        for t, seq, sid in self.entries[:n]:
            del self.by_id[sid]
        del self.times[:n]
        del self.entries[:n]
        return n


# ---------------------------------------------------------------- 增量引擎

class AlignmentEngine:
    """逐条事件驱动的对齐引擎。所有方法返回输出事件列表。"""

    def __init__(self, tolerance: float, sources: list[str]):
        if tolerance < 0:
            raise ValueError("tolerance must be >= 0")
        self.tolerance = tolerance
        self.sources = list(sources)
        self._buffers = {s: _SourceBuffer() for s in self.sources}
        self._anchors: dict[str, _AnchorState] = {}
        self.watermark = float("-inf")
        # 统计：迟到/无效事件
        self.late_anchors = 0
        self.late_samples = 0
        self.ignored_retracts = 0
        self.gc_dropped = 0

    # -- 内部 ------------------------------------------------------

    def _emit(self, st: _AnchorState, kind: str) -> OutputEvent:
        missing = tuple(s for s in self.sources if st.matches.get(s) is None)
        return OutputEvent(
            kind=kind,
            anchor_id=st.anchor_id,
            anchor_time=st.time,
            matches=dict(st.matches),
            missing=missing,
            version=st.version,
            finalized=(kind == "finalize"),
        )

    def _compute_matches(self, time: float) -> dict:
        return {
            s: self._buffers[s].best_in_window(time, self.tolerance)
            for s in self.sources
        }

    def _refresh_anchor(self, st: _AnchorState, source: str) -> OutputEvent | None:
        """重算某锚点在指定源上的最优样本，变化则升版本并发 upsert。"""
        new_best = self._buffers[source].best_in_window(st.time, self.tolerance)
        if new_best == st.matches.get(source):
            return None
        st.matches[source] = new_best
        st.version += 1
        return self._emit(st, "upsert")

    # -- 输入接口 --------------------------------------------------

    def add_anchor(self, anchor_id: str, time: float, seq: int = 0) -> list[OutputEvent]:
        if anchor_id in self._anchors:
            raise ValueError(f"duplicate anchor {anchor_id!r}")
        if time + self.tolerance <= self.watermark:
            self.late_anchors += 1          # 到达即已最终化，直接拒绝
            return []
        st = _AnchorState(anchor_id=anchor_id, time=time, seq=seq)
        st.matches = self._compute_matches(time)
        st.version = 1
        self._anchors[anchor_id] = st
        return [self._emit(st, "upsert")]

    def retract_anchor(self, anchor_id: str) -> list[OutputEvent]:
        st = self._anchors.pop(anchor_id, None)
        if st is None:                      # 未知或已最终化
            self.ignored_retracts += 1
            return []
        return [OutputEvent("retract", anchor_id, st.time, {}, (), st.version)]

    def add_sample(self, source: str, sample_id: str, time: float, seq: int = 0) -> list[OutputEvent]:
        buf = self._buffers[source]
        if sample_id in buf.by_id:
            raise ValueError(f"duplicate sample {sample_id!r}")
        if time <= self.watermark - 2 * self.tolerance:
            self.late_samples += 1          # 对任何现存/未来锚点都无用
            return []
        buf.add(time, seq, sample_id)
        events = []
        for st in self._anchors.values():
            if abs(st.time - time) <= self.tolerance:
                to_emit = self._refresh_anchor(st, source)
                # 注：仅窗口内锚点可能受影响
                if to_emit is not None:
                    events.append(to_emit)
        return events

    def retract_sample(self, source: str, sample_id: str) -> list[OutputEvent]:
        buf = self._buffers.setdefault(source, _SourceBuffer())
        if buf.remove(sample_id) is None:
            self.ignored_retracts += 1
            return []
        events = []
        for st in self._anchors.values():
            if st.matches.get(source) == sample_id:
                # 只有当前最优被撤走的锚点才需要回退重选
                to_emit = self._refresh_anchor(st, source)
                if to_emit is not None:
                    events.append(to_emit)
        return events

    def advance_watermark(self, watermark: float) -> list[OutputEvent]:
        if watermark < self.watermark:
            raise ValueError("watermark must be monotonic")
        self.watermark = watermark
        events = []
        for aid, st in list(self._anchors.items()):
            if st.time + self.tolerance <= watermark:
                events.append(self._emit(st, "finalize"))
                del self._anchors[aid]
        threshold = watermark - 2 * self.tolerance
        for buf in self._buffers.values():
            self.gc_dropped += buf.drop_before(threshold)
        return events

    # -- 只读视图 --------------------------------------------------

    def open_anchor_ids(self) -> list[str]:
        return sorted(self._anchors)

    def live_sample_count(self) -> int:
        return sum(len(b.by_id) for b in self._buffers.values())


# ---------------------------------------------------------------- 批量接口

def align_batch(anchors: list[tuple], samples_by_source: dict[str, list[tuple]],
                tolerance: float, sources: list[str] | None = None) -> list[OutputEvent]:
    """一次性批量对齐。

    anchors: [(anchor_id, time, seq), ...]
    samples_by_source: {source: [(sample_id, time, seq), ...]}
    返回按锚点排序的 finalize 结果（version=1）。
    """
    if sources is None:
        sources = sorted(samples_by_source)
    rows = []
    for aid, atime, _seq in sorted(anchors, key=lambda a: (a[1], a[0])):
        matches = {}
        for src in sources:
            best_key = None
            best_id = None
            for sid, stime, sseq in samples_by_source.get(src, []):
                if abs(stime - atime) > tolerance:
                    continue
                key = (abs(stime - atime), stime, sseq, sid)
                if best_key is None or key < best_key:
                    best_key, best_id = key, sid
            matches[src] = best_id
        missing = tuple(s for s in sources if matches[s] is None)
        rows.append(OutputEvent(
            kind="finalize",
            anchor_id=aid,
            anchor_time=atime,
            matches=matches,
            missing=missing,
            version=1,
            finalized=True,
        ))
    return rows


# ---------------------------------------------------------------- 独立扫描参考

def reference_align(anchors, samples_by_source, tolerance, sources=None):
    """刻意朴素的独立实现：每个锚点全表扫描，供测试交叉核对。"""
    if sources is None:
        sources = sorted(samples_by_source)
    table = {}
    for aid, atime, _seq in anchors:
        matches = {}
        for src in sources:
            best = None
            for sid, stime, sseq in samples_by_source.get(src, []):
                d = abs(stime - atime)
                if d > tolerance:
                    continue
                cand = (d, stime, sseq, sid)
                if best is None or cand < best[0]:
                    best = (cand, sid)
            matches[src] = best[1] if best else None
        table[aid] = {
            "anchor_time": atime,
            "matches": matches,
            "missing": tuple(s for s in sources if matches[s] is None),
        }
    return table


# ---------------------------------------------------------------- 消费方

class AlignmentConsumer:
    """消费输出事件流，恢复"当前对齐表"。

    - upsert:   覆盖该行（version 单调递增，旧版本直接丢弃）
    - retract:  删除该行（锚点被撤销）
    - finalize: 写入最终内容并打 finalized 标记
    """

    def __init__(self):
        self.table = {}   # anchor_id -> row dict

    def apply(self, ev: OutputEvent) -> None:
        if ev.kind == "retract":
            self.table.pop(ev.anchor_id, None)
            return
        cur = self.table.get(ev.anchor_id)
        if cur is not None and cur["version"] > ev.version:
            return        # 乱序/重复的旧版本
        self.table[ev.anchor_id] = {
            "anchor_time": ev.anchor_time,
            "matches": dict(ev.matches),
            "missing": ev.missing,
            "version": ev.version,
            "finalized": ev.finalized,
        }

    def apply_all(self, events) -> None:
        for ev in events:
            self.apply(ev)

    def snapshot(self):
        return {aid: dict(row) for aid, row in self.table.items()}
