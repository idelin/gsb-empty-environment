"""交集引擎：把多条触发流编译为分段约束，惰性地产出共同触发集合。

工作流程：
1. 统一刻度：取窗口端点与全部规则中有理量的分母之 lcm，所有时间换算成
   整数 tick（任意精度，不溢出、不丢精度）。
2. 分段编译：以所有相位切换点为边界切分窗口；每个区段内各流的同余不变，
   用 CRT 合并为一条同余 (R, M)。无解的区段记入 ``reports`` 并附算术证据。
3. 暂停区间取各流之并集，从区段解集中扣除（区间运算，非逐 tick 扫描）。
4. 例外点只有在“其余每条流在该时刻也触发”时才并入共同集合——非共同
   例外不可加入。
5. ``iter_points`` 惰性产出；``count``/``kth`` 用整式计数与二分定位，
   巨大窗口也不需要逐 tick 枚举。
"""

from __future__ import annotations

import bisect
import hashlib
import heapq
from dataclasses import dataclass
from fractions import Fraction
from typing import Iterator, List, Sequence, Tuple

from .crt import Congruence, NoCommonSolution, crt_all
from .streams import Stream
from .timebase import Instant, TickScale, TimeValue, as_fraction


def _ceil_div(a: int, b: int) -> int:
    """ceil(a/b)，b > 0。"""
    return -((-a) // b)


def _merge_intervals(intervals) -> List[Tuple[int, int]]:
    merged: List[Tuple[int, int]] = []
    for a, b in sorted(intervals):
        if a >= b:
            continue
        if merged and a <= merged[-1][1]:
            if b > merged[-1][1]:
                merged[-1] = (merged[-1][0], b)
        else:
            merged.append((a, b))
    return merged


@dataclass(frozen=True)
class Checkpoint:
    """绑定所有流全部规则版本的检查点。"""

    entries: Tuple[Tuple[str, str], ...]

    @classmethod
    def from_streams(cls, streams: Sequence[Stream]) -> "Checkpoint":
        return cls(tuple(sorted((st.name, st.digest()) for st in streams)))

    def digest(self) -> str:
        h = hashlib.sha256()
        for name, stream_digest in self.entries:
            h.update(name.encode("utf-8"))
            h.update(b"\0")
            h.update(stream_digest.encode("utf-8"))
            h.update(b"\n")
        return h.hexdigest()


@dataclass(frozen=True)
class SegmentReport:
    """某个区段没有共同触发的说明（含算术证据）。"""

    start: Fraction
    end: Fraction
    detail: str

    def __str__(self) -> str:
        return f"[{self.start}, {self.end}): {self.detail}"


@dataclass(frozen=True)
class _Segment:
    lo: int
    hi: int
    residue: int
    modulus: int
    pauses: Tuple[Tuple[int, int], ...]


class Intersection:
    """多条流在时间窗 [start, end) 内的共同触发集合。"""

    def __init__(self, streams: Sequence[Stream], start: TimeValue, end: TimeValue):
        streams = tuple(streams)
        if not streams:
            raise ValueError("至少需要一条流")
        start_f, end_f = as_fraction(start), as_fraction(end)
        if not start_f < end_f:
            raise ValueError("时间窗为空")
        self.streams: Tuple[Stream, ...] = streams
        self.start = start_f
        self.end = end_f

        denominators = [start_f.denominator, end_f.denominator]
        for st in streams:
            for ph in st.phases:
                denominators.extend((ph.start.denominator, ph.period.denominator, ph.phase.denominator))
            for pa in st.pauses:
                denominators.extend((pa.start.denominator, pa.end.denominator))
            for ex in st.exceptions:
                denominators.append(ex.at.denominator)
        self.scale = TickScale(denominators)

        lo = self.scale.to_ticks(start_f)
        hi = self.scale.to_ticks(end_f)
        self._lo, self._hi = lo, hi

        self._stream_pauses: List[List[Tuple[int, int]]] = []
        self._stream_exceptions: List[set] = []
        for st in streams:
            pauses = _merge_intervals(
                (max(self.scale.to_ticks(pa.start), lo), min(self.scale.to_ticks(pa.end), hi))
                for pa in st.pauses
            )
            self._stream_pauses.append(pauses)
            self._stream_exceptions.append({self.scale.to_ticks(ex.at) for ex in st.exceptions})

        self.checkpoint = Checkpoint.from_streams(streams)
        self.reports: List[SegmentReport] = []
        self._segments: List[_Segment] = []
        self._compile(lo, hi)
        self._seg_los = [seg.lo for seg in self._segments]
        self._extras = self._collect_extra_exceptions(lo, hi)

    # ------------------------------------------------------------------ 编译

    def _compile(self, lo: int, hi: int) -> None:
        bounds = {lo, hi}
        for st in self.streams:
            for ph in st.phases:
                b = self.scale.to_ticks(ph.start)
                if lo < b < hi:
                    bounds.add(b)
        points = sorted(bounds)
        for a, b in zip(points, points[1:]):
            seg_start = self.scale.to_time(a)
            seg_end = self.scale.to_time(b)
            congruences = []
            inactive = []
            for st in self.streams:
                rule = st.active_phase(seg_start)
                if rule is None:
                    inactive.append(st.name)
                else:
                    modulus = self.scale.to_ticks(rule.period)
                    residue = self.scale.to_ticks(rule.phase) % modulus
                    congruences.append(Congruence(residue, modulus))
            if inactive:
                self.reports.append(
                    SegmentReport(seg_start, seg_end, f"流 {inactive} 在该区段无生效相位规则")
                )
                continue
            try:
                combined = crt_all(congruences)
            except NoCommonSolution as exc:
                self.reports.append(SegmentReport(seg_start, seg_end, exc.explain()))
                continue
            pauses = _merge_intervals(
                (max(x, a), min(y, b))
                for stream_pauses in self._stream_pauses
                for (x, y) in stream_pauses
                if x < b and y > a
            )
            self._segments.append(
                _Segment(a, b, combined.residue, combined.modulus, tuple(pauses))
            )

    def _stream_fires(self, index: int, t: int) -> bool:
        if t in self._stream_exceptions[index]:
            return True
        st = self.streams[index]
        rule = st.active_phase(self.scale.to_time(t))
        if rule is None:
            return False
        modulus = self.scale.to_ticks(rule.period)
        if (t - self.scale.to_ticks(rule.phase)) % modulus != 0:
            return False
        return not any(x <= t < y for x, y in self._stream_pauses[index])

    def _in_progression(self, t: int) -> bool:
        i = bisect.bisect_right(self._seg_los, t) - 1
        if i < 0:
            return False
        seg = self._segments[i]
        if t >= seg.hi or (t - seg.residue) % seg.modulus != 0:
            return False
        return not any(x <= t < y for x, y in seg.pauses)

    def _collect_extra_exceptions(self, lo: int, hi: int) -> Tuple[int, ...]:
        candidates = sorted(
            {t for excs in self._stream_exceptions for t in excs if lo <= t < hi}
        )
        extras = []
        for t in candidates:
            # 例外点必须与所有其它流相交；非共同例外不可加入
            if all(self._stream_fires(i, t) for i in range(len(self.streams))):
                if not self._in_progression(t):
                    extras.append(t)
        return tuple(extras)

    # ------------------------------------------------------------------ 计数

    @staticmethod
    def _prog_count(residue: int, modulus: int, lo: int, hi: int) -> int:
        """等差点 residue + j*modulus 落在 [lo, hi) 内的个数。"""
        if lo >= hi:
            return 0
        return max(0, _ceil_div(hi - residue, modulus) - _ceil_div(lo - residue, modulus))

    def _segment_count(self, seg: _Segment, lo: int, hi: int) -> int:
        a, b = max(lo, seg.lo), min(hi, seg.hi)
        total = self._prog_count(seg.residue, seg.modulus, a, b)
        for x, y in seg.pauses:
            total -= self._prog_count(seg.residue, seg.modulus, max(x, a), min(y, b))
        return total

    def _count_in(self, lo: int, hi: int) -> int:
        lo, hi = max(lo, self._lo), min(hi, self._hi)
        if lo >= hi:
            return 0
        total = sum(self._segment_count(seg, lo, hi) for seg in self._segments)
        total += bisect.bisect_left(self._extras, hi) - bisect.bisect_left(self._extras, lo)
        return total

    def count(self) -> int:
        """窗口内共同触发点总数（整式计算，不逐点枚举）。"""
        return self._count_in(self._lo, self._hi)

    # ------------------------------------------------------------------ 产出

    @staticmethod
    def _segment_points(seg: _Segment) -> Iterator[int]:
        j = _ceil_div(seg.lo - seg.residue, seg.modulus)
        pauses = seg.pauses
        pi = 0
        while True:
            t = seg.residue + j * seg.modulus
            if t >= seg.hi:
                return
            while pi < len(pauses) and pauses[pi][1] <= t:
                pi += 1
            if pi < len(pauses) and pauses[pi][0] <= t < pauses[pi][1]:
                j += 1
                continue
            yield t
            j += 1

    def iter_points(self) -> Iterator[Instant]:
        """惰性地产出窗口内全部共同触发点（升序）。"""
        gens = [self._segment_points(seg) for seg in self._segments]
        gens.append(iter(self._extras))
        last = None
        for t in heapq.merge(*gens):
            if t == last:
                continue
            last = t
            yield Instant(self.scale.to_time(t))

    def __iter__(self) -> Iterator[Instant]:
        return self.iter_points()

    def kth(self, k: int) -> Instant:
        """第 k 个共同触发点（0 起计）；不存在时抛 IndexError。"""
        if k < 0:
            raise IndexError("k 必须非负")
        total = self.count()
        if k >= total:
            raise IndexError(f"窗口内只有 {total} 个共同触发点，没有第 {k} 个")
        lo, hi = self._lo, self._hi - 1
        while lo < hi:
            mid = (lo + hi) // 2
            if self._count_in(self._lo, mid + 1) > k:
                hi = mid
            else:
                lo = mid + 1
        return Instant(self.scale.to_time(lo))

    def first(self) -> Instant | None:
        """第一个共同触发点；无解时返回 None（证据见 self.reports）。"""
        try:
            return self.kth(0)
        except IndexError:
            return None
