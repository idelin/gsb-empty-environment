"""分段仿射映射：logical = rate * real + offset。

段类型由速率决定：rate=0 为暂停段（常量段），rate<0 为反向段，
rate>1 为倍速段，rate=1 为常速段。所有运算都是符号化的区间运算，
按断点处理，绝不逐 tick 展开，因此可以处理巨大区间。
"""
from __future__ import annotations

from dataclasses import dataclass

from .intervals import Interval, IntervalSet
from .quantities import to_fraction


@dataclass(frozen=True)
class Segment:
    domain: Interval
    rate: object
    offset: object
    seg_id: str = ""
    kind: str = "normal"  # normal | pause | speed | reverse
    parents: tuple = ()  # 复合映射中记录来源段 id，作为来源证据

    def __post_init__(self):
        object.__setattr__(self, "rate", to_fraction(self.rate))
        object.__setattr__(self, "offset", to_fraction(self.offset))

    def apply(self, x):
        return self.rate * to_fraction(x) + self.offset

    def image_of(self, iv: Interval):
        """iv 在源轴上的像（已自动与 domain 求交），保留端点开闭。"""
        sub = self.domain.intersect(iv)
        if sub is None:
            return None
        if self.rate == 0:
            return Interval.point(self.offset)
        lo_img = self.rate * sub.lo + self.offset
        hi_img = self.rate * sub.hi + self.offset
        if self.rate > 0:
            return Interval.maybe(lo_img, hi_img, sub.lo_closed, sub.hi_closed)
        return Interval.maybe(hi_img, lo_img, sub.hi_closed, sub.lo_closed)

    def preimage_of(self, iv: Interval):
        """domain 中映射进 iv 的源轴子区间。

        常量段（暂停段）命中时返回整段原像。
        """
        if self.rate == 0:
            return self.domain if iv.contains(self.offset) else None
        a = (iv.lo - self.offset) / self.rate
        b = (iv.hi - self.offset) / self.rate
        if self.rate > 0:
            cand = Interval.maybe(a, b, iv.lo_closed, iv.hi_closed)
        else:
            cand = Interval.maybe(b, a, iv.hi_closed, iv.lo_closed)
        if cand is None:
            return None
        return self.domain.intersect(cand)


@dataclass(frozen=True)
class Contribution:
    """一条映射证据：哪个段把 source 子区间映成了 target 子区间。"""

    seg_id: str
    source: Interval
    target: Interval


class MappingResult:
    """一次像/逆像查询的结果：完整区间集合 + 逐段证据。"""

    __slots__ = ("values", "evidence")

    def __init__(self, values: IntervalSet, evidence=()):
        self.values = values
        self.evidence = tuple(evidence)

    def __iter__(self):
        return iter(self.values)

    def __repr__(self):
        return f"MappingResult({self.values}, evidence={len(self.evidence)} 条)"


class PiecewiseAffineMap:
    """定义域互不相交的分段仿射映射（不可变）。"""

    def __init__(self, segments):
        segs = sorted(segments, key=lambda s: (s.domain.lo, not s.domain.lo_closed))
        for prev, nxt in zip(segs, segs[1:]):
            overlap = nxt.domain.lo < prev.domain.hi or (
                nxt.domain.lo == prev.domain.hi
                and prev.domain.hi_closed
                and nxt.domain.lo_closed
            )
            if overlap:
                raise ValueError(
                    f"overlapping segments: {prev.seg_id} / {nxt.seg_id}"
                )
        self.segments = tuple(segs)

    def segment_at(self, x):
        x = to_fraction(x)
        for seg in self.segments:
            if seg.domain.contains(x):
                return seg
        return None

    def value_at(self, x):
        seg = self.segment_at(x)
        return None if seg is None else seg.apply(x)

    def image(self, targets) -> MappingResult:
        """求源轴区间集合的像（完整区间集合 + 证据）。"""
        ivset = targets if isinstance(targets, IntervalSet) else IntervalSet(targets)
        out, ev = [], []
        for seg in self.segments:
            for iv in ivset:
                img = seg.image_of(iv)
                if img is None:
                    continue
                out.append(img)
                ev.append(Contribution(seg.seg_id, seg.domain.intersect(iv), img))
        return MappingResult(IntervalSet(out), ev)

    def preimage(self, targets) -> MappingResult:
        """求逻辑轴区间集合的逆像（完整区间集合 + 证据）。

        暂停段命中时贡献整段原像；相邻段的闭端点会作为边界选择
        一并保留在结果中（例如暂停 [5,10) 时，点 10 由后续段贡献）。
        """
        ivset = targets if isinstance(targets, IntervalSet) else IntervalSet(targets)
        out, ev = [], []
        for seg in self.segments:
            for iv in ivset:
                pre = seg.preimage_of(iv)
                if pre is None:
                    continue
                out.append(pre)
                ev.append(Contribution(seg.seg_id, pre, iv))
        return MappingResult(IntervalSet(out), ev)

    def compose(self, other: "PiecewiseAffineMap") -> "PiecewiseAffineMap":
        """映射复合：返回 h = other ∘ self，即 h(x) = other(self(x))。"""
        raw = []
        for sf in self.segments:
            if sf.rate == 0:
                sg = other.segment_at(sf.offset)
                if sg is None:
                    continue
                raw.append(
                    Segment(
                        domain=sf.domain,
                        rate=0,
                        offset=sg.apply(sf.offset),
                        seg_id=f"{sf.seg_id}>>{sg.seg_id}",
                        kind="pause",
                        parents=(sf.seg_id, sg.seg_id),
                    )
                )
                continue
            image = sf.image_of(sf.domain)
            for sg in other.segments:
                k = image.intersect(sg.domain)
                if k is None:
                    continue
                pre = sf.preimage_of(k)
                if pre is None:
                    continue
                rate = sg.rate * sf.rate
                offset = sg.rate * sf.offset + sg.offset
                if rate == 0:
                    kind = "pause"
                elif rate < 0:
                    kind = "reverse"
                else:
                    kind = "normal"
                raw.append(
                    Segment(
                        domain=pre,
                        rate=rate,
                        offset=offset,
                        seg_id=f"{sf.seg_id}>>{sg.seg_id}",
                        kind=kind,
                        parents=(sf.seg_id, sg.seg_id),
                    )
                )
        merged = []
        for seg in sorted(raw, key=lambda s: (s.domain.lo, not s.domain.lo_closed)):
            if (
                merged
                and merged[-1].rate == seg.rate
                and merged[-1].offset == seg.offset
                and merged[-1].domain.mergeable(seg.domain)
            ):
                last = merged[-1]
                merged[-1] = Segment(
                    domain=last.domain.merge(seg.domain),
                    rate=last.rate,
                    offset=last.offset,
                    seg_id=f"{last.seg_id}+{seg.seg_id}",
                    kind=last.kind,
                    parents=last.parents + seg.parents,
                )
            else:
                merged.append(seg)
        return PiecewiseAffineMap(merged)

    def offset_table(self):
        from .table import OffsetTable

        return OffsetTable(self)
