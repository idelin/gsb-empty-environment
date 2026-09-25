"""Compile streams plus a window into piecewise-constant constraint segments.

Every pause edge and phase-switch time is a segment boundary, so inside one
segment each stream is either fully paused or a single pure congruence
``t = anchor (mod period)`` with ``t >= anchor``. All values are integers on
the unified scale.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction


@dataclass(frozen=True)
class StreamConstraint:
    """Within one segment a stream is either paused or a pure congruence."""

    anchor: int  # scaled: t = anchor (mod period), t >= anchor
    period: int  # scaled
    paused: bool
    name: str


@dataclass(frozen=True)
class Segment:
    lo: int  # scaled, inclusive
    hi: int  # scaled, exclusive
    constraints: tuple[StreamConstraint, ...]


def compile_segments(streams, lo: Fraction, hi: Fraction, scale: int) -> list[Segment]:
    lo_scaled, hi_scaled = int(lo * scale), int(hi * scale)
    bounds = {lo_scaled, hi_scaled}
    for stream in streams:
        for pause in stream.pauses:
            bounds.add(min(hi_scaled, max(lo_scaled, int(pause.start * scale))))
            bounds.add(min(hi_scaled, max(lo_scaled, int(pause.end * scale))))
        for switch in stream.switches:
            at = int(switch.at * scale)
            if lo_scaled < at < hi_scaled:
                bounds.add(at)
    ordered = sorted(bounds)
    segments = []
    for a, b in zip(ordered, ordered[1:]):
        if a >= b:
            continue
        probe = Fraction(a, scale)
        constraints = []
        for stream in streams:
            paused = any(p.contains(probe) for p in stream.pauses)
            anchor = stream.anchor_at(probe)
            constraints.append(
                StreamConstraint(
                    anchor=int(anchor * scale),
                    period=int(stream.period * scale),
                    paused=paused,
                    name=stream.name,
                )
            )
        segments.append(Segment(a, b, tuple(constraints)))
    return segments
