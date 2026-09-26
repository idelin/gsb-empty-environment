"""Plan compilation: global gain curve, constraint check, source mapping.

Compilation is global, not pairwise: every segment owns an independent gain
envelope (fade-in ramp, steady 1.0, fade-out ramp) and the compiler sums
all envelopes into one global gain curve.  The total-gain constraint is
checked against that summed curve, so overlapping fades of three or more
segments are validated exactly once and no per-pair rescaling ever happens.
"""

from __future__ import annotations

from dataclasses import dataclass

from .curves import fade_in_gain, fade_out_gain
from .errors import GainConstraintError, SegmentValidationError
from .segment import SHORTEN, Segment, resolve_fades, validate_segment

DECLARED_LATENCY = 0      # samples; rendering needs no look-ahead
DEFAULT_TOLERANCE = 1e-9  # numeric tolerance of the gain-constraint check
DEFAULT_MAX_POWER = 1.0   # sum of squared gains may not exceed this


@dataclass(frozen=True)
class CompiledSegment:
    """A non-empty segment with its effective (post-rule) fades."""

    seg: Segment
    fade_in: int
    fade_out: int

    @property
    def offset(self) -> int:
        return self.seg.offset

    @property
    def end(self) -> int:
        return self.seg.end

    @property
    def duration(self) -> int:
        return self.seg.duration


@dataclass(frozen=True)
class Interval:
    """Maps output range [out_start, out_end) to a source run.

    The first output sample of the interval reads source index
    ``src_first``; each subsequent output sample advances by ``step``
    (+1 forward, -1 reversed).  A crossfade region is part of both
    contributors' intervals but exists only once on the output timeline.
    """

    out_start: int
    out_end: int
    seg_id: str
    src_id: str
    src_first: int
    step: int

    def source_at(self, t: int) -> int:
        """Source index read by output sample ``t`` of this interval."""
        return self.src_first + (t - self.out_start) * self.step


def gain_at(cseg: CompiledSegment, i: int) -> float:
    """Gain of a compiled segment at local sample index ``i``."""
    duration = cseg.duration
    if cseg.fade_in and i < cseg.fade_in:
        return fade_in_gain(cseg.seg.curve, i / cseg.fade_in)
    if cseg.fade_out and i >= duration - cseg.fade_out:
        return fade_out_gain(cseg.seg.curve, (i - (duration - cseg.fade_out)) / cseg.fade_out)
    return 1.0


def src_index_at(cseg: CompiledSegment, i: int) -> int:
    """Source index read at local sample index ``i`` (handles reversal)."""
    seg = cseg.seg
    return seg.src_start + i if seg.step > 0 else seg.src_start - 1 - i


class Plan:
    """An immutable compiled plan, ready for chunked or whole rendering.

    Attributes:
        segments: compiled non-empty segments sorted by offset
        sources: mapping of source id to sample sequence
        sample_rate: declared sample rate of sources and output
        total: output length in samples
        intervals: source-to-output mapping intervals
        tolerance: numeric tolerance used by the constraint check
        max_power / max_amplitude: enforced total-gain limits
        latency: declared algorithmic latency in samples (0)
    """

    latency = DECLARED_LATENCY

    def __init__(self, segments, sources, sample_rate, total, intervals,
                 tolerance, max_power, max_amplitude):
        self.segments = segments
        self.sources = sources
        self.sample_rate = sample_rate
        self.total = total
        self.intervals = intervals
        self.tolerance = tolerance
        self.max_power = max_power
        self.max_amplitude = max_amplitude

    def __len__(self) -> int:
        return self.total

    def output_to_source(self, t: int) -> tuple:
        """All (src_id, src_index, seg_id) contributions at output sample ``t``."""
        if not 0 <= t < self.total:
            raise IndexError(f"output sample {t} outside [0, {self.total})")
        hits = []
        for cseg in self.segments:
            if cseg.offset <= t < cseg.end:
                hits.append(
                    (cseg.seg.src_id, src_index_at(cseg, t - cseg.offset), cseg.seg.seg_id)
                )
        return tuple(hits)

    def source_to_output(self, seg_id: str, src_index: int):
        """Output sample that reads ``src_index`` of segment ``seg_id``'s source.

        Returns None when the source index is outside the segment's range.
        """
        for cseg in self.segments:
            if cseg.seg.seg_id == seg_id:
                seg = cseg.seg
                if not seg.src_lo <= src_index < seg.src_hi:
                    return None
                if seg.step > 0:
                    local = src_index - seg.src_start
                else:
                    local = seg.src_start - 1 - src_index
                return seg.offset + local
        raise KeyError(f"unknown segment id: {seg_id!r}")


def compile_plan(segments, sources, sample_rate, *, on_short=SHORTEN,
                 max_power=DEFAULT_MAX_POWER, max_amplitude=None,
                 tolerance=DEFAULT_TOLERANCE) -> Plan:
    """Compile segments into a Plan.

    Empty segments are dropped (they never change the result).  Fades are
    resolved with the short-segment rule (``on_short``).  The global gain
    curve is then checked: wherever two or more segments overlap, the
    summed power (and optionally summed amplitude) must stay within the
    given limits plus ``tolerance``.
    """
    seen = set()
    compiled = []
    for seg in segments:
        if seg.seg_id in seen:
            raise SegmentValidationError(f"duplicate segment id: {seg.seg_id!r}")
        seen.add(seg.seg_id)
        if seg.src_id not in sources:
            raise SegmentValidationError(
                f"{seg.seg_id!r}: unknown source {seg.src_id!r}"
            )
        validate_segment(seg, len(sources[seg.src_id]))
        if seg.is_empty:
            continue
        fade_in, fade_out = resolve_fades(seg, on_short)
        compiled.append(CompiledSegment(seg, fade_in, fade_out))
    compiled.sort(key=lambda c: (c.offset, c.seg.seg_id))
    total = max((c.end for c in compiled), default=0)
    intervals = tuple(_interval_for(c) for c in compiled)
    _check_gain_constraint(compiled, total, max_power, max_amplitude, tolerance)
    return Plan(
        segments=tuple(compiled),
        sources=sources,
        sample_rate=int(sample_rate),
        total=total,
        intervals=intervals,
        tolerance=tolerance,
        max_power=max_power,
        max_amplitude=max_amplitude,
    )


def _interval_for(cseg: CompiledSegment) -> Interval:
    seg = cseg.seg
    src_first = seg.src_start if seg.step > 0 else seg.src_start - 1
    return Interval(
        out_start=seg.offset,
        out_end=seg.end,
        seg_id=seg.seg_id,
        src_id=seg.src_id,
        src_first=src_first,
        step=seg.step,
    )


def _check_gain_constraint(compiled, total, max_power, max_amplitude, tolerance):
    """Validate the summed global gain curve on every multi-segment region.

    A single segment's envelope never exceeds gain 1.0, so only regions
    covered by two or more segments can violate the constraint; those are
    evaluated sample by sample.  This is what makes arbitrarily overlapping
    fade regions (including triple overlaps) safe: they are checked against
    one global curve, not faded pairwise and rescaled.
    """
    if not compiled:
        return
    boundaries = {0, total}
    for cseg in compiled:
        boundaries.add(cseg.offset)
        boundaries.add(cseg.end)
    ordered = sorted(boundaries)
    for region_start, region_end in zip(ordered, ordered[1:]):
        active = [
            c for c in compiled
            if c.offset < region_end and c.end > region_start
        ]
        if len(active) < 2:
            continue
        for t in range(region_start, region_end):
            gains = [gain_at(c, t - c.offset) for c in active]
            power = sum(g * g for g in gains)
            if power > max_power + tolerance:
                raise GainConstraintError(t, power, max_power, kind="power")
            if max_amplitude is not None:
                amplitude = sum(gains)
                if amplitude > max_amplitude + tolerance:
                    raise GainConstraintError(t, amplitude, max_amplitude,
                                              kind="amplitude")
