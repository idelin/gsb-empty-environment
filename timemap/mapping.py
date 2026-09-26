"""Piecewise-affine maps from real time to logical time.

A map is a chain of segments; each segment applies logical = slope * real + offset
on its own domain interval.  slope == 0 is a pause (constant segment),
slope < 0 a reverse segment, any other value a (possibly sped-up) play segment.

Images and pre-images are computed as complete IntervalSets over segment
breakpoints only -- never by enumerating ticks -- so huge domains are cheap.
A constant (pause) segment yields its whole domain as the pre-image of its
value, including boundary choices at both ends.
"""
from __future__ import annotations

from bisect import bisect_right
from dataclasses import dataclass
from fractions import Fraction
from typing import Iterable

from .intervals import Interval, IntervalSet
from .types import exact


@dataclass(frozen=True)
class Evidence:
    """Provenance record attached to segments and revisions."""

    source: str
    detail: str = ""

    def __str__(self) -> str:
        return f"{self.source}: {self.detail}" if self.detail else self.source


@dataclass(frozen=True)
class Segment:
    start: Fraction
    end: Fraction
    slope: Fraction
    offset: Fraction
    start_open: bool = False
    end_open: bool = True
    evidence: tuple[Evidence, ...] = ()

    def __post_init__(self) -> None:
        for name in ("start", "end", "slope", "offset"):
            object.__setattr__(self, name, exact(getattr(self, name)))
        if not self.start < self.end:
            raise ValueError("segment must have positive length")
        object.__setattr__(self, "evidence", tuple(self.evidence))

    def value_at(self, t) -> Fraction:
        return self.slope * exact(t) + self.offset

    @property
    def is_pause(self) -> bool:
        return self.slope == 0

    @property
    def kind(self) -> str:
        if self.slope == 0:
            return "pause"
        if self.slope < 0:
            return "reverse"
        if self.slope == 1:
            return "play"
        return f"speed x{self.slope}"

    def domain(self) -> Interval:
        return Interval(self.start, self.end, self.start_open, self.end_open)

    def image(self) -> Interval:
        """Image of the whole segment domain."""
        a = self.value_at(self.start)
        b = self.value_at(self.end)
        if self.slope > 0:
            return Interval(a, b, self.start_open, self.end_open)
        if self.slope < 0:
            return Interval(b, a, self.end_open, self.start_open)
        return Interval.point(a)

    def image_of(self, iv: Interval) -> Interval:
        """Image of a sub-interval of the domain (requires slope != 0)."""
        a = self.value_at(iv.lo)
        b = self.value_at(iv.hi)
        if self.slope > 0:
            return Interval(a, b, iv.lo_open, iv.hi_open)
        return Interval(b, a, iv.hi_open, iv.lo_open)

    def preimage_of(self, iv: Interval) -> Interval:
        """Pre-image of a value sub-interval (requires slope != 0)."""
        a = (iv.lo - self.offset) / self.slope
        b = (iv.hi - self.offset) / self.slope
        if self.slope > 0:
            return Interval(a, b, iv.lo_open, iv.hi_open)
        return Interval(b, a, iv.hi_open, iv.lo_open)

    def __str__(self) -> str:
        return f"{self.domain()} {self.kind}: logical = {self.slope}*real + {self.offset}"


def _merge_affine(segments: Iterable[Segment]) -> list[Segment]:
    """Join adjacent segments that share one affine function."""
    ordered = sorted(segments, key=lambda s: (s.start, s.start_open))
    out: list[Segment] = []
    for seg in ordered:
        if out:
            prev = out[-1]
            if (
                prev.end == seg.start
                and prev.slope == seg.slope
                and prev.offset == seg.offset
                and not (prev.end_open and seg.start_open)
            ):
                evidence = prev.evidence + tuple(
                    e for e in seg.evidence if e not in prev.evidence
                )
                out[-1] = Segment(
                    prev.start, seg.end, prev.slope, prev.offset,
                    prev.start_open, seg.end_open, evidence,
                )
                continue
        out.append(seg)
    return out


class PiecewiseMap:
    """A piecewise-affine map on a closed real-time domain."""

    def __init__(self, segments: Iterable[Segment]):
        segs = sorted(segments, key=lambda s: (s.start, s.start_open))
        if not segs:
            raise ValueError("a map needs at least one segment")
        for prev, nxt in zip(segs, segs[1:]):
            if prev.end != nxt.start:
                raise ValueError(
                    f"domain gap or overlap between {prev.end} and {nxt.start}"
                )
            if (not prev.end_open) == (not nxt.start_open):
                raise ValueError(
                    f"boundary {prev.end} must be owned by exactly one side"
                )
        if segs[0].start_open or segs[-1].end_open:
            raise ValueError("the outer domain must be closed at both ends")
        self._segments = tuple(segs)
        self._starts = [s.start for s in segs]

    @classmethod
    def build(cls, start, start_value, pieces, evidence=()) -> "PiecewiseMap":
        """Build a continuous map from (duration, slope) pieces."""
        items = [(exact(d), exact(s)) for d, s in pieces]
        if not items:
            raise ValueError("no pieces given")
        segs = []
        t = exact(start)
        v = exact(start_value)
        for i, (dur, slope) in enumerate(items):
            if dur <= 0:
                raise ValueError("durations must be positive")
            segs.append(Segment(
                t, t + dur, slope, v - slope * t,
                end_open=i < len(items) - 1, evidence=tuple(evidence),
            ))
            v = v + slope * dur
            t = t + dur
        return cls(segs)

    @property
    def segments(self) -> tuple[Segment, ...]:
        return self._segments

    def domain(self) -> Interval:
        return Interval(self._segments[0].start, self._segments[-1].end, False, False)

    def boundaries(self) -> list[Fraction]:
        pts = {s.start for s in self._segments} | {s.end for s in self._segments}
        return sorted(pts)

    def segment_at(self, t) -> Segment:
        t = exact(t)
        idx = bisect_right(self._starts, t) - 1
        for cand in (idx, idx - 1):
            if 0 <= cand < len(self._segments):
                seg = self._segments[cand]
                if seg.domain().contains(t):
                    return seg
        raise KeyError(f"{t} is outside map domain {self.domain()}")

    def eval(self, t) -> Fraction:
        seg = self.segment_at(t)
        return seg.value_at(t)

    def image(self, iv: Interval) -> IntervalSet:
        """Complete image of a real-time interval, as an IntervalSet."""
        out = []
        for seg in self._segments:
            cut = iv.intersect(seg.domain())
            if cut is None:
                continue
            if seg.is_pause:
                out.append(Interval.point(seg.value_at(seg.start)))
            else:
                out.append(seg.image_of(cut))
        return IntervalSet(out)

    def preimage(self, iv: Interval) -> IntervalSet:
        """Complete pre-image of a logical-time interval, as an IntervalSet.

        Constant (pause) segments contribute their entire domain when their
        value lies in `iv`, so boundary choices are preserved.
        """
        out = []
        for seg in self._segments:
            if seg.is_pause:
                if iv.contains(seg.value_at(seg.start)):
                    out.append(seg.domain())
                continue
            hit = iv.intersect(seg.image())
            if hit is not None:
                out.append(seg.preimage_of(hit))
        return IntervalSet(out)

    def compose(self, other: "PiecewiseMap") -> "PiecewiseMap":
        """Return other ∘ self (self: A->B, other: B->C) as a map A->C."""
        out: list[Segment] = []
        for seg in self._segments:
            if seg.is_pause:
                c = seg.value_at(seg.start)
                gseg = other.segment_at(c)
                out.append(Segment(
                    seg.start, seg.end, 0, gseg.value_at(c),
                    seg.start_open, seg.end_open, seg.evidence + gseg.evidence,
                ))
                continue
            img = seg.image()
            for gseg in other._segments:
                hit = img.intersect(gseg.domain())
                if hit is None:
                    continue
                pre = seg.preimage_of(hit)
                out.append(Segment(
                    pre.lo, pre.hi,
                    gseg.slope * seg.slope,
                    gseg.slope * seg.offset + gseg.offset,
                    pre.lo_open, pre.hi_open,
                    seg.evidence + gseg.evidence,
                ))
        return PiecewiseMap(_merge_affine(out))

    def cut(self, points) -> list[Segment]:
        """Split every segment at the given interior points."""
        pts = sorted({exact(p) for p in points})
        out = []
        for seg in self._segments:
            inner = [p for p in pts if seg.start < p < seg.end]
            bounds = [seg.start, *inner, seg.end]
            last = len(bounds) - 2
            for i, (a, b) in enumerate(zip(bounds, bounds[1:])):
                out.append(Segment(
                    a, b, seg.slope, seg.offset,
                    seg.start_open if i == 0 else False,
                    seg.end_open if i == last else True,
                    seg.evidence,
                ))
        return out

    def replace_range(self, start, end, spec, evidence=()) -> "PiecewiseMap":
        """Local revision: replace [start, end) by new (duration, slope) pieces.

        The replacement continues the value at `start`; everything after `end`
        is shifted by the resulting delta so the map stays continuous.
        """
        start = exact(start)
        end = exact(end)
        dom = self.domain()
        if not (dom.lo <= start < end <= dom.hi):
            raise ValueError("replacement range must lie inside the domain")
        spec = [(exact(d), exact(s)) for d, s in spec]
        if not spec or sum(d for d, _ in spec) != end - start:
            raise ValueError("replacement durations must sum to the range length")
        pieces = self.cut([start, end])
        left_owner = any(p.end == start and not p.end_open for p in pieces)
        right_owner = any(p.start == end and not p.start_open for p in pieces)
        new_segs = []
        t = start
        v = self.eval(start)
        for i, (dur, slope) in enumerate(spec):
            new_segs.append(Segment(
                t, t + dur, slope, v - slope * t,
                start_open=i == 0 and left_owner,
                end_open=True, evidence=tuple(evidence),
            ))
            v = v + slope * dur
            t = t + dur
        last = new_segs[-1]
        new_segs[-1] = Segment(
            last.start, last.end, last.slope, last.offset,
            last.start_open, right_owner, last.evidence,
        )
        delta = v - self.eval(end)
        out = []
        for p in pieces:
            if p.end <= start:
                out.append(p)
            elif p.start >= end:
                out.append(Segment(
                    p.start, p.end, p.slope, p.offset + delta,
                    p.start_open, p.end_open, p.evidence,
                ))
        out.extend(new_segs)
        return PiecewiseMap(out)

    def __str__(self) -> str:
        return "\n".join(str(s) for s in self._segments)
