"""Lazy exact intersection engine over compiled piecewise constraints.

The engine never enumerates ticks. Each compiled segment contributes an
arithmetic progression (via CRT) plus a handful of explicit exception
points, so ``count``/``kth`` are O(#segments + #exceptions) even for
astronomically large windows, and ``iter_common`` yields lazily.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .checkpoint import Checkpoint
from .compiler import compile_segments
from .congruence import ceil_div, common_scale, crt_all
from .time_model import TimePoint


@dataclass(frozen=True)
class SegmentEvidence:
    """Why one compiled segment cannot contribute common triggers."""

    segment: tuple[Fraction, Fraction]
    kind: str  # "paused-stream" | "crt-conflict"
    detail: str


@dataclass(frozen=True)
class NoSolutionReport:
    """Arithmetic evidence that a window contains no common trigger."""

    window: tuple[Fraction, Fraction]
    evidence: tuple[SegmentEvidence, ...]

    def __str__(self) -> str:
        lines = [f"no common trigger in [{self.window[0]}, {self.window[1]}):"]
        for item in self.evidence:
            lines.append(
                f"  [{item.segment[0]}, {item.segment[1]}) {item.kind}: {item.detail}"
            )
        return "\n".join(lines)


class Engine:
    """Intersect several :class:`PeriodicStream` objects exactly and lazily."""

    def __init__(self, streams, checkpoint: Checkpoint | None = None):
        self.streams = list(streams)
        if not self.streams:
            raise ValueError("engine needs at least one stream")
        names = [s.name for s in self.streams]
        if len(set(names)) != len(names):
            raise ValueError("stream names must be unique")
        self.checkpoint = checkpoint
        self._verify()

    # -- preparation ----------------------------------------------------
    def _verify(self) -> None:
        if self.checkpoint is not None:
            self.checkpoint.verify(self.streams)

    def _scale(self, lo: Fraction, hi: Fraction) -> int:
        values = [lo, hi]
        for stream in self.streams:
            values.append(stream.period)
            values.append(stream.phase)
            for pause in stream.pauses:
                values.extend((pause.start, pause.end))
            for switch in stream.switches:
                values.extend((switch.at, switch.phase))
            values.extend(stream.exceptions)
        return common_scale(*values)

    def _common_exceptions(self, lo: Fraction, hi: Fraction, scale: int) -> list[int]:
        """Exception points that also trigger every other stream (scaled)."""
        found = set()
        for stream in self.streams:
            for point in stream.exceptions:
                if not lo <= point < hi:
                    continue
                if all(
                    other is stream or other.is_trigger(point)
                    for other in self.streams
                ):
                    found.add(int(point * scale))
        return sorted(found)

    @staticmethod
    def _segment_ap(segment):
        """Combined congruence of one segment.

        Returns ``((residue, modulus, effective_lo), None)``,
        ``(None, CrtConflict)`` on a CRT conflict, or ``(None, None)``
        when some stream is paused (no periodic common points).
        """
        active = [c for c in segment.constraints if not c.paused]
        if len(active) != len(segment.constraints):
            return None, None
        solution, conflict = crt_all(
            [(c.anchor % c.period, c.period, c.name) for c in active]
        )
        if conflict is not None:
            return None, conflict
        residue, modulus = solution
        effective_lo = max(segment.lo, max(c.anchor for c in active))
        return (residue, modulus, effective_lo), None

    # -- per-segment combinatorics --------------------------------------
    @staticmethod
    def _count_in_segment(segment, ap, segment_exceptions) -> int:
        total = 0
        on_ap = 0
        if ap is not None:
            residue, modulus, effective_lo = ap
            first = ceil_div(effective_lo - residue, modulus)
            last = ceil_div(segment.hi - residue, modulus)
            total = max(0, last - first)
            for point in segment_exceptions:
                if point >= effective_lo and (point - residue) % modulus == 0:
                    on_ap += 1
        return total + len(segment_exceptions) - on_ap

    @staticmethod
    def _merge_segment(segment, ap, segment_exceptions, scale):
        exceptions = iter(sorted(segment_exceptions))
        point = next(exceptions, None)

        def drain_below(limit):
            nonlocal point
            while point is not None and point < limit:
                yield TimePoint(Fraction(point, scale))
                point = next(exceptions, None)

        if ap is not None:
            residue, modulus, effective_lo = ap
            index = ceil_div(effective_lo - residue, modulus)
            term = residue + index * modulus
            while term < segment.hi:
                yield from drain_below(term)
                if point == term:
                    point = next(exceptions, None)
                yield TimePoint(Fraction(term, scale))
                index += 1
                term = residue + index * modulus
        yield from drain_below(segment.hi + 1)

    @staticmethod
    def _kth_in_segment(segment, ap, segment_exceptions, k) -> int:
        if ap is None:
            return segment_exceptions[k]
        residue, modulus, effective_lo = ap
        index = ceil_div(effective_lo - residue, modulus)
        remaining = k
        for point in segment_exceptions:
            upto = ceil_div(point - residue, modulus)
            below = max(0, upto - index)
            if remaining < below:
                return residue + (index + remaining) * modulus
            remaining -= below
            index = max(index, upto)
            if residue + index * modulus == point:
                if remaining == 0:
                    return point
                remaining -= 1
                index += 1
            else:
                if remaining == 0:
                    return point
                remaining -= 1
        return residue + (index + remaining) * modulus

    # -- public queries ---------------------------------------------------
    def iter_common(self, lo, hi):
        """Lazily yield every common trigger TimePoint in ``[lo, hi)``."""
        self._verify()
        lo, hi = Fraction(lo), Fraction(hi)
        scale = self._scale(lo, hi)
        exceptions = self._common_exceptions(lo, hi, scale)
        for segment in compile_segments(self.streams, lo, hi, scale):
            ap, _ = self._segment_ap(segment)
            segment_exceptions = [
                e for e in exceptions if segment.lo <= e < segment.hi
            ]
            yield from self._merge_segment(segment, ap, segment_exceptions, scale)

    def count(self, lo, hi) -> int:
        """Number of common triggers in ``[lo, hi)`` without enumerating them."""
        self._verify()
        lo, hi = Fraction(lo), Fraction(hi)
        scale = self._scale(lo, hi)
        exceptions = self._common_exceptions(lo, hi, scale)
        total = 0
        for segment in compile_segments(self.streams, lo, hi, scale):
            ap, _ = self._segment_ap(segment)
            segment_exceptions = [
                e for e in exceptions if segment.lo <= e < segment.hi
            ]
            total += self._count_in_segment(segment, ap, segment_exceptions)
        return total

    def kth(self, k, lo, hi) -> TimePoint:
        """The k-th (0-based) common trigger in ``[lo, hi)``."""
        self._verify()
        if k < 0:
            raise ValueError("k must be non-negative")
        lo, hi = Fraction(lo), Fraction(hi)
        scale = self._scale(lo, hi)
        exceptions = self._common_exceptions(lo, hi, scale)
        remaining = k
        for segment in compile_segments(self.streams, lo, hi, scale):
            ap, _ = self._segment_ap(segment)
            segment_exceptions = [
                e for e in exceptions if segment.lo <= e < segment.hi
            ]
            size = self._count_in_segment(segment, ap, segment_exceptions)
            if remaining < size:
                value = self._kth_in_segment(
                    segment, ap, segment_exceptions, remaining
                )
                return TimePoint(Fraction(value, scale))
            remaining -= size
        raise IndexError(f"fewer than {k + 1} common triggers in window")

    def explain_no_solution(self, lo, hi) -> NoSolutionReport | None:
        """Arithmetic evidence when ``[lo, hi)`` has no common trigger.

        Returns ``None`` if a common trigger actually exists; otherwise a
        report citing, per segment, either the paused streams or the CRT
        conflict (residues disagreeing modulo the gcd of the moduli).
        """
        self._verify()
        lo, hi = Fraction(lo), Fraction(hi)
        scale = self._scale(lo, hi)
        exceptions = self._common_exceptions(lo, hi, scale)
        if exceptions:
            return None
        evidence = []
        for segment in compile_segments(self.streams, lo, hi, scale):
            bounds = (Fraction(segment.lo, scale), Fraction(segment.hi, scale))
            ap, conflict = self._segment_ap(segment)
            if ap is not None:
                residue, modulus, effective_lo = ap
                if ceil_div(segment.hi - residue, modulus) > ceil_div(
                    effective_lo - residue, modulus
                ):
                    return None  # this segment yields solutions
            elif conflict is not None:
                evidence.append(SegmentEvidence(bounds, "crt-conflict", str(conflict)))
            else:
                paused = [c.name for c in segment.constraints if c.paused]
                evidence.append(
                    SegmentEvidence(
                        bounds, "paused-stream", f"streams paused: {', '.join(paused)}"
                    )
                )
        return NoSolutionReport((lo, hi), tuple(evidence))
